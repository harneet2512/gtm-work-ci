package codespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// played is the demo after the presenter pressed Play on both cases: the live state moved forward.
func (r *rig) played() {
	r.t.Helper()
	r.w.play(r.t, SlotCase1)
	r.w.play(r.t, SlotCase2)
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase2)
	r.w.modelCalls, r.w.pipelineRuns = 0, 0 // count only what happens after this point
	r.plat.events = nil
}

func TestResetAfterPlayLeavesTheStateEqualToTheBaseline(t *testing.T) {
	r := newRig(t)
	r.seeded()
	baseline := map[string]caseRows{SlotCase1: r.w.rows(t, SlotCase1), SlotCase2: r.w.rows(t, SlotCase2)}
	wantTree := fingerprintOf(t, r.w.live)
	r.played()
	if got := r.w.rows(t, SlotCase1); reflect.DeepEqual(got, baseline[SlotCase1]) {
		t.Fatal("the play must have moved the live state, or this test proves nothing")
	}
	if inv, _ := r.plat.Invisibility(context.Background(), "man-2"); inv.Status != "released" {
		t.Fatalf("EcoLite Event N must be released after its Play: %s", inv.Status)
	}

	if err := r.ops.ResetAll(context.Background(), nil); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}
	for _, slot := range []string{SlotCase1, SlotCase2} {
		got := r.w.rows(t, slot)
		if len(got.Events) != len(baseline[slot].Events) || !reflect.DeepEqual(got.Knowledge, baseline[slot].Knowledge) {
			t.Fatalf("%s rows after reset = %+v, want the baseline %+v (row counts and knowledge ids)", slot, got, baseline[slot])
		}
	}
	if inv, _ := r.plat.Invisibility(context.Background(), "man-2"); inv.Status != "withheld" {
		t.Fatalf("EcoLite's Event N must be invisible again, got %q", inv.Status)
	}
	if got := fingerprintOf(t, r.w.live); got != wantTree {
		t.Fatalf("the whole live copy must equal the baseline: %+v vs %+v", got, wantTree)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q, want case1", active)
	}
}

func TestResetStopsServicesRestoresThenRestartsAndChecksBothCasesCaseOneLast(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played()
	var steps []string
	if err := r.ops.ResetAll(context.Background(), func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	want := "stop-core,stop-stores,start-stores,stop-core,start-core:case2,stop-core,start-core:case1"
	if got := joined(r.plat.events); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	if steps[len(steps)-1] != "Reset complete" || !contains(steps, "Restoring the sealed baseline") {
		t.Fatalf("steps = %v", steps)
	}
}

// The product owner's rule: nothing is rebuilt or recomputed after the one-time setup.
func TestResetNeverRebuildsMigratesCarriesOrCallsAModel(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played()
	r.admin.calls = nil
	if err := r.ops.ResetAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if r.plat.rebuilds != 0 || r.carries != 0 || r.w.modelCalls != 0 || r.w.pipelineRuns != 0 {
		t.Fatalf("rebuilds=%d carries=%d model calls=%d pipeline runs=%d", r.plat.rebuilds, r.carries, r.w.modelCalls, r.w.pipelineRuns)
	}
	if len(r.admin.calls) != 0 {
		t.Fatalf("Reset must not create, drop or clone a database: %v", r.admin.calls)
	}
}

func TestResetFailsClearlyWithoutABaselineAndTouchesNothing(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played()
	_ = os.RemoveAll(r.ops.Base.(Baseline).Dir())
	err := r.ops.ResetAll(context.Background(), nil)
	if !errors.Is(err, ErrNoBaseline) || !strings.Contains(err.Error(), "one-time setup") {
		t.Fatalf("err = %v, want a message that tells the operator to run the one-time setup", err)
	}
	if len(r.plat.events) != 0 || r.plat.rebuilds != 0 {
		t.Fatalf("a refused reset must not stop or rebuild anything: %v", r.plat.events)
	}
	if !r.w.released(SlotCase1) {
		t.Fatal("the live state must be left as it was")
	}
}

func TestResetFailsClearlyOnACorruptBaseline(t *testing.T) {
	r := newRig(t)
	r.seeded()
	b := r.ops.Base.(Baseline)
	_ = os.WriteFile(filepath.Join(b.Dir(), "live", "pg", "data", "events-case1.json"), []byte("[1]"), 0o644)
	err := r.ops.ResetAll(context.Background(), nil)
	if !errors.Is(err, ErrNoBaseline) || !strings.Contains(err.Error(), "does not match") && !strings.Contains(err.Error(), "recorded") {
		t.Fatalf("err = %v", err)
	}
	if len(r.plat.events) != 0 || r.plat.rebuilds != 0 {
		t.Fatalf("a corrupt baseline must never fall back to a rebuild: %v", r.plat.events)
	}
}

func TestResetWithoutABaselineConfiguredIsAnError(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.ops.Base = nil
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "one-time setup") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheModelCallCacheSurvivesAReset(t *testing.T) {
	r := newRig(t)
	r.seeded()
	cache := filepath.Join(r.home, "cassettes")
	writeTree(t, r.home, map[string]string{"cassettes/a.json": "answer-a", "cassettes/cache-stats.json": "{}"})
	r.played()
	writeTree(t, r.home, map[string]string{"cassettes/b.json": "answer-from-the-play"})
	before := fingerprintOf(t, cache)
	for i := 0; i < 2; i++ {
		if err := r.ops.ResetAll(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := fingerprintOf(t, cache); got != before || got.Files != 3 {
		t.Fatalf("the cache must never be deleted or reset: %+v vs %+v", got, before)
	}
}

func TestResetIsRepeatable(t *testing.T) {
	r := newRig(t)
	r.seeded()
	want := fingerprintOf(t, r.w.live)
	for i := 0; i < 3; i++ {
		r.played()
		if err := r.ops.ResetAll(context.Background(), nil); err != nil {
			t.Fatalf("reset %d: %v", i+1, err)
		}
		if got := fingerprintOf(t, r.w.live); got != want {
			t.Fatalf("reset %d: live = %+v, want %+v", i+1, got, want)
		}
	}
}

func TestResetFailsWhenACaseIsNotWithheldAfterTheRestore(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.plat.invisible["man-2"] = "released"
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), `want withheld`) {
		t.Fatalf("err = %v", err)
	}
	r.plat.invisible["man-2"] = "leaked"
	if err := r.ops.ResetAll(context.Background(), nil); err == nil {
		t.Fatal("a leaked restore must fail")
	}
}

func TestResetStopsWhenTheStoresCannotBeStopped(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played()
	r.plat.failStop = errors.New("postgres will not stop")
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "postgres will not stop") {
		t.Fatalf("err = %v", err)
	}
	if contains(r.plat.events, "start-stores") || !r.w.released(SlotCase1) {
		t.Fatal("nothing may be replaced while a store still runs")
	}
}

