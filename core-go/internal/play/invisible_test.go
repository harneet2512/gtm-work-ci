package play

import (
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

func kinds(rep Report) map[string]bool {
	out := map[string]bool{}
	for _, l := range rep.Leaks {
		out[l.Store+"/"+l.Kind] = true
	}
	return out
}

func TestAWorldWithoutEventNIsWithheld(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	rep, err := r.svc.Invisibility(bg, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusWithheld || len(rep.Leaks) != 0 || rep.HeldOutEventID != r.held.EventID || rep.ManifestID != r.manifest {
		t.Fatalf("report = %+v", rep)
	}
	for _, want := range []string{"source_event", "activity", "claim", "account_state", "state_history", "state_diff", "signal", "agent_run", "neo4j"} {
		found := false
		for _, c := range rep.Checked {
			found = found || c == want
		}
		if !found {
			t.Errorf("the report does not say it checked %s: %v", want, rep.Checked)
		}
	}
	if r.probe.callCount() != 1 || !contains(r.probe.calls[0], r.held.EventID) {
		t.Fatalf("Neo4j must be asked about the held-out event id: %v", r.probe.calls)
	}
	if len(r.probe.laterFrom) != 1 || !r.probe.laterFrom[0].Equal(r.held.Event.OccurredAt.UTC()) {
		t.Fatalf("Neo4j must be asked for the activities at or after event N: %v", r.probe.laterFrom)
	}
}

// An injected leak is detected, one store at a time.
func TestAnInjectedLeakIsDetected(t *testing.T) {
	cases := []struct {
		name   string
		inject func(*testing.T, *rig)
		want   string
	}{
		{"the event was ingested out of band", func(t *testing.T, r *rig) {
			if _, err := r.stack.Ingest.Ingest(bg, r.held.Event); err != nil {
				t.Fatal(err)
			}
		}, "postgres/source_event"},
		{"the event was ingested and recomputed out of band", func(t *testing.T, r *rig) {
			if _, err := r.stack.Ingest.Ingest(bg, r.held.Event); err != nil {
				t.Fatal(err)
			}
			if err := r.stack.Drain(bg); err != nil {
				t.Fatal(err)
			}
		}, "postgres/state_diff"},
		{"a source event stands under the dataset id", func(t *testing.T, r *rig) {
			replaytest.One(t, env.DB, `INSERT INTO source_events (id, source_system, source_object_id, source_event_key, idempotency_key, payload)
				VALUES ($1::uuid, 'crm', 'other', 'k', repeat('c', 64), '{}') RETURNING id::text`, r.held.EventID)
		}, "postgres/source_event"},
		{"the account state embeds the event id", func(t *testing.T, r *rig) {
			if _, err := env.DB.Exec(`UPDATE account_state SET state = state || jsonb_build_object('note', $2::text) WHERE account_id = $1::uuid`,
				r.world.Account, "derived from "+r.held.EventID); err != nil {
				t.Fatal(err)
			}
		}, "postgres/account_state"},
		{"the state history embeds the event id", func(t *testing.T, r *rig) {
			if _, err := env.DB.Exec(`UPDATE state_history SET state = state || jsonb_build_object('note', $2::text) WHERE account_id = $1::uuid`,
				r.world.Account, "derived from "+r.held.EventID); err != nil {
				t.Fatal(err)
			}
		}, "postgres/state_history"},
		{"a graph activity is dated at or after event N", func(t *testing.T, r *rig) {
			r.probe.laterHits = []ctxgraph.Hit{{Kind: "node", Type: "Activity", ID: "a-later"}}
		}, "neo4j/graph_node:Activity"},
		{"a graph node cites the event", func(t *testing.T, r *rig) {
			r.probe.hits = []ctxgraph.Hit{{Kind: "node", Type: "Claim", ID: "k-1"}}
		}, "neo4j/graph_node:Claim"},
		{"a graph edge cites the event", func(t *testing.T, r *rig) {
			r.probe.hits = []ctxgraph.Hit{{Kind: "edge", Type: "ABOUT", ID: "e-1"}}
		}, "neo4j/graph_edge:ABOUT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, replaytest.NewHeldOut(2))
			tc.inject(t, r)
			rep, err := r.svc.Invisibility(bg, r.manifest)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Status != StatusLeaked || !kinds(rep)[tc.want] {
				t.Fatalf("report = %+v, want a leak of %s", rep, tc.want)
			}
		})
	}
}

func TestEveryStoreOfAFullOutOfBandReleaseIsReported(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	if _, err := r.stack.Ingest.Ingest(bg, r.held.Event); err != nil {
		t.Fatal(err)
	}
	if err := r.stack.Drain(bg); err != nil {
		t.Fatal(err)
	}
	rep, err := r.svc.Invisibility(bg, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(rep)
	for _, want := range []string{"source_event", "activity", "claim", "account_state", "state_history", "state_diff", "signal", "trigger_evaluation",
		"agent_run", "graph_projection_job"} {
		if !got["postgres/"+want] {
			t.Errorf("a derived %s exists but is not reported: %v", want, rep.Leaks)
		}
	}
	for _, l := range rep.Leaks {
		if l.ID == "" || l.Store == "" || l.Kind == "" {
			t.Errorf("an incomplete leak entry: %+v", l)
		}
	}
}

func TestAnUncheckableGraphIsNeverReportedAsClean(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	r.probe.err = ctxgraph.ErrGraphUnavailable
	if _, err := r.svc.Invisibility(bg, r.manifest); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("unreachable Neo4j: %v, want ErrGraphUnavailable", err)
	}
	r.probe.err = nil
	noProbe := r.service(func(o *Options) { o.Probe = nil })
	if _, err := noProbe.Invisibility(bg, r.manifest); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("no graph configured: %v, want ErrGraphUnavailable", err)
	}
	r.probe.err = errors.New("boom")
	if _, err := r.svc.Invisibility(bg, r.manifest); err == nil || errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("an unexpected probe failure must stay an error: %v", err)
	}
}

