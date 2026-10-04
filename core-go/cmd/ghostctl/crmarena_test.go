package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const crmarenaSample = "../../../fixtures/crmarena_sample"

func TestCRMArenaCommandsUsageNeedsNoDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	bad := [][]string{
		{"import-crmarena"}, {"import-crmarena", "a", "b"}, {"import-crmarena", "--allow-mixed"}, {"crmarena-split", "dir"}, {"crmarena-split", "--cutoff"},
		{"crmarena-split", "--previous-min-quiet-days", "-1", "a", "b"}, {"crmarena-split", "--previous-min-quiet-days"},
		{"import-crmarena", "--until", "2023-11-01", "a"}, {"import-crmarena", "--until"},
		{"import-crmarena", "--full-timeline", "--until", "2023-11-01T00:00:00Z", "a"},
		{"replay-crmarena"}, {"replay-crmarena", "--deal", "x", "dir"}, {"replay-crmarena", "--from", "yesterday", "dir"}, {"replay-crmarena", "dir"},
		{"crmarena-split", "--seed", "x", "a", "b"}, {"crmarena-report", "--out"}, {"crmarena-report", "x", "y", "z"},
	}
	for _, args := range bad {
		if err := run(args, &out); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if err := run([]string{"import-crmarena", "--full-timeline", filepath.Join(t.TempDir(), "missing")}, &out); err == nil ||
		strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("a missing export must fail before the database is needed: %v", err)
	}
}

func TestImportCRMArenaDryRunAndSplitOnTheSample(t *testing.T) {
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--dry-run", "--full-timeline", sample}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "built 336 events") || !strings.Contains(out.String(), "replies linked") {
		t.Fatalf("dry run output: %s", out.String())
	}
	out.Reset()
	if err := run([]string{"crmarena-split", "--cutoff", "2023-11-01", "--seed", "3", sample, "split.json"}, &out); err != nil {
		t.Fatal(err)
	}
	var split struct {
		Cutoff   string   `json:"cutoff"`
		Seed     int64    `json:"seed"`
		Previous []string `json:"previous_deal_ids"`
		Current  []string `json:"current_deal_ids"`
		Quiet    int      `json:"previous_min_quiet_days"`
		Deals    []struct {
			ID   string `json:"deal_id"`
			Days *int   `json:"days_quiet_at_cutoff"`
		} `json:"previous_deals"`
	}
	raw, err := os.ReadFile("split.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &split); err != nil {
		t.Fatal(err)
	}
	if split.Cutoff != "2023-11-01" || split.Seed != 3 || len(split.Previous) == 0 || len(split.Current) == 0 {
		t.Fatalf("split: %+v (output %s)", split, out.String())
	}
	if split.Quiet != 60 || len(split.Deals) != len(split.Previous) || split.Deals[0].Days == nil || *split.Deals[0].Days < 60 {
		t.Fatalf("split lacks the quiet-period manifest or per-deal quiet days: %+v", split)
	}
}

func TestImportCRMArenaSampleAgainstTestDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--full-timeline", sample}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "336 events: 336 new, 0 duplicate") {
		t.Fatalf("first import: %s", out.String())
	}
	out.Reset()
	if err := run([]string{"import-crmarena", "--full-timeline", sample}, &out); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if !strings.Contains(out.String(), "336 events: 0 new, 336 duplicate") || !strings.Contains(out.String(), "0 created") {
		t.Fatalf("re-import is not idempotent: %s", out.String())
	}

	out.Reset()
	if err := run([]string{"crmarena-report", "--out", "report.json"}, &out); err != nil {
		t.Fatal(err)
	}
	var rep IngestReport
	raw, _ := os.ReadFile("report.json")
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	checks := map[string][2]int{
		"activities":               {rep.Activities, 336},
		"accounts":                 {rep.Entities["accounts"], 3},
		"contacts":                 {rep.Entities["contacts"], 17},
		"opportunities":            {rep.Entities["opportunities"], 26},
		"owned opps":               {rep.Entities["opportunities_with_owner"], 26},
		"contacts at our domain":   {rep.Anomalies["contacts_at_our_domain"], 0},
		"cross-account deal links": {rep.Anomalies["activities_on_another_accounts_deal"], 0},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %d, want %d", name, c[0], c[1])
		}
	}
	if rep.Unresolved > rep.Activities/10 {
		t.Errorf("unresolved %d of %d: %v", rep.Unresolved, rep.Activities, rep.UnresolvedByReason)
	}
	out.Reset()
	if err := run([]string{"crmarena-report"}, &out); err != nil || !strings.Contains(out.String(), `"activities": 336`) {
		t.Errorf("report to stdout: %v %s", err, out.String())
	}
	assertRawRecordsTraceThroughIngest(t, env.DB)
	assertLegacyWorldGuard(t, env.DB, sample)
}

// TestCRMArenaFullSnapshot ingests the whole local export (HAR-130 item 4). It only runs when asked:
//
//	CRMARENA_DIR=<repo>/data/crmarena_b2b CRMARENA_REPORT=<out.json> go test ./cmd/ghostctl -run FullSnapshot -timeout 2h
func TestCRMArenaFullSnapshot(t *testing.T) {
	dir, report := os.Getenv("CRMARENA_DIR"), os.Getenv("CRMARENA_REPORT")
	if dir == "" || report == "" {
		t.Skip("set CRMARENA_DIR and CRMARENA_REPORT to ingest the full snapshot")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--full-timeline", dir}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	if err := run([]string{"crmarena-report", "--out", report}, &out); err != nil {
		t.Fatal(err)
	}
	t.Log(out.String())
}
