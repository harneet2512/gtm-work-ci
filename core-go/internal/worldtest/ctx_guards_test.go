package worldtest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// mint issues a run token for an existing run.
func (s *stack) mint(runID string) string {
	s.t.Helper()
	tok, err := s.signer.Issue(runID, time.Now())
	if err != nil {
		s.t.Fatal(err)
	}
	return tok
}

func pulls(t *testing.T, runID string) string {
	t.Helper()
	var n string
	if err := env.DB.QueryRow(`SELECT count(*)::text FROM context_access_log WHERE agent_run_id = $1::uuid`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// M1: another activity of the account at the very instant of the run's trigger is inside the cutoff
// (trigger + 1 microsecond). World time cannot say whether the run could see it, so the pull fails closed:
// 409, nothing served, nothing logged. A run on another trigger is not affected.
func TestRunContextFailsClosedWhenAnActivityTiesWithTheTrigger(t *testing.T) {
	s := newStack(t)
	w := s.world
	run := w.NewRun(t, 3)
	insertBulk(t, w, "tie", 1, `'`+w.At(3).Format(time.RFC3339Nano)+`'::timestamptz`)
	token := s.mint(run)
	for _, c := range ctxTools {
		r := s.pull(token, c.tool, c.query)
		if r.status != http.StatusConflict || errorCode(t, r) != "world_time_ambiguous" {
			t.Fatalf("%s: status %d code %q, want 409 world_time_ambiguous: %s", c.tool, r.status, errorCode(t, r), r.body)
		}
		if strings.Contains(string(r.body), "tie") && strings.Contains(string(r.body), "source_object_id") {
			t.Errorf("the refusal carries data: %s", r.body)
		}
	}
	if n := pulls(t, run); n != "0" {
		t.Errorf("%s refused pulls were logged", n)
	}
	if r := s.pull(s.runToken(4), "state", "limit=5"); r.status != http.StatusOK {
		t.Fatalf("a run on another trigger must read normally: %d %s", r.status, r.body)
	}
}

// M2, burst: the run's recorded state version folds an activity newer than its trigger (a coalesced burst
// whose version has the burst's newest as_of). Serving it would show the run its own future, and falling back
// to the version before the burst would silently drop its trigger: the pull fails closed instead.
func TestRunContextRefusesAStateVersionThatFoldsLaterActivities(t *testing.T) {
	s := newStack(t)
	w := s.world
	run := w.NewRun(t, 3)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET state_version = $2 WHERE id = $1::uuid`, run, w.Event(4).Version); err != nil {
		t.Fatal(err)
	}
	token := s.mint(run)
	for _, tool := range []string{"state", "people", "commitments"} {
		r := s.pull(token, tool, "limit=5")
		if r.status != http.StatusConflict || errorCode(t, r) != "run_state_after_cutoff" {
			t.Fatalf("%s: status %d code %q, want 409 run_state_after_cutoff: %s", tool, r.status, errorCode(t, r), r.body)
		}
		assertNoneOf(t, tool, string(r.body), markersFrom(4)...)
	}
	if r := s.pull(token, "activities", "limit=5"); r.status != http.StatusOK {
		t.Fatalf("tools that read no state are unaffected: %d", r.status)
	}
}

// M2, same as_of: a later version that shares the as_of of the run's own version must not win. Before the fix
// the version came from the cutoff (highest version with as_of < cutoff, ties to the higher one) and a
// recomputed duplicate hid the version the run was built on.
func TestRunContextReadsExactlyTheVersionTheRunWasBuiltOn(t *testing.T) {
	s := newStack(t)
	w := s.world
	run := w.NewRun(t, 3) // pinned to Event(3).Version
	const leaked = "LATER_VERSION_SAME_AS_OF"
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
 SELECT account_id, 1000, as_of, jsonb_set(state, '{fields,stage,value}', '"`+leaked+`"') FROM state_history WHERE account_id = $1::uuid AND version = $2`,
		w.Account, w.Event(3).Version); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := storetest.Purge(context.Background(), env.DB, `DELETE FROM state_history WHERE version = 1000`); err != nil {
			t.Errorf("clean up: %v", err)
		}
	})
	raw, err := reader(t).StateWorldAsOf(context.Background(), w.Account, justAfter(w.At(3)))
	if err != nil || !strings.Contains(string(raw), leaked) {
		t.Fatalf("setup: the cutoff rule must prefer the duplicate (that is the defect): %v %.200s", err, raw)
	}
	r := s.pull(s.mint(run), "state", "field_path=stage")
	if r.status != http.StatusOK || strings.Contains(string(r.body), leaked) || !strings.Contains(string(r.body), worldfixtureStage(3)) {
		t.Fatalf("the run must read its own version: %d %s", r.status, r.body)
	}
}
