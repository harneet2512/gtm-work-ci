package play

import (
	"strings"
	"testing"
)

// TestTheViewAtEveryEpisodeWithholdsTheFuture is the §G no-leak invariant checked at every episode k:
// prior episodes name only events ≤ k, the next event k+1 is named but withheld (no materiality, state,
// change or decision), events k+2..N are not named at all, and nothing the held-out event caused — the SOC2
// blocker — appears in the world the view shows until it is released.
func TestTheViewAtEveryEpisodeWithholdsTheFuture(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 3)
	eventID := map[int]string{
		1: "0e7e0000-0000-4000-8000-0000000000a1",
		2: "0e7e0000-0000-4000-8000-0000000000a2",
		3: r.held.EventID,
	}
	for k := 0; k <= 3; k++ {
		doc, err := r.svc.Episodes(bg, r.manifest, &k)
		if err != nil {
			t.Fatalf("Episodes(at=%d): %v", k, err)
		}
		v := resultField(t, doc)
		if v["episode"] != float64(k) {
			t.Fatalf("episode = %v, want %d", v["episode"], k)
		}
		// every event strictly after the next one stays unnamed
		for pos := k + 2; pos <= 3; pos++ {
			if strings.Contains(string(doc), eventID[pos]) {
				t.Fatalf("view at %d names event %d (%s):\n%s", k, pos, eventID[pos], doc)
			}
		}
		prior := v["prior_episodes"].([]any)
		if len(prior) != k {
			t.Fatalf("prior_episodes at %d = %d", k, len(prior))
		}
		for i, p := range prior {
			m := p.(map[string]any)
			if m["position"] != float64(i+1) || m["released"] != true {
				t.Fatalf("prior episode at %d: %+v", k, m)
			}
		}
		next, _ := v["next_event"].(map[string]any)
		if k == 3 {
			if next != nil {
				t.Fatalf("a completed replay shows no next event: %+v", next)
			}
		} else {
			if next == nil || next["position"] != float64(k+1) || next["released"] != false {
				t.Fatalf("next event at %d: %+v", k, next)
			}
			// named but withheld: no materiality, state, change, decision or diff
			for _, f := range []string{"material", "no_action_reason", "state_version", "account_change_id",
				"decision_episode_id", "graph_diff_id"} {
				if next[f] != nil {
					t.Fatalf("the withheld event at %d carries %s = %v", k, f, next[f])
				}
			}
		}
		// the state the view shows is the one episode k ended in — never a later version
		st, _ := v["state"].(map[string]any)
		if k == 0 {
			if st != nil {
				t.Fatalf("state before any release: %+v", st)
			}
		} else if st == nil || st["version"] != float64(k) {
			t.Fatalf("state version at %d: %+v", k, st)
		}
		// the held-out email's blocker never appears before it is released
		if k < 3 && strings.Contains(string(doc), "SOC2") {
			t.Fatalf("view at %d leaks what the held-out event caused:\n%s", k, doc)
		}
		if k == 3 && !strings.Contains(string(doc), "SOC2") {
			t.Fatalf("the released world's state lacks the SOC2 blocker:\n%s", doc)
		}
	}
}

// TestAnInjectedFutureRowDoesNotEnterTheView double-checks the world-timed read against rows a later world
// wrote: an activity far in the future of an episode's bound — one the released events never caused —
// cannot appear in that episode's state.
func TestAnInjectedFutureRowDoesNotEnterTheView(t *testing.T) {
	r := newEpisodeRig(t)
	advanceEpisodes(t, r, 1)
	// a stray state version as_of a time after episode 1's bound: world-timed reads must skip it
	if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state)
		SELECT account_id, 99, '2026-10-01T00:00:00Z', state || '{"injected": true}'::jsonb FROM state_history
		WHERE account_id = $1::uuid AND version = 1`, r.world.Account); err != nil {
		t.Fatalf("inject a future state row: %v", err)
	}
	at := 1
	doc, err := r.svc.Episodes(bg, r.manifest, &at)
	if err != nil {
		t.Fatal(err)
	}
	st := resultField(t, doc)["state"].(map[string]any)
	if st["version"] != 1.0 || strings.Contains(string(doc), "injected") {
		t.Fatalf("the injected future state leaked into episode 1's view: %+v", st)
	}
}
