package crmarena

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func frozenSplit(t *testing.T) Split {
	t.Helper()
	raw, err := os.ReadFile(repoPath(t, "bench/data/deal_split.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s Split
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestFrozenSplitIsConsistent runs everywhere (the split is committed; the snapshot is not).
func TestFrozenSplitIsConsistent(t *testing.T) {
	s := frozenSplit(t)
	if _, err := s.CutoffTime(); err != nil {
		t.Fatal(err)
	}
	if s.Counts.Previous != len(s.Previous) || s.Counts.Current != len(s.Current) || s.Counts.Ambiguous != len(s.Ambiguous) {
		t.Errorf("counts %+v disagree with lists (%d previous, %d ambiguous, %d current)", s.Counts, len(s.Previous), len(s.Ambiguous), len(s.Current))
	}
	if s.PreviousMinQuietDays < 1 || len(s.PreviousDeals) != len(s.Previous) || len(s.AmbiguousDeals) != len(s.Ambiguous) {
		t.Errorf("split lacks its quiet-period manifest (%d days) or per-deal quiet days", s.PreviousMinQuietDays)
	}
	for i, d := range s.PreviousDeals {
		if d.DealID != s.Previous[i] || d.DaysQuiet < s.PreviousMinQuietDays {
			t.Fatalf("previous deal %+v is not %s with at least %d quiet days", d, s.Previous[i], s.PreviousMinQuietDays)
		}
	}
	for i, d := range s.AmbiguousDeals {
		if d.DealID != s.Ambiguous[i] || d.DaysQuiet >= s.PreviousMinQuietDays || d.DaysQuiet < 0 {
			t.Fatalf("ambiguous deal %+v does not fall inside the %d-day window", d, s.PreviousMinQuietDays)
		}
	}
	previous := s.PreviousSet()
	for _, id := range s.Ambiguous {
		if previous[id] || s.CurrentSet()[id] {
			t.Errorf("ambiguous deal %s is also previous or current", id)
		}
	}
	if !sort.StringsAreSorted(s.Previous) || !sort.StringsAreSorted(s.Current) {
		t.Error("deal lists are not sorted")
	}
	current := s.CurrentSet()
	for _, id := range s.Previous {
		if current[id] {
			t.Errorf("deal %s is both previous and current", id)
		}
	}
	if s.Rule == "" || !strings.Contains(s.Source.Licence, "CC BY-NC 4.0") || s.Source.Manifest == "" {
		t.Errorf("split provenance incomplete: rule %q source %+v", s.Rule, s.Source)
	}
	best, err := ChooseCutoff(s.Candidates)
	if err != nil || best.Cutoff != s.Cutoff {
		t.Errorf("frozen cutoff %s is not the rule's choice %s (%v)", s.Cutoff, best.Cutoff, err)
	}
}

// fullSnapshot loads the git-ignored export when it is present and is the one the split was frozen on.
func fullSnapshot(t *testing.T, s Split) Snapshot {
	t.Helper()
	dir := repoPath(t, "data/crmarena_b2b")
	raw, err := os.ReadFile(dir + "/manifest.json")
	if err != nil {
		t.Skip("data/crmarena_b2b is not exported here (git-ignored); run bench/data/crmarena_export.py")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != s.Source.Manifest {
		t.Fatalf("local snapshot manifest differs from the one the split was frozen on")
	}
	snap, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestFrozenSplitReproducesFromTheSnapshot(t *testing.T) {
	frozen := frozenSplit(t)
	snap := fullSnapshot(t, frozen)
	w, err := Windows(snap)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewSplitWith(w, SplitConfig{Seed: frozen.Seed, PreviousMinQuietDays: frozen.PreviousMinQuietDays})
	if err != nil {
		t.Fatal(err)
	}
	again.Source = frozen.Source
	if !reflect.DeepEqual(again, frozen) {
		t.Fatalf("recomputed split differs from bench/data/deal_split.json (cutoff %s vs %s)", again.Cutoff, frozen.Cutoff)
	}
}

// TestFullSnapshotHasNoLeakage applies the leakage guards to every deal of the real snapshot.
func TestFullSnapshotHasNoLeakage(t *testing.T) {
	frozen := frozenSplit(t)
	snap := fullSnapshot(t, frozen)
	r, err := Build(snap)
	if err != nil {
		t.Fatal(err)
	}
	cutoff, _ := frozen.CutoffTime()
	know, err := KnowledgeInputs(r.Events, frozen)
	if err != nil {
		t.Fatal(err)
	}
	previous := frozen.PreviousSet()
	for _, e := range know {
		if !e.OccurredAt().Before(cutoff) || !previous[e.DealID] {
			t.Fatalf("%s/%s (deal %s, %s) leaked into knowledge", e.Source.SourceObjectID, e.Source.SourceEventKey, e.DealID, e.OccurredAt())
		}
	}
	for _, id := range frozen.Current {
		events, err := Replay(r.Events, frozen, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(events); i++ {
			if events[i].OccurredAt().Before(events[i-1].OccurredAt()) {
				t.Fatalf("replay of %s is out of date order", id)
			}
		}
	}
	withheld := 0
	for _, e := range r.Events {
		if previous[e.DealID] && !e.OccurredAt().Before(cutoff) {
			withheld++
		}
	}
	t.Logf("knowledge inputs: %d events of %d previous deals (%d of their events dated at or after the cutoff withheld); "+
		"%d current deals replayable in order", len(know), len(frozen.Previous), withheld, len(frozen.Current))
}

// TestFullSnapshotKnowledgeCarriesNoLateDateAndOrderingHolds: the generic guards on all 1,170 deals.
func TestFullSnapshotKnowledgeCarriesNoLateDateAndOrderingHolds(t *testing.T) {
	frozen := frozenSplit(t)
	r, err := Build(fullSnapshot(t, frozen))
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotValuesComeLast(t, r.Events)
	if leaks := outcomeLeaks(t, r.Events); len(leaks) > 0 {
		t.Errorf("%d outcome leaks on the full snapshot, first: %s", len(leaks), leaks[0])
	}
	if got := unclassifiedFields(t, r.Events); len(got) > 0 {
		t.Errorf("fields with no classification on the full snapshot: %v", got)
	}
	know, red, err := KnowledgeInputsWithReport(r.Events, frozen)
	if err != nil {
		t.Fatal(err)
	}
	cutoff, _ := frozen.CutoffTime()
	assertNoDateAtOrAfter(t, know, cutoff)
	t.Logf("knowledge inputs: %d events; redacted %d post-cutoff date fields in %d events: %v", len(know), red.Fields, red.Events, red.ByField)
}
