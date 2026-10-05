package main

// learn report tests: the flag-validation matrix runs without a database (rejected combinations fail
// before config.Load), and the end-to-end legs run against an embedded Postgres — an empty world and
// one registered version — asserting the stdout contract is a JSON document and nothing else.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func TestLearnReportFlagValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://unused/db") // validation fails before it is ever dialed
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no selector", []string{"report"}, "pass --evaluator"},
		{"all with evaluator", []string{"report", "--all", "--evaluator", "grounding"}, "cannot combine"},
		{"all with version", []string{"report", "--all", "--version", "2"}, "cannot combine"},
		{"version alone", []string{"report", "--version", "2"}, "--evaluator"},
		{"unknown axis", []string{"report", "--evaluator", "not_an_axis"}, "not an eval axis"},
		{"positional arg", []string{"report", "--all", "extra"}, "usage"},
		{"negative version", []string{"report", "--evaluator", "grounding", "--version", "-1"}, "usage"},
		{"bad flag", []string{"report", "--bogus"}, "usage"},
		{"unknown subcommand", []string{"sideways"}, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runLearn(tc.args, &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("learn %v: err = %v, want %q", tc.args, err, tc.want)
			}
		})
	}
}

// TestLearnReport runs the command end to end against an embedded Postgres: the empty world emits a
// valid empty document; a registered version emits its report keyed '<evaluator>:v<N>'; --evaluator
// selects; --out writes the file. The first byte of stdout must be the JSON — no operational text.
func TestLearnReport(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)

	// Empty world: --all is a valid document with no reports.
	var out bytes.Buffer
	if err := runLearn([]string{"report", "--all"}, &out); err != nil {
		t.Fatalf("learn report --all: %v", err)
	}
	var doc struct {
		GeneratedAt string          `json:"generated_at"`
		Reports     json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &doc); err != nil {
		t.Fatalf("stdout is not a clean JSON document: %v\n%s", err, out.String())
	}
	if doc.GeneratedAt == "" || string(doc.Reports) != "{}" {
		t.Fatalf("empty-world document = %s", out.String())
	}

	// One registered version -> one keyed report; --evaluator alone reports every version of the axis.
	if _, err := env.DB.Exec(`INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, created_from)
 VALUES ('cta_calibration', 9, 'candidate', 'semantic', 'x', 'manual')`); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	out.Reset()
	if err := runLearn([]string{"report", "--evaluator", "cta_calibration"}, &out); err != nil {
		t.Fatalf("learn report --evaluator: %v", err)
	}
	var set struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &set); err != nil {
		t.Fatalf("stdout is not a clean JSON document: %v\n%s", err, out.String())
	}
	if _, ok := set.Reports["cta_calibration:v9"]; !ok || len(set.Reports) != 1 {
		t.Fatalf("reports = %v, want just cta_calibration:v9", keysOf(set.Reports))
	}

	// S4: a version or evaluator filter that misses is an error (non-zero exit), never an empty
	// document a CI gate would read as a pass — and nothing reaches stdout.
	for _, args := range [][]string{
		{"report", "--evaluator", "cta_calibration", "--version", "1"},
		{"report", "--evaluator", "grounding"},
	} {
		out.Reset()
		err := runLearn(args, &out)
		if err == nil || !errors.Is(err, evalreport.ErrUnknownSelection) {
			t.Fatalf("learn %v: err = %v, want ErrUnknownSelection", args, err)
		}
		if out.Len() != 0 {
			t.Fatalf("a failed selection wrote to stdout: %q", out.String())
		}
	}

	// --out writes the document to a file and leaves a one-line confirmation on stdout.
	out.Reset()
	path := filepath.Join(t.TempDir(), "report.json")
	if err := runLearn([]string{"report", "--all", "--out", path}, &out); err != nil {
		t.Fatalf("learn report --out: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read --out file: %v", err)
	}
	var fileDoc struct {
		Reports map[string]json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal(raw, &fileDoc); err != nil || len(fileDoc.Reports) != 1 {
		t.Fatalf("--out document: %v reports=%d", err, len(fileDoc.Reports))
	}
	if !strings.Contains(out.String(), "wrote 1 report(s)") {
		t.Fatalf("stdout confirmation = %q", out.String())
	}
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
