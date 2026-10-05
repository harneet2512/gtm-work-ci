package codespace

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type carryCall struct{ from, to string }

func handoffRig(t *testing.T) (*rig, *[]carryCall) {
	t.Helper()
	r := newRig(t)
	r.seeded()
	if err := WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1); err != nil {
		t.Fatal(err)
	}
	// the demo was reset, so both cases' own graphs (one Neo4j each) are already built
	for _, slot := range []string{SlotCase1, SlotCase2} {
		if err := WriteMarker(r.ops.Paths.GraphMarker(slot), slot); err != nil {
			t.Fatal(err)
		}
	}
	var calls []carryCall
	r.ops.Carry = func(_ context.Context, from, to Case) (int, error) {
		calls = append(calls, carryCall{from.Slot, to.Slot})
		return 2, nil
	}
	return r, &calls
}

func TestHandoffMovesTheDemoToTheNextCaseAfterEventNWasPlayed(t *testing.T) {
	r, carries := handoffRig(t)
	r.plat.invisible["man-1"] = "released"

	got, err := r.ops.Handoff(context.Background(), "man-1")
	if err != nil {
		t.Fatalf("Handoff: %v", err)
	}
	if got.Slot != SlotCase2 || got.ManifestID != "man-2" || got.AccountID != "acct-2" || got.Label != "EcoLite Innovations" {
		t.Fatalf("handoff = %+v", got)
	}
	if len(*carries) != 1 || (*carries)[0] != (carryCall{SlotCase1, SlotCase2}) {
		t.Fatalf("what case 1 learned must be carried into case 2: %v", *carries)
	}
	if want := "stop-core,start-core:case2"; strings.Join(r.plat.events, ",") != want {
		t.Fatalf("events = %v, want %s: the later case's graph was built at reset, so the handoff only carries and restarts core", r.plat.events, want)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase2 {
		t.Fatalf("active = %q", active)
	}
}

func TestHandoffIsIdempotentOnceTheNextCaseIsActive(t *testing.T) {
	r, carries := handoffRig(t)
	r.plat.invisible["man-1"] = "released"
	if _, err := r.ops.Handoff(context.Background(), "man-1"); err != nil {
		t.Fatal(err)
	}
	r.plat.events, *carries = nil, nil
	got, err := r.ops.Handoff(context.Background(), "man-1") // a retried Play after a lost response
	if err != nil || got.Slot != SlotCase2 {
		t.Fatalf("second handoff = %+v, %v", got, err)
	}
	if len(r.plat.events) != 0 || len(*carries) != 0 {
		t.Fatalf("an active next case must not be restarted or re-carried: %v %v", r.plat.events, *carries)
	}
}

func TestHandoffRefusesUntilEventNWasPlayed(t *testing.T) {
	r, _ := handoffRig(t) // man-1 is withheld
	_, err := r.ops.Handoff(context.Background(), "man-1")
	if !errors.Is(err, ErrNotPlayed) {
		t.Fatalf("err = %v, want ErrNotPlayed", err)
	}
	if len(r.plat.events) != 0 {
		t.Fatalf("a refused handoff must not touch core: %v", r.plat.events)
	}
}

func TestHandoffHasNowhereToGoFromTheLastCaseOrAnUnknownManifest(t *testing.T) {
	r, _ := handoffRig(t)
	if _, err := r.ops.Handoff(context.Background(), "man-2"); !errors.Is(err, ErrNoNextCase) {
		t.Fatalf("last case: %v", err)
	}
	if _, err := r.ops.Handoff(context.Background(), "man-404"); !errors.Is(err, ErrUnknownManifest) {
		t.Fatalf("unknown manifest: %v", err)
	}
}

func TestHandoffReportsAFailedCarryOrActivation(t *testing.T) {
	r, _ := handoffRig(t)
	r.plat.invisible["man-1"] = "released"
	r.ops.Carry = func(context.Context, Case, Case) (int, error) { return 0, errors.New("carry broke") }
	if _, err := r.ops.Handoff(context.Background(), "man-1"); err == nil || !strings.Contains(err.Error(), "carry broke") {
		t.Fatalf("err = %v", err)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active == SlotCase2 {
		t.Fatal("a failed handoff must not record case 2 as active")
	}
}

func TestHandoffRebuildsOnlyWhenTheLaterGraphIsMissing(t *testing.T) {
	r, _ := handoffRig(t)
	r.plat.invisible["man-1"] = "released"
	_ = os.Remove(r.ops.Paths.GraphMarker(SlotCase2)) // for example a reset that stopped half way
	if _, err := r.ops.Handoff(context.Background(), "man-1"); err != nil {
		t.Fatal(err)
	}
	if want := "stop-core,rebuild:case2,start-core:case2"; strings.Join(r.plat.events, ",") != want {
		t.Fatalf("events = %v, want %s", r.plat.events, want)
	}
	if g, _ := ReadMarker(r.ops.Paths.GraphMarker(SlotCase1)); g != SlotCase1 {
		t.Fatal("activating one case leaves the other case's graph alone")
	}
}

func TestResetRemovesEveryGraphMarkerSoEachGraphIsRebuiltFromTheRestoredWorld(t *testing.T) {
	r := newRig(t)
	r.seeded()
	for _, slot := range []string{SlotCase1, SlotCase2} {
		_ = WriteMarker(r.ops.Paths.GraphMarker(slot), slot)
	}
	if err := r.ops.ResetAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.plat.events, ","); got != "stop-core,stop-core,rebuild:case2,start-core:case2,stop-core,rebuild:case1,start-core:case1" {
		t.Fatalf("both graphs must be rebuilt after a restore: %s", got)
	}
	for _, slot := range []string{SlotCase1, SlotCase2} {
		if g, _ := ReadMarker(r.ops.Paths.GraphMarker(slot)); g != slot {
			t.Fatalf("after the reset each case's graph is current: %s = %q", slot, g)
		}
	}
}
