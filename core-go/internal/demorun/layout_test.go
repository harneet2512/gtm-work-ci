package demorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"core-go", "worker-py", "web"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindRootWalksUpToTheDirectoryWithClaudeMdAndCoreGo(t *testing.T) {
	root := fakeRepo(t)
	deep := filepath.Join(root, "core-go", "cmd", "ghostctl")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRoot(deep)
	if err != nil || got != root {
		t.Fatalf("FindRoot = %q, %v; want %q", got, err, root)
	}
	if _, err := FindRoot(t.TempDir()); err == nil {
		t.Fatal("a directory outside the repository must be an error")
	}
}

func TestMainCheckoutOfAWorktree(t *testing.T) {
	sep := string(filepath.Separator)
	main := filepath.Join(t.TempDir(), "gtm")
	wt := filepath.Join(main, ".claude", "worktrees", "agent-1")
	if got := MainCheckout(wt); got != main {
		t.Fatalf("MainCheckout(worktree) = %q, want %q", got, main)
	}
	if got := MainCheckout(main); got != main {
		t.Fatalf("MainCheckout(main) = %q, want itself", got)
	}
	if strings.Contains(MainCheckout(wt), sep+".claude") {
		t.Fatal("still inside .claude")
	}
}

func TestLayoutPathsAreUnderDemoDir(t *testing.T) {
	l := NewLayout("/repo")
	for name, p := range map[string]string{
		"pid": l.PIDDir(), "log": l.LogFile("core"), "pg": l.PGDir(), "neo": l.Neo4jDir(),
		"bin": l.BinDir(), "secrets": l.SecretsFile(), "manifest": l.ManifestFile(), "events": l.ReplayEventsDir(),
	} {
		if !strings.HasPrefix(filepath.ToSlash(p), "/repo/.demo/") {
			t.Errorf("%s = %s is not under .demo", name, p)
		}
	}
	if !strings.HasSuffix(l.LogFile("core"), "core.log") {
		t.Fatalf("log file = %s", l.LogFile("core"))
	}
}

func TestStateDirOverridesTheDemoDirectory(t *testing.T) {
	l := NewLayout("/repo")
	l.StateDir = "/tmp/smoke-state"
	if got := filepathSlash(l.PGDir()); got != "/tmp/smoke-state/pg" {
		t.Fatalf("PGDir = %q: GHOST_DEMO_DIR must relocate everything", got)
	}
	if got := filepathSlash(l.LogFile("core")); got != "/tmp/smoke-state/logs/core.log" {
		t.Fatalf("LogFile = %q", got)
	}
}

func TestDefaultPortsAndOverrides(t *testing.T) {
	p := DefaultPorts(Env{})
	if p.Core != 8080 || p.Worker != 8090 || p.Web != 3000 || p.Postgres == 0 || p.Bolt == 0 {
		t.Fatalf("defaults wrong: %+v", p)
	}
	q := DefaultPorts(Env{"GHOST_DEMO_PORT_CORE": "18080", "GHOST_DEMO_PORT_WEB": "13000", "GHOST_DEMO_PORT_WORKER": "bogus"})
	if q.Core != 18080 || q.Web != 13000 || q.Worker != 8090 {
		t.Fatalf("overrides wrong (a bad value must fall back to the default): %+v", q)
	}
	seen := map[int]string{}
	for name, port := range map[string]int{"core": p.Core, "worker": p.Worker, "web": p.Web, "pg": p.Postgres, "bolt": p.Bolt} {
		if other, dup := seen[port]; dup {
			t.Fatalf("%s and %s share port %d", name, other, port)
		}
		seen[port] = name
	}
}

func TestEnsureSecretsGeneratesOnceAndNeverReturnsThemInErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "secrets.env")
	first, err := EnsureSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"GHOST_API_TOKEN", "GHOST_RUN_TOKEN_SECRET", "NEO4J_PASSWORD"} {
		if len(first[k]) < 32 {
			t.Errorf("%s is missing or too short", k)
		}
	}
	second, err := EnsureSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range first.Names() {
		if first[k] != second[k] {
			t.Errorf("%s changed between calls: secrets must be stable across restarts", k)
		}
	}
	// A partially written file is completed, never regenerated wholesale.
	if err := os.WriteFile(path, []byte("GHOST_API_TOKEN="+first["GHOST_API_TOKEN"]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := EnsureSecrets(path)
	if err != nil {
		t.Fatal(err)
	}
	if third["GHOST_API_TOKEN"] != first["GHOST_API_TOKEN"] || third["NEO4J_PASSWORD"] == "" {
		t.Fatal("existing values must be kept and missing ones added")
	}
}
