package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demomine"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

// checkReplayDataset: --replay-events-out wrote the replay dataset Play reads (GHOST_REPLAY_EVENTS): exactly one
// SourceEvent, found by the manifest's held-out identity, whose payload is the one the manifest pins.
func checkReplayDataset(t *testing.T, dir string, manifestJSON []byte) {
	t.Helper()
	var m struct {
		Held struct {
			Pin  string `json:"payload_sha256"`
			Prov struct {
				Source struct {
					System string `json:"source_system"`
					Object string `json:"source_object_id"`
				} `json:"source"`
				Key string `json:"source_event_key"`
			} `json:"provenance"`
		} `json:"held_out_event"`
	}
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	src, err := play.NewFileSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := src.Lookup(context.Background(), play.SourceRef{System: m.Held.Prov.Source.System, ObjectID: m.Held.Prov.Source.Object, EventKey: m.Held.Prov.Key})
	if err != nil {
		t.Fatalf("the replay dataset does not hold the held-out event: %v", err)
	}
	got, err := payloadhash.SHA256(ev.Payload)
	if err != nil || got != m.Held.Pin {
		t.Fatalf("replay payload digest %q (err %v) != the manifest's pin %q", got, err, m.Held.Pin)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("the replay dataset must hold exactly the held-out event, found %d files", len(entries))
	}
}

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
	replayDir := filepath.Join(dir, "replay-events")
	out.Reset()
	err = run([]string{"freeze-demo-manifest", "--data", abs(t, crmarenaSample), "--opportunity", top.OpportunityID,
		"--held-out", top.HeldOut.EventID, "--report", reportJSON, "--out", manifest, "--created-at", "2026-10-03T12:00:00Z", "--allow-throwaway",
		"--replay-events-out", replayDir}, &out)
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
	checkReplayDataset(t, replayDir, doc)
	if strings.Contains(string(doc), top.HeldOut.SourceObjectID) && strings.Count(string(doc), top.HeldOut.SourceObjectID) != 1 {
		t.Fatalf("Event N's source object appears more than once in the manifest (only as the bare held-out event)")
	}
}