func TestAReleasedManifestIsReportedReleasedAndNotInspected(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	if _, err := r.play(); err != nil {
		t.Fatal(err)
	}
	calls := r.probe.callCount()
	rep, err := r.svc.Invisibility(bg, r.manifest)
	if err != nil || rep.Status != StatusReleased || len(rep.Leaks) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if r.probe.callCount() != calls {
		t.Fatal("a released event is legitimately present: nothing to probe")
	}
}

func TestInvisibilityOfAnUnknownManifest(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	for _, id := range []string{"99999999-9999-4999-8999-999999999999", "not-a-uuid", ""} {
		if _, err := r.svc.Invisibility(bg, id); !errors.Is(err, ErrManifestNotFound) {
			t.Errorf("%q: %v, want ErrManifestNotFound", id, err)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The check proves the world stops at N-1: it does not look only for event N's own ids.
func TestTheWorldMustStopAtNMinusOne(t *testing.T) {
	cases := []struct {
		name   string
		inject func(*testing.T, *rig)
		want   string
	}{
		{"a later event was ingested", func(t *testing.T, r *rig) {
			later := replaytest.Email(9, replaytest.T0.Add(3*time.Hour), "inbound", "Checking in again")
			if _, err := r.stack.Ingest.Ingest(bg, later); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindLaterActivity},
		{"the same real event stands under another source key", func(t *testing.T, r *rig) {
			// the same real email (same instant, same text) under another source key (system, object, event key)
			dup := replaytest.Email(8, *r.held.Event.OccurredAt, "inbound", "We need the SOC2 report before signing. Thanks")
			if refOf(dup) == refOf(r.held.Event) {
				t.Fatal("the copy must have another idempotency key")
			}
			if _, err := r.stack.Ingest.Ingest(bg, dup); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindLaterActivity},
		{"an event dated exactly at N was ingested", func(t *testing.T, r *rig) {
			at := replaytest.Email(77, *r.held.Event.OccurredAt, "inbound", "A different email at the same instant")
			if _, err := r.stack.Ingest.Ingest(bg, at); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindLaterActivity},
		{"AccountState was last moved by something other than N-1", func(t *testing.T, r *rig) {
			if _, err := env.DB.Exec(`UPDATE account_state SET last_activity_id = (SELECT a.id FROM activities a
				JOIN source_events se ON se.id = a.source_event_id WHERE se.source_object_id = '<m1@x.com>') WHERE account_id = $1::uuid`, r.world.Account); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindStateCursor},
		{"AccountState has no cursor", func(t *testing.T, r *rig) {
			if _, err := env.DB.Exec(`UPDATE account_state SET last_activity_id = NULL WHERE account_id = $1::uuid`, r.world.Account); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindStateCursor},
		{"there is no AccountState at all", func(t *testing.T, r *rig) {
			if _, err := env.DB.Exec(`DELETE FROM account_state WHERE account_id = $1::uuid`, r.world.Account); err != nil {
				t.Fatal(err)
			}
		}, "postgres/" + kindStateCursor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, replaytest.NewHeldOut(2))
			tc.inject(t, r)
			rep, err := r.svc.Invisibility(bg, r.manifest)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Status != StatusLeaked || !kinds(rep)[tc.want] {
				t.Fatalf("report = %+v, want a leak of %s", rep, tc.want)
			}
			if _, err := r.play(); err == nil {
				t.Fatal("Play released an event into a world that is not at N-1")
			} else if _, ok := err.(*VisibleError); !ok {
				t.Fatalf("Play: %v, want a VisibleError", err)
			}
		})
	}
}

func TestACleanWorldAtNMinusOnePasses(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	rep, err := r.svc.Invisibility(bg, r.manifest)
	if err != nil || rep.Status != StatusWithheld || len(rep.Leaks) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	for _, want := range []string{kindLaterActivity, kindStateCursor} {
		found := false
		for _, c := range rep.Checked {
			found = found || c == want
		}
		if !found {
			t.Errorf("the report does not say it checked %s: %v", want, rep.Checked)
		}
	}
}
