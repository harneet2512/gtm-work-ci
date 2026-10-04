package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadEnvFileReturnsPairsForTheWorkerAndOnlyNamesForTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := utf8BOM + "# a comment\n\nGHOST_MODEL=openrouter/qwen/qwen3.8-27b:free\nexport OPENROUTER_API_KEY=\"sk-test-not-a-real-key\"\nGHOST_LLM_MAX_RPM='15'\nnot a pair\n=novalue\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs, names, err := readEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names, ","); got != "GHOST_MODEL,OPENROUTER_API_KEY,GHOST_LLM_MAX_RPM" {
		t.Fatalf("names = %s", got)
	}
	if pairs[1] != "OPENROUTER_API_KEY=sk-test-not-a-real-key" || pairs[2] != "GHOST_LLM_MAX_RPM=15" {
		t.Fatalf("pairs = %v", pairs)
	}
	for _, n := range names {
		if strings.Contains(n, "=") || strings.Contains(n, "sk-test") {
			t.Fatalf("a name must never carry a value: %q", n)
		}
	}
	if p, n, err := readEnvFile(""); p != nil || n != nil || err != nil {
		t.Fatal("no env file means no variables")
	}
	if _, _, err := readEnvFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a named env file that does not exist must be an error")
	}
}

func TestSplitListAndRepoRootFrom(t *testing.T) {
	if got := splitList(" a, b ,,c "); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("splitList = %v", got)
	}
	if splitList("") != nil {
		t.Fatal("an empty list is nil")
	}
	if got := repoRootFrom(filepath.Join("repo", "contracts", "knowledge", "lifecycle.v1.json")); got != "repo" {
		t.Fatalf("repoRootFrom = %q", got)
	}
}

func TestTheABCCommandsRefuseMissingFlagsWithTheirUsage(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"abc-pack"}, &out); err == nil || !strings.Contains(err.Error(), "usage: ghostctl abc-pack") {
		t.Fatalf("abc-pack: %v", err)
	}
	if err := run([]string{"abc-arms", "--pack", "x"}, &out); err == nil || !strings.Contains(err.Error(), "usage: ghostctl abc-arms") {
		t.Fatalf("abc-arms: %v", err)
	}
}

func TestWriteJSONFileCreatesTheDirectoryAndEndsWithANewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "dir", "out.json")
	if err := writeJSONFile(path, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{\"a\":1}\n" {
		t.Fatalf("%q %v", raw, err)
	}
}
