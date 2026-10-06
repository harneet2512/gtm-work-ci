package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed measurement is exactly what the committed command produces from the committed gold and rule set.
func TestTransitionsEvalReproducesTheCommittedReport(t *testing.T) {
	committed, err := os.ReadFile(resolveRepoFile("", "bench/reports/transitions-gold-2026-10-03.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "report.json")
	var stdout bytes.Buffer
	if err := run([]string{"transitions-eval", "--date", "2026-10-03", "--out", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))) {
		t.Fatal("bench/reports/transitions-gold-2026-10-03.json is stale: rerun `go run ./cmd/ghostctl transitions-eval --date 2026-10-03 --out ../bench/reports/transitions-gold-2026-10-03.json`")
	}
	if !strings.Contains(stdout.String(), "premature=") {
		t.Errorf("summary = %s", stdout.String())
	}
}

func TestTransitionsEvalRefusesUnknownArguments(t *testing.T) {
	if err := run([]string{"transitions-eval", "extra"}, &bytes.Buffer{}); err == nil {
		t.Fatal("want a usage error")
	}
}
