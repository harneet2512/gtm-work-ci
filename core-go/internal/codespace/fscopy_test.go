package codespace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyTreeKeepsEmptyDirectoriesAndEveryByteWithAndWithoutHashing(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"a/b/file": "payload", "top": "x", "a/empty-sibling": ""})
	if err := os.MkdirAll(filepath.Join(src, "pg_twophase", "nested"), 0o755); err != nil { // empty directories are state too
		t.Fatal(err)
	}
	want, err := fingerprint(src, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, withContent := range []bool{false, true} {
		dst := filepath.Join(t.TempDir(), "copy")
		got, err := copyTree(context.Background(), src, dst, withContent)
		if err != nil {
			t.Fatalf("withContent=%v: %v", withContent, err)
		}
		if got.Layout != want.Layout || got.Files != want.Files || got.Bytes != want.Bytes || (withContent && got.Content != want.Content) {
			t.Fatalf("withContent=%v: copy fingerprint %+v, want %+v", withContent, got, want)
		}
		again, err := fingerprint(dst, true)
		if err != nil || again.Content != want.Content || again.Layout != want.Layout {
			t.Fatalf("withContent=%v: the copy on disk differs: %+v %v", withContent, again, err)
		}
		if _, err := os.Stat(filepath.Join(dst, "pg_twophase", "nested")); err != nil {
			t.Fatalf("an empty directory was lost: %v", err)
		}
	}
}

// Postgres refuses a data directory that is not private, so a restored cluster must keep the modes of the original.
func TestCopyTreeKeepsPrivateDirectoryAndFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX permission bits")
	}
	src := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(src, "base", "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "base", "1", "rel"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if _, err := copyTree(context.Background(), src, dst, false); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dst: 0o700, filepath.Join(dst, "base"): 0o700, filepath.Join(dst, "base", "1"): 0o700, filepath.Join(dst, "base", "1", "rel"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v (%v), want %v", path, info.Mode().Perm(), err, want)
		}
	}
}

func TestCopyTreeOverwritesAnExistingFileAndCopiesASingleFile(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"one": "new", "target": "old-and-longer"})
	if _, err := copyTree(context.Background(), filepath.Join(dir, "one"), filepath.Join(dir, "target"), false); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "target")); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
}

func TestCopyTreeReportsAMissingSourceAndHonoursCancellation(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "x")
	if _, err := copyTree(context.Background(), filepath.Join(t.TempDir(), "absent"), dst, false); err == nil {
		t.Fatal("a missing source must be an error")
	}
	src := t.TempDir()
	writeTree(t, src, map[string]string{"f": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyTree(ctx, src, dst, false); err == nil {
		t.Fatal("a cancelled copy must stop")
	}
	if _, err := copyFile(filepath.Join(src, "absent"), filepath.Join(src, "out"), 0o644, false); err == nil {
		t.Fatal("copying a missing file must fail")
	}
	if _, err := copyFile(filepath.Join(src, "absent"), filepath.Join(src, "out"), 0o644, true); err == nil {
		t.Fatal("hashing a missing file must fail")
	}
}
