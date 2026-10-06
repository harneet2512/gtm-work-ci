package coalesce_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

// ADR-0019: the state at world time T is the highest version whose as_of (the newest folded
// activity's occurred_at) is strictly earlier than T.
func TestStateBeforeReadsVersionsStrictlyBeforeAWorldTime(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	svc := ingestService(t, clock.NewFixed(t0))
	s := newService(t, clock.NewFixed(t0.Add(time.Hour)), coalesce.Options{Extractor: blockerExtractor()})
	e1, e2 := t0.Add(-3*time.Hour), t0.Add(-2*time.Hour)
	ingestAll(t, svc, inbound(t, 1, e1, "First blocker. More"))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ingestAll(t, svc, inbound(t, 2, e2, "Second blocker. More"))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		at      time.Time
		version int // 0: no state before that time
	}{
		{"long before the first event", e1.Add(-24 * time.Hour), 0},
		{"exactly at the first event: strict, the event is not before itself", e1, 0},
		{"one microsecond after the first event", e1.Add(time.Microsecond), 1},
		{"between the events", e1.Add(30 * time.Minute), 1},
		{"exactly at the second event: strict, v2 is excluded", e2, 1},
		{"one microsecond after the second event", e2.Add(time.Microsecond), 2},
		{"long after", e2.Add(1000 * time.Hour), 2},
	} {
		st, found, err := coalesce.StateBefore(ctx, env.DB, w.account, tc.at)
		if err != nil || found != (tc.version != 0) || st.Version != tc.version {
			t.Errorf("%s: version=%d found=%v err=%v, want %d", tc.name, st.Version, found, err, tc.version)
		}
	}
}

func TestStateBeforeFallsBackWhenAVersionCoalescedABurstAcrossTheTime(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	svc := ingestService(t, clock.NewFixed(t0))
	s := newService(t, clock.NewFixed(t0.Add(time.Hour)), coalesce.Options{Extractor: blockerExtractor()})
	e1, e2, e3 := t0.Add(-4*time.Hour), t0.Add(-3*time.Hour), t0.Add(-2*time.Hour)
	ingestAll(t, svc, inbound(t, 1, e1, "First blocker. More"))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ingestAll(t, svc, inbound(t, 2, e2, "Second blocker. More"), inbound(t, 3, e3, "Third blocker. More"))
	res, err := s.Drain(ctx)
	if err != nil || len(res.Recomputes) != 1 || res.Recomputes[0].Version != 2 {
		t.Fatalf("the burst must be one version: %+v %v", res, err)
	}

	// v2 folds E2 and E3 and its as_of is E3. At a time between them v2 would show E3, so it must not be used.
	st, found, err := coalesce.StateBefore(ctx, env.DB, w.account, e2.Add(time.Minute))
	if err != nil || !found || st.Version != 1 {
		t.Fatalf("inside the burst: version=%d found=%v err=%v, want the version before the burst", st.Version, found, err)
	}
	if body := stateJSON(t, st); strings.Contains(body, "Third blocker") || strings.Contains(body, "Second blocker") {
		t.Fatalf("a fact from the burst leaked into the answer: %s", body)
	}
	if st, found, err = coalesce.StateBefore(ctx, env.DB, w.account, e3.Add(time.Microsecond)); err != nil || !found || st.Version != 2 {
		t.Fatalf("after the burst: version=%d found=%v err=%v", st.Version, found, err)
	}
}

func TestStateBeforeBreaksTiesOnAsOfByTheHigherVersion(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	for _, v := range []int{1, 2, 3} {
		if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, $2, $3, $4::jsonb)`,
			w.account, v, t0, `{"version":`+string(rune('0'+v))+`}`); err != nil {
			t.Fatal(err)
		}
	}
	st, found, err := coalesce.StateBefore(ctx, env.DB, w.account, t0.Add(time.Second))
	if err != nil || !found || st.Version != 3 {
		t.Fatalf("version=%d found=%v err=%v", st.Version, found, err)
	}
	if _, found, _ := coalesce.StateBefore(ctx, env.DB, w.account, t0); found {
		t.Fatal("versions as_of exactly T are not strictly before T")
	}
}

func TestStateJSONBeforeReturnsTheStoredDocumentUnchanged(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-time.Hour), "First blocker. More"))
	if _, err := newService(t, clock.NewFixed(t0.Add(time.Hour)), coalesce.Options{Extractor: blockerExtractor()}).Drain(ctx); err != nil {
		t.Fatal(err)
	}
	raw, found, err := coalesce.StateJSONBefore(ctx, env.DB, w.account, t0)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	var got, want map[string]any
	stored := scalar(t, `SELECT state::text FROM state_history WHERE account_id = $1::uuid AND version = 1`, w.account)
	if err := json.Unmarshal(raw, &got); err != nil || json.Unmarshal([]byte(stored), &want) != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got["account_name"] != want["account_name"] || got["version"] != want["version"] {
		t.Fatalf("the world read must return exactly what was stored: %s vs %s", raw, stored)
	}
	if _, _, err := coalesce.StateJSONBefore(ctx, env.DB, "not-a-uuid", t0); err == nil {
		t.Fatal("a bad account id must be an error")
	}
}

func stateJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// M4: a replay drives the adjudication clock with the world time of the event it folds, so the claim status the
// recompute stores is the one claimstore.ClaimsAsOf computes for the world graph at that time. Without it the
// recompute adjudicates at the wall clock and a claim that expired in between is already expired in the stored
// state while the world graph at T still holds it.
func TestRecomputeAdjudicatesAtTheReplayClockSoStoredStateAndWorldGraphAgree(t *testing.T) {
	for _, tc := range []struct {
		name   string
		replay bool
	}{{"with the replay clock", true}, {"without it: the documented wall-clock default", false}} {
		t.Run(tc.name, func(t *testing.T) {
			w := seedBasic(t)
			ctx := context.Background()
			svc := ingestService(t, clock.NewFixed(t0))
			e1 := t0.Add(-3 * time.Hour)
			e2 := e1.Add(30 * time.Minute)
			opts := coalesce.Options{Extractor: blockerExtractor()}
			wall := clock.NewFixed(t0.Add(time.Hour))
			replay := clock.NewFixed(e1)
			if tc.replay {
				opts.AdjudicationClock = replay
			}
			s := newService(t, wall, opts)
			ingestAll(t, svc, inbound(t, 1, e1, "First blocker. More"))
			if _, err := s.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			first := scalar(t, `SELECT id::text FROM claims WHERE account_id = $1::uuid ORDER BY occurred_at LIMIT 1`, w.account)
			// the claim is valid for an hour after E1: standing at E2 (30 minutes later), expired by the wall clock
			if _, err := env.DB.Exec(`UPDATE claims SET expires_at = $2 WHERE id = $1::uuid`, first, e1.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			replay.Set(e2)
			ingestAll(t, svc, inbound(t, 2, e2, "Second blocker. More"))
			if _, err := s.Drain(ctx); err != nil {
				t.Fatal(err)
			}
			stored := scalar(t, `SELECT status FROM claims WHERE id = $1::uuid`, first)
			at, err := claimstore.ClaimsAsOf(ctx, env.DB, w.account, e2.Add(time.Microsecond))
			if err != nil {
				t.Fatal(err)
			}
			world := string(at[first].Status)
			if tc.replay && stored != world {
				t.Fatalf("stored status %q, world graph at E2 says %q", stored, world)
			}
			if !tc.replay && stored == world {
				t.Fatalf("the control must show the divergence the replay clock closes: both %q", stored)
			}
		})
	}
}
