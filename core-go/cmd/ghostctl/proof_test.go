package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestProofHar129WritesTheRunDirectory drives the documented command end to end (no database).
func TestProofHar129WritesTheRunDirectory(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"proof-har129", "--out", dir, "--run-id", "har129-cmd-test"}, &out); err != nil {
		t.Fatalf("proof-har129: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "har129-cmd-test") || !strings.Contains(text, "NOT RUN") {
		t.Fatalf("unexpected output: %s", text)
	}
	entries, err := os.ReadDir(dir + string(os.PathSeparator) + "har129-cmd-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 13 {
		t.Fatalf("run dir has %d files, want 13", len(entries))
	}
	for _, name := range []string{"manifest.json", "demo-report.md", "e2e-results.json"} {
		if _, err := os.Stat(dir + string(os.PathSeparator) + "har129-cmd-test" + string(os.PathSeparator) + name); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}
