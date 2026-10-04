package neo4jtest

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pinned Neo4j Community distribution. The SHA-256 is the one dist.neo4j.org publishes next to the
// archive (<url>.sha256) and was re-computed locally when it was pinned. Bump all three together,
// and keep the CI service image (.github/workflows/ci.yml) on the same minor line.
const (
	Version      = "5.26.31"
	ArchiveSHA   = "f8fc23340561405f1ff10ca6ac2d317d095d3c74509a616883c45d7a61f5cfec"
	downloadBase = "https://dist.neo4j.org/"
	maxFileBytes = 1 << 30 // no single file in the archive may be larger than this
)

func archiveName() string { return "neo4j-community-" + Version + "-unix.tar.gz" }

func distDirName() string { return "neo4j-community-" + Version }

// Fetcher downloads and unpacks the pinned distribution into a cache directory.
type Fetcher struct {
	CacheDir string
	URL      string // archive URL; defaults to the pinned dist.neo4j.org URL
	SHA256   string // expected archive checksum; defaults to ArchiveSHA
	Client   *http.Client
}

func (f Fetcher) url() string {
	if f.URL != "" {
		return f.URL
	}
	return downloadBase + archiveName()
}

func (f Fetcher) wantSHA() string {
	if f.SHA256 != "" {
		return f.SHA256
	}
	return ArchiveSHA
}

// Ensure returns the unpacked distribution directory (NEO4J_HOME), downloading and verifying the
// archive when it is not cached. A checksum mismatch is a hard error, never a skip: the archive is
// deleted and the caller must investigate. A network failure is a skip (wrapped ErrSkip).
func (f Fetcher) Ensure(ctx context.Context) (string, error) {
	home := filepath.Join(f.CacheDir, distDirName())
	if _, err := os.Stat(filepath.Join(home, "lib")); err == nil {
		return home, nil
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return "", fmt.Errorf("neo4jtest: create cache dir: %w", err)
	}
	archive := filepath.Join(f.CacheDir, archiveName())
	if err := f.verifiedArchive(ctx, archive); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(f.CacheDir, "extract-")
	if err != nil {
		return "", fmt.Errorf("neo4jtest: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := extractTarGz(archive, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(tmp, distDirName()), home); err != nil {
		if _, statErr := os.Stat(filepath.Join(home, "lib")); statErr == nil { // another process won the race
			return home, nil
		}
		return "", fmt.Errorf("neo4jtest: install distribution: %w", err)
	}
	return home, nil
}

// verifiedArchive makes sure archive exists and matches the pinned checksum.
func (f Fetcher) verifiedArchive(ctx context.Context, archive string) error {
	if _, err := os.Stat(archive); err == nil {
		got, err := fileSHA256(archive)
		if err != nil {
			return err
		}
		if got == f.wantSHA() {
			return nil
		}
		_ = os.Remove(archive) // a corrupt cache entry is re-downloaded
	}
	if err := f.download(ctx, archive); err != nil {
		return err
	}
	got, err := fileSHA256(archive)
	if err != nil {
		return err
	}
	if got != f.wantSHA() {
		_ = os.Remove(archive)
		return fmt.Errorf("neo4jtest: checksum mismatch for %s: got %s, pinned %s", f.url(), got, f.wantSHA())
	}
	return nil
}

func (f Fetcher) download(ctx context.Context, dest string) error {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url(), nil)
	if err != nil {
		return fmt.Errorf("neo4jtest: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return skipf("cannot download Neo4j %s (%v); offline? point NEO4J_TEST_URI at a running Neo4j or pre-seed the cache dir", Version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return skipf("cannot download Neo4j %s: %s answered %s", Version, f.url(), resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "download-")
	if err != nil {
		return fmt.Errorf("neo4jtest: temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return skipf("download of Neo4j %s interrupted: %v", Version, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("neo4jtest: close download: %w", err)
	}
	return os.Rename(tmp.Name(), dest)
}

func fileSHA256(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("neo4jtest: open %s: %w", path, err)
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", fmt.Errorf("neo4jtest: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz unpacks archive into dest, refusing entries that escape dest.
func extractTarGz(archive, dest string) error {
	fh, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("neo4jtest: open archive: %w", err)
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		return fmt.Errorf("neo4jtest: gunzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("neo4jtest: read archive: %w", err)
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(target+string(os.PathSeparator), root) {
			return fmt.Errorf("neo4jtest: archive entry %q escapes the target directory", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeEntry(target, tr, hdr)
		}
		if err != nil {
			return err
		}
	}
}

func writeEntry(target string, r io.Reader, hdr *tar.Header) error {
	if hdr.Size > maxFileBytes {
		return fmt.Errorf("neo4jtest: archive entry %q is %d bytes, over the limit", hdr.Name, hdr.Size)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("neo4jtest: mkdir: %w", err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777|0o600)
	if err != nil {
		return fmt.Errorf("neo4jtest: create %s: %w", target, err)
	}
	if _, err := io.CopyN(out, r, hdr.Size); err != nil {
		_ = out.Close()
		return fmt.Errorf("neo4jtest: write %s: %w", target, err)
	}
	return out.Close()
}