func TestResetRefusesAnUnseededCase(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = os.Remove(r.ops.Paths.State(SlotCase2))
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "never seeded") {
		t.Fatalf("err = %v", err)
	}
	if len(r.plat.events) != 0 {
		t.Fatalf("nothing may be stopped: %v", r.plat.events)
	}
}

// The product owner: the baseline is each case's state after history events 1..N-1 went through the real pipeline once,
// Event N held out; restoring it never processes any of them again.
func TestTheRestoredBaselineHasReplayedEveryHistoryEventThroughNMinusOneAndNothingAfter(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played()
	if err := r.ops.ResetAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	m, err := r.ops.Base.(Baseline).Manifest()
	if err != nil || len(m.History) != 2 {
		t.Fatalf("manifest history = %+v, %v", m.History, err)
	}
	for _, h := range m.History {
		n := historyLen[h.Slot]
		if h.HistoryEvents != n || h.ThroughPosition != n || h.EventNPosition != n+1 {
			t.Fatalf("%s history = %+v, want events 1..%d and Event N at %d", h.Slot, h, n, n+1)
		}
		got := r.w.rows(t, h.Slot).Events
		for i, p := range got {
			if p != i+1 {
				t.Fatalf("%s processed events = %v, want exactly 1..%d", h.Slot, got, n)
			}
		}
		if len(got) != n || got[len(got)-1] >= h.EventNPosition {
			t.Fatalf("%s processed %v: nothing at or after Event N (%d) may be in the restored state", h.Slot, got, h.EventNPosition)
		}
		if r.w.released(h.Slot) {
			t.Fatalf("%s: Event N was released in the restored state", h.Slot)
		}
	}
	if m.History[0].Slot != SlotCase1 || m.History[1].Slot != SlotCase2 || m.History[0].HistoryEvents == m.History[1].HistoryEvents {
		t.Fatalf("both MedTech and EcoLite must carry their own history: %+v", m.History)
	}
	if r.w.pipelineRuns != 0 || r.w.modelCalls != 0 || r.plat.rebuilds != 0 {
		t.Fatal("restoring must not re-process any event")
	}
}

func TestVerifyRejectsABaselineSealedAfterEventNWasReleased(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.w.play(t, SlotCase2) // the state about to be sealed already moved past N-1
	st, _, _ := r.ops.SeedState(SlotCase2)
	st.PlayedAt = time.Now()
	if err := demorun.SaveState(r.ops.Paths.State(SlotCase2), st); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ops.Base.(Baseline).Seal(context.Background()); err == nil || !strings.Contains(err.Error(), "already released Event N") {
		t.Fatalf("err = %v, want a refusal to seal past N-1", err)
	}
}

