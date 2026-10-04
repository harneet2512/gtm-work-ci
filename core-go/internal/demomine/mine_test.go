package demomine

import (
	"bytes"
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const sampleDir = "../../../fixtures/crmarena_sample"

var (
	cacheOnce sync.Once
	cacheRep  Report
	cacheErr  error
)

// cachedSample is the mined sample, computed once per test binary (a run takes tens of seconds).
func cachedSample(t *testing.T) Report {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	cacheOnce.Do(func() { cacheRep, cacheErr = runMine() })
	if cacheErr != nil {
		t.Fatalf("mine: %v", cacheErr)
	}
	return cacheRep
}

// mineSample runs the miner on the committed sample in a freshly reset database.
func mineSample(t *testing.T) Report {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test")
	}
	rep, err := runMine()
	if err != nil {
		t.Fatalf("mine: %v", err)
	}
	return rep
}

func runMine() (Report, error) {
	env, err := storetest.Start(context.Background())
	if err != nil {
		return Report{}, err
	}
	defer env.Close()
	cfg, err := LoadConfig(filepath.FromSlash(configPath))
	if err != nil {
		return Report{}, err
	}
	return Mine(context.Background(), env.DB, MineOptions{Dir: filepath.FromSlash(sampleDir), Config: cfg,
		SplitLabel: "every opportunity of the sample", Date: "2026-10-03"})
}

func TestMineSampleIsByteIdenticalAcrossRuns(t *testing.T) {
	a, b := cachedSample(t), mineSample(t)
	ja, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	jb, _ := b.JSON()
	if !bytes.Equal(ja, jb) {
		t.Fatal("two runs on the same input rendered different JSON")
	}
	if !bytes.Equal(a.Markdown(), b.Markdown()) {
		t.Fatal("two runs on the same input rendered different Markdown")
	}
	t.Logf("scanned %d opportunities, %d events, %d material, %d cases", a.Counts.OpportunitiesScanned, a.Counts.ScannedEvents,
		a.Counts.MaterialEvents, a.Counts.OpportunitiesWithCase)
	if a.Counts.OpportunitiesScanned == 0 || a.Counts.MaterialEvents == 0 || len(a.Top) == 0 {
		t.Fatalf("the sample must yield scanned opportunities, material events and at least one case: %+v", a.Counts)
	}
}

// Chronology: a case's events are the opportunity's events in the snapshot's own order, never reordered.
func TestMinedSequencesKeepTheRealChronology(t *testing.T) {
	rep := cachedSample(t)
	snap, err := crmarena.Load(filepath.FromSlash(sampleDir))
	if err != nil {
		t.Fatal(err)
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{}
	for _, e := range res.Events {
		if d := e.ReplayDeal(); d != "" {
			want[d] = append(want[d], e.Source.SourceSystem+"/"+e.Source.SourceObjectID+"/"+e.Source.SourceEventKey)
		}
	}
	if len(rep.Top) == 0 {
		t.Fatalf("no cases: %+v", rep.Counts)
	}
	for _, c := range rep.Top {
		got := append(append([]EventRecord{}, c.History...), c.HeldOut)
		for i, e := range got {
			if e.Position != i+1 {
				t.Errorf("%s: position %d at index %d", c.OpportunityID, e.Position, i)
			}
			if id := e.SourceSystem + "/" + e.SourceObjectID + "/" + e.SourceEventKey; id != want[c.OpportunityID][i] {
				t.Errorf("%s: event %d is %s, the snapshot order has %s", c.OpportunityID, i+1, id, want[c.OpportunityID][i])
			}
			if i > 0 && e.OccurredAt < got[i-1].OccurredAt {
				t.Errorf("%s: event %d (%s) is dated before event %d (%s)", c.OpportunityID, i+1, e.OccurredAt, i, got[i-1].OccurredAt)
			}
			if e.Layer != LayerBase || e.Provenance != ProvenanceCRMB2B {
				t.Errorf("%s: event %d provenance %s/%s, want base", c.OpportunityID, i+1, e.Layer, e.Provenance)
			}
		}
	}
}
