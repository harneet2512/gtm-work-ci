package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

func TestMineAndFreezeRejectBadArguments(t *testing.T) {
	for _, args := range [][]string{
		{"mine-demo-cases", "extra"},
		{"mine-demo-cases", "--date", "yesterday"},
		{"freeze-demo-manifest"},
		{"freeze-demo-manifest", "--opportunity", "006X", "--held-out", "e"}, // no --out
		{"freeze-demo-manifest", "--opportunity", "006X", "--held-out", "e", "--out", "m.json", "--created-at", "monday"},               // bad time
		{"freeze-demo-manifest", "--opportunity", "006X", "--held-out", "e", "--out", "m.json", "--allow-throwaway"},                    // no --created-at: the hash would depend on the wall clock
		{"freeze-demo-manifest", "--opportunity", "006X", "--held-out", "e", "--out", "m.json", "--created-at", "2026-10-03T12:00:00Z"}, // throwaway database not acknowledged
	} {
		var out bytes.Buffer
		if err := run(args, &out); err == nil {
			t.Errorf("run(%v) succeeded", args)
		}
	}
}

// The commands run on a private embedded Postgres: with DATABASE_URL pointing at nothing they still work,
// the report is written, and the case freezes to a manifest that validates and holds Event N bare.
func TestMineThenFreezeOnTheSampleNeverTouchesDATABASE_URL(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	t.Setenv("DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/never?sslmode=disable")
	t.Setenv("TEST_DATABASE_URL", "")
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run([]string{"mine-demo-cases", "--data", abs(t, crmarenaSample), "--scoring", abs(t, "../../../bench/config/demo_case_scoring.v1.json"),
		"--date", "2026-10-03", "--out-dir", dir}, &out); err != nil {
		t.Fatalf("mine: %v\n%s", err, out.String())
	}
	reportJSON := filepath.Join(dir, "demo-cases-2026-10-03.json")
	raw, err := os.ReadFile(reportJSON)
	if err != nil {
		t.Fatal(err)
	}
	var rep demomine.Report
	if err := json.Unmarshal(raw, &rep); err != nil || len(rep.Top) == 0 {
		t.Fatalf("report: %v, %d cases", err, len(rep.Top))
	}
	if _, err := os.Stat(filepath.Join(dir, "demo-cases-2026-10-03.md")); err != nil {
		t.Fatal(err)
	}
	if rep.Counts.OpportunitiesScanned == 0 || rep.Counts.EventsReplayed == 0 {
		t.Fatalf("the report must count what was scanned: %+v", rep.Counts)
	}
	top := rep.Top[0]
	manifest := filepath.Join(dir, "manifest.json")
	out.Reset()
	err = run([]string{"freeze-demo-manifest", "--data", abs(t, crmarenaSample), "--opportunity", top.OpportunityID,
		"--held-out", top.HeldOut.EventID, "--report", reportJSON, "--out", manifest, "--created-at", "2026-10-03T12:00:00Z", "--allow-throwaway"}, &out)
	if err != nil {
		t.Fatalf("freeze: %v\n%s", err, out.String())
	}
	doc, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("demo_manifest", doc); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if err := demomine.VerifyHash(doc); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), top.HeldOut.SourceObjectID) && strings.Count(string(doc), top.HeldOut.SourceObjectID) != 1 {
		t.Fatalf("Event N's source object appears more than once in the manifest (only as the bare held-out event)")
	}
}
