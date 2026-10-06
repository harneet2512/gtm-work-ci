package play

import (
	"errors"
	"testing"
)

func TestResetMovesTheCursorBackDeterministically(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 3)

	doc, err := r.svc.Reset(bg, r.manifest, 1)
	if err != nil {
		t.Fatalf("Reset(1): %v", err)
	}
	validateDef(t, "resetResult", doc)
	m := resultField(t, doc)
	if m["episode"] != 1.0 || m["digest"] == nil {
		t.Fatalf("reset result: %+v", m)
	}
	released := m["released_events"].([]any)
	if len(released) != 1 || released[0] != "0e7e0000-0000-4000-8000-0000000000a1" {
		t.Fatalf("released_events = %v", released)
	}
	// the cursor moved back: the view is the world at episode 1, event 2 next again
	view, err := r.svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := resultField(t, view)
	if v["episode"] != 1.0 || len(v["prior_episodes"].([]any)) != 1 {
		t.Fatalf("view after reset: %+v", v)
	}
	// a second reset to the same episode produces the same digest over the same events
	again, err := r.svc.Reset(bg, r.manifest, 1)
	if err != nil {
		t.Fatal(err)
	}
	b := resultField(t, again)
	if b["digest"] != m["digest"] {
		t.Fatalf("reset digests differ: %v != %v", m["digest"], b["digest"])
	}
}

func TestResetThenAdvanceReusesTheExistingBookkeeping(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 1)
	doc, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	before := resultField(t, doc)
	if _, err := r.svc.Reset(bg, r.manifest, 1); err != nil {
		t.Fatal(err)
	}
	ingested := r.ingest.count()
	doc2, err := r.svc.NextEpisode(bg, r.manifest)
	if err != nil {
		t.Fatalf("advance after reset: %v", err)
	}
	after := resultField(t, doc2)
	if r.ingest.count() != ingested {
		t.Fatal("re-advancing a released episode ingests nothing")
	}
	for _, k := range []string{"episode", "material", "no_action_reason", "state_version", "state_digest"} {
		if after[k] != before[k] {
			t.Fatalf("%s differs across the reset: %v != %v", k, before[k], after[k])
		}
	}
	if got := r.count("demo_episodes"); got != "2" {
		t.Fatalf("demo_episodes = %s (the bookkeeping was reused, not rewritten)", got)
	}
}

func TestResetRefusesWhatWasNeverReleased(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 1)
	for _, k := range []int{-1, 2, 3, 4} {
		if _, err := r.svc.Reset(bg, r.manifest, k); !errors.Is(err, ErrInvalidEpisode) {
			t.Fatalf("Reset(%d): %v", k, err)
		}
	}
}

func TestResetToTheBeginningShowsOnlyTheFirstEventNext(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 2)
	doc, err := r.svc.Reset(bg, r.manifest, 0)
	if err != nil {
		t.Fatalf("Reset(0): %v", err)
	}
	validateDef(t, "resetResult", doc)
	if len(resultField(t, doc)["released_events"].([]any)) != 0 {
		t.Fatalf("reset to 0: %+v", resultField(t, doc))
	}
	view, err := r.svc.Episodes(bg, r.manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := resultField(t, view)
	if v["episode"] != 0.0 || v["state"] != nil {
		t.Fatalf("view at the reset beginning: %+v", v)
	}
	next := v["next_event"].(map[string]any)
	if next["position"] != 1.0 || next["released"] != false {
		t.Fatalf("after reset(0) the first event is next again: %+v", next)
	}
}
