package codespace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// treePrint is the fingerprint of a file or directory tree. Layout covers every directory, file name and size (cheap: stat only);
// Content also covers every byte (a full read, so it is computed only when sealing and on a deep verify).
type treePrint struct {
	Files   int
	Bytes   int64
	Layout  string
	Content string
}

type treeEntry struct {
	rel  string
	dir  bool
	size int64
	mode fs.FileMode
}

// scan lists a tree in lexical order. A single file is a tree of one entry named "".
func scan(root string) ([]treeEntry, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []treeEntry{{rel: "", size: info.Size(), mode: info.Mode().Perm()}}, nil
	}
	var out []treeEntry
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			out = append(out, treeEntry{rel: rel, dir: true, mode: fi.Mode().Perm()})
			return nil
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("codespace: %s is not a regular file", path)
		}
		out = append(out, treeEntry{rel: rel, size: fi.Size(), mode: fi.Mode().Perm()})
		return nil
	})
	return out, err
}

// layoutOf fingerprints a listing without reading any file.
func layoutOf(entries []treeEntry) treePrint {
	h := sha256.New()
	var p treePrint
	for _, e := range entries {
		if e.dir {
			fmt.Fprintf(h, "d\x00%s\n", e.rel)
			continue
		}
		fmt.Fprintf(h, "f\x00%s\x00%d\n", e.rel, e.size)
		p.Files++
		p.Bytes += e.size
	}
	p.Layout = hex.EncodeToString(h.Sum(nil))
	return p
}

// contentOf combines per-file hashes (aligned with entries) into one digest.
func contentOf(entries []treeEntry, sums []string) string {
	h := sha256.New()
	for i, e := range entries {
		if !e.dir {
			fmt.Fprintf(h, "%s\x00%s\n", e.rel, sums[i])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fingerprint fingerprints path; withContent also reads and hashes every file.
func fingerprint(path string, withContent bool) (treePrint, error) {
	entries, err := scan(path)
	if err != nil {
		return treePrint{}, err
	}
	p := layoutOf(entries)
	if !withContent {
		return p, nil
	}
	sums := make([]string, len(entries))
	err = forEachFile(context.Background(), entries, func(i int, e treeEntry) error {
		f, err := os.Open(joinRel(path, e.rel))
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		sums[i] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	p.Content = contentOf(entries, sums)
	return p, err
}

func joinRel(root, rel string) string {
	if rel == "" {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// forEachFile runs fn for every file entry on a small worker pool and returns the first error.
func forEachFile(ctx context.Context, entries []treeEntry, fn func(i int, e treeEntry) error) error {
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	fail := func(err error) { once.Do(func() { first = err }) }
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if err := fn(i, entries[i]); err != nil {
					fail(err)
				}
			}
		}()
	}
	for i, e := range entries {
		if e.dir {
			continue
		}
		if ctx.Err() != nil {
			fail(ctx.Err())
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return first
}

// copyTree copies a file or directory tree (empty directories included) and returns the fingerprint of what it read, with
// the content digest when withContent is set. dst must not exist.
func copyTree(ctx context.Context, src, dst string, withContent bool) (treePrint, error) {
	entries, err := scan(src)
	if err != nil {
		return treePrint{}, err
	}
	if len(entries) == 1 && entries[0].rel == "" {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return treePrint{}, err
		}
	} else {
		root, err := os.Stat(src)
		if err != nil {
			return treePrint{}, err
		}
		if err := makeDir(dst, root.Mode().Perm()); err != nil {
			return treePrint{}, err
		}
	}
	for _, e := range entries {
		if e.dir {
			if err := makeDir(joinRel(dst, e.rel), e.mode); err != nil {
				return treePrint{}, err
			}
		}
	}
	sums := make([]string, len(entries))
	err = forEachFile(ctx, entries, func(i int, e treeEntry) error {
		sum, err := copyFile(joinRel(src, e.rel), joinRel(dst, e.rel), e.mode, withContent)
		sums[i] = sum
		return err
	})
	if err != nil {
		return treePrint{}, err
	}
	p := layoutOf(entries)
	if withContent {
		p.Content = contentOf(entries, sums)
	}
	return p, nil
}

// makeDir creates a directory with exactly the given permissions, whatever the umask: Postgres refuses a data directory
// that is not private (0700), so a copy must keep the modes of the original.
func makeDir(path string, mode fs.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func copyFile(src, dst string, mode fs.FileMode, withHash bool) (sum string, err error) {
	if !withHash { // a restore does not need the bytes read in Go: let the operating system copy
		if handled, err := nativeCopyFile(src, dst); handled {
			return "", err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode|0o600)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	var w io.Writer = out
	h := sha256.New()
	if withHash {
		w = io.MultiWriter(out, h)
	}
	if _, err := io.CopyBuffer(w, in, make([]byte, 1<<20)); err != nil {
		return "", err
	}
	if withHash {
		sum = hex.EncodeToString(h.Sum(nil))
	}
	return sum, nil
}
