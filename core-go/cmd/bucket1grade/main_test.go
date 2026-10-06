package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bundle = `{"id":"ep-13","at":"2026-03-10T09:00:00Z","account_id":"acc-1",
"activities":[{"id":"a1","source":"email","external_id":"m1","speaker_id":"p","text":"the DPA is signed","occurred_at":"2026-03-10T09:00:00Z","account_id":"acc-1"}],
"claims":[{"id":"c1","activity_id":"a1","account_id":"acc-1","kind":"fact","field":"blockers","value":"DPA signed","quote":"the DPA is signed","speaker_id":"p","occurred_at":"2026-03-10T09:00:00Z","status":"active"}]}`

func TestRunWritesNineResults(t *testing.T) {
	dir := t.TempDir()
	in, outFile := filepath.Join(dir, "e.json"), filepath.Join(dir, "r.json")
	if err := os.WriteFile(in, []byte(bundle), 0o644); err != nil {
		t.Fatal(err)
	}
	sink, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := run([]string{"-episode", in, "-out", outFile}, sink); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(outFile)
	if strings.Count(string(raw), `"gate": "B`) != 9 {
		t.Fatalf("results: %s", raw)
	}
}

func TestRunRefusesMissingFlagsAndBadBundles(t *testing.T) {
	if err := run(nil, os.Stdout); err == nil {
		t.Fatal("flags are required")
	}
	if err := run([]string{"-episode", filepath.Join(t.TempDir(), "none.json"), "-out", "x"}, os.Stdout); err == nil {
		t.Fatal("a missing bundle is an error")
	}
	if err := run([]string{"-bogus"}, os.Stdout); err == nil {
		t.Fatal("a bad flag is an error")
	}
}