func TestResumeKeepsTheLiveStateAndStartsCoreOnTheActiveCase(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.played() // the live copy sits on EcoLite, after its Play
	want := fingerprintOf(t, r.w.live)
	if err := r.ops.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := fingerprintOf(t, r.w.live); got != want {
		t.Fatalf("resume must not touch the live state: %+v vs %+v", got, want)
	}
	if got := joined(r.plat.events); got != "stop-core,start-core:case2" {
		t.Fatalf("events = %s: resume only restarts core on the case that was active", got)
	}
	if r.plat.rebuilds != 0 || r.carries != 0 {
		t.Fatal("resume rebuilds and carries nothing")
	}
}

func TestResumeStartsOnCaseOneWhenNothingWasActiveAndNeedsABaseline(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = os.Remove(r.ops.Paths.ActiveFile())
	if err := r.ops.Resume(context.Background()); err != nil || !contains(r.plat.events, "start-core:case1") {
		t.Fatalf("err = %v events = %v", err, r.plat.events)
	}
	_ = os.RemoveAll(r.ops.Base.(Baseline).Dir())
	r.plat.events = nil
	if err := r.ops.Resume(context.Background()); !errors.Is(err, ErrNoBaseline) || len(r.plat.events) != 0 {
		t.Fatalf("a missing baseline fails resume too: %v %v", err, r.plat.events)
	}
}

func TestActivateStopsCoreAndStartsItOnTheCaseWithoutRebuildingTheGraph(t *testing.T) {
	r := newRig(t)
	r.seeded()
	status, err := r.ops.Activate(context.Background(), SlotCase2)
	if err != nil || status != "withheld" {
		t.Fatalf("Activate = %q, %v", status, err)
	}
	if got := joined(r.plat.events); got != "stop-core,start-core:case2" {
		t.Fatalf("events = %s", got)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase2 {
		t.Fatalf("active = %q", active)
	}
	if r.carries != 1 {
		t.Fatalf("activating a later case carries what the earlier one learned, carries = %d", r.carries)
	}
}

func TestActivateNeverRebuildsAMissingGraphAndPointsToSetup(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = os.Remove(r.ops.Paths.GraphMarker(SlotCase2))
	_, err := r.ops.Activate(context.Background(), SlotCase2)
	if err == nil || !strings.Contains(err.Error(), "one-time setup") {
		t.Fatalf("err = %v", err)
	}
	if r.plat.rebuilds != 0 || contains(r.plat.events, "start-core:case2") {
		t.Fatalf("the demo path never rebuilds: %v", r.plat.events)
	}
}

func TestActivateRefusesAnUnseededCaseAndAnUnknownOne(t *testing.T) {
	r := newRig(t)
	if _, err := r.ops.Activate(context.Background(), SlotCase1); err == nil || !strings.Contains(err.Error(), "not seeded") {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.ops.Activate(context.Background(), "case9"); err == nil {
		t.Fatal("unknown slot accepted")
	}
	if len(r.plat.events) != 0 {
		t.Fatalf("nothing may be touched: %v", r.plat.events)
	}
}

func TestActivateRefusesALeakedWorldAndDoesNotRecordItAsActive(t *testing.T) {
	r := newRig(t)
	r.seeded()
	_ = os.Remove(r.ops.Paths.ActiveFile())
	r.plat.invisible["man-1"] = "leaked"
	_, err := r.ops.Activate(context.Background(), SlotCase1)
	if err == nil || !strings.Contains(err.Error(), "Event N is visible before Play") {
		t.Fatalf("err = %v", err)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != "" {
		t.Fatalf("a leaked case must not become active: %q", active)
	}
	r.plat.invisible["man-1"], r.plat.failStart = "withheld", errors.New("core will not start")
	if _, err := r.ops.Activate(context.Background(), SlotCase1); err == nil || !strings.Contains(err.Error(), "core will not start") {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedStateIgnoresAnIncompleteStateFile(t *testing.T) {
	r := newRig(t)
	if err := demorun.SaveState(r.ops.Paths.State(SlotCase1), demorun.DemoState{CaseName: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.ops.SeedState(SlotCase1); ok || err != nil {
		t.Fatalf("a state without a manifest id is not a seed: ok=%v err=%v", ok, err)
	}
	if err := os.MkdirAll(r.ops.Paths.CaseDir(SlotCase2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.ops.Paths.State(SlotCase2), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ops.SeedState(SlotCase2); err == nil {
		t.Fatal("a corrupt state file must be an error")
	}
}
