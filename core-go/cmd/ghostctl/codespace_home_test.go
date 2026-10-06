package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUseDemoHomeRedirectsRunnerStateAndTheNeoCacheUnderTheHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GHOST_DEMO_HOME", home)
	t.Setenv("GHOST_DEMO_DIR", "")
	t.Setenv("GHOST_NEO4J_CACHE", "")
	useDemoHome()
	if got := os.Getenv("GHOST_DEMO_DIR"); got != home {
		t.Fatalf("GHOST_DEMO_DIR = %q, want %q", got, home)
	}
	if got := os.Getenv("GHOST_NEO4J_CACHE"); got != filepath.Join(home, "cache", "neo4j") {
		t.Fatalf("GHOST_NEO4J_CACHE = %q", got)
	}
	t.Setenv("GHOST_NEO4J_CACHE", "E:/mine")
	useDemoHome()
	if got := os.Getenv("GHOST_NEO4J_CACHE"); got != "E:/mine" {
		t.Fatalf("an explicit cache must win: %q", got)
	}
}

func TestCodespaceDownOnAStoppedDemoIsANoOp(t *testing.T) {
	codespaceRepo(t)
	var out bytes.Buffer
	if err := run([]string{"codespace", "down"}, &out); err != nil {
		t.Fatalf("down: %v", err)
	}
}
