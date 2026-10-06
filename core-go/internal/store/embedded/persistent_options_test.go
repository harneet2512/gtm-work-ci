package embedded

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentOptionsAreValidatedBeforeAnythingStarts(t *testing.T) {
	for name, o := range map[string]PersistentOptions{
		"no directory": {Port: 15432},
		"port zero":    {Dir: t.TempDir()},
		"port too big": {Dir: t.TempDir(), Port: 70000},
	} {
		if _, stop, err := LaunchPersistent(o); err == nil || stop != nil {
			t.Errorf("%s: LaunchPersistent = %v, want a refusal", name, err)
		}
	}
}

func TestPersistentNamesDefaultToALoopbackDemoDatabaseAndShowInTheDSN(t *testing.T) {
	db, user, pw := PersistentOptions{}.names()
	if db != "ghost_demo" || user != "ghost" || pw != "ghost" {
		t.Fatalf("defaults = %s %s %s", db, user, pw)
	}
	dsn := PersistentOptions{Port: 15555, Database: "d", User: "u", Password: "p"}.DSN()
	if dsn != "postgres://u:p@127.0.0.1:15555/d?sslmode=disable" {
		t.Fatalf("dsn = %s", dsn)
	}
}

func TestLaunchPersistentReportsADirectoryItCannotCreate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := LaunchPersistent(PersistentOptions{Dir: filepath.Join(file, "under"), Port: 15444})
	if err == nil || !strings.Contains(err.Error(), "create") {
		t.Fatalf("err = %v", err)
	}
}

func TestWipePersistentRefusesAnEmptyDirectoryAndWipesDataAndRuntime(t *testing.T) {
	if err := WipePersistent(PersistentOptions{}); err == nil {
		t.Fatal("an empty option must never wipe the working directory")
	}
	root := t.TempDir()
	for _, d := range []string{"data", "runtime", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := WipePersistent(PersistentOptions{Dir: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin")); err != nil {
		t.Fatal("the extracted binaries are kept")
	}
	for _, d := range []string{"data", "runtime"} {
		if _, err := os.Stat(filepath.Join(root, d)); err == nil {
			t.Fatalf("%s must be gone", d)
		}
	}
}
