package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// WP32 (HAR-131) precondition 3: --synthetic-covered wires SupersedeSnapshotStage / RealTerminalDeals into
// import-crmarena (--until and --full-timeline) and replay-crmarena.

// coverableDeal is a sample deal with a snapshot StageName event and no visible real outcome.
func coverableDeal(t *testing.T) (string, string) {
	t.Helper()
	dir := crmarenaSample
	snap, err := crmarena.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	real := crmarena.RealTerminalDeals(res.Events)
	for _, e := range res.Events {
		if e.DealID != "" && real[e.DealID] == "" && strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") && e.Source.SourceSystem == "crm" {
			return dir, e.DealID
		}
	}
	t.Fatal("sample has no coverable deal")
	return "", ""
}

func writeCovered(t *testing.T, deals ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "covered_deals.json")
	raw, _ := json.Marshal(coveredFile{Provenance: "synthetic:v1", Deals: deals})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func stageKeys(t *testing.T, raw []byte, deal string) int {
	t.Helper()
	var events []normalize.SourceEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	n := 0
	for _, e := range events {
		if e.SourceSystem == "crm" && e.SourceObjectID == "opp:"+deal && strings.HasPrefix(e.SourceEventKey, "field:StageName:") {
			n++
		}
	}
	return n
}

func TestReplaySupersedesTheSnapshotStageOfCoveredDealsOnly(t *testing.T) {
	dir, deal := coverableDeal(t)
	plain, covered := filepath.Join(t.TempDir(), "plain.json"), filepath.Join(t.TempDir(), "covered.json")
	var out bytes.Buffer
	if err := run([]string{"replay-crmarena", "--from", "2000-01-01T00:00:00Z", "--skip-store-check", "--out", plain, dir}, &out); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"replay-crmarena", "--from", "2000-01-01T00:00:00Z", "--skip-store-check", "--out", covered,
		"--synthetic-covered", writeCovered(t, deal), dir}, &out); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(plain)
	b, _ := os.ReadFile(covered)
	if stageKeys(t, a, deal) == 0 || stageKeys(t, b, deal) != 0 {
		t.Fatalf("deal %s: %d stage events without the layer, %d with it (want >0, 0)", deal, stageKeys(t, a, deal), stageKeys(t, b, deal))
	}
	var na, nb []json.RawMessage
	_ = json.Unmarshal(a, &na)
	_ = json.Unmarshal(b, &nb)
	if len(na)-len(nb) != stageKeys(t, a, deal) {
		t.Fatalf("only the covered deal's snapshot stage may be dropped: %d -> %d events", len(na), len(nb))
	}
}

func TestImportSupersedesUnderBothTimelines(t *testing.T) {
	dir, deal := coverableDeal(t)
	path := writeCovered(t, deal)
	for _, timeline := range [][]string{{"--full-timeline"}, {"--until", "2030-01-01T00:00:00Z"}} {
		var out bytes.Buffer
		args := append([]string{"import-crmarena", "--dry-run", "--synthetic-covered", path}, timeline...)
		if err := run(append(args, dir), &out); err != nil {
			t.Fatalf("%v: %v", timeline, err)
		}
		if !strings.Contains(out.String(), "synthetic layer covers 1 deals: 1 snapshot stage events superseded") {
			t.Fatalf("%v: supersede not applied:\n%s", timeline, out.String())
		}
	}
}

func TestCoveringADealWithAVisibleRealOutcomeIsRefused(t *testing.T) {
	dir := crmarenaSample
	snap, err := crmarena.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	for deal := range crmarena.RealTerminalDeals(res.Events) {
		var out bytes.Buffer
		err := run([]string{"import-crmarena", "--dry-run", "--full-timeline", "--synthetic-covered", writeCovered(t, deal), dir}, &out)
		if err == nil || !strings.Contains(err.Error(), "visible real outcome") {
			t.Fatalf("covering real-terminal deal %s must fail, got %v", deal, err)
		}
		return
	}
	t.Skip("the sample has no deal with a visible real outcome")
}

func TestSyntheticCoveredFileMustNameItsDeals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	_ = os.WriteFile(path, []byte(`{"provenance":"synthetic:v1","deals":[]}`), 0o644)
	if _, err := readCovered(path); err == nil {
		t.Fatal("an empty covered list must be refused")
	}
}
