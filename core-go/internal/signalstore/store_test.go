package signalstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

type fixture struct{ account, activity, claim string }

// seed creates an account, an activity, a blocker claim and the state history row a diff needs.
func seed(t *testing.T) fixture {
	t.Helper()
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE agent_runs, trigger_evaluations, signals, state_diffs, state_history, account_state, claims,
		activities, source_events, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	f := fixture{}
	f.account = scalar(t, `INSERT INTO accounts (name) VALUES ('Acme') RETURNING id::text`)
	ev := scalar(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', 'm1', 'received', repeat('a', 64), '{}') RETURNING id::text`)
	f.activity = scalar(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', 'm1', $2, $3::uuid, '{"source_system":"email","source_object_id":"m1"}') RETURNING id::text`, ev, t0, f.account)
	f.claim = scalar(t, `INSERT INTO claims (account_id, field_path, value, standing, confidence, source_activity_id, occurred_at, extractor, evidence_quote)
		VALUES ($1::uuid, 'blockers', '"Need the SOC2 report"', 'first_party_ai', 0.9, $2::uuid, $3, 'test', 'Need the SOC2 report') RETURNING id::text`, f.account, f.activity, t0)
	for v := 1; v <= 2; v++ {
		if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, $2, $3, '{}')`, f.account, v, t0); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func diff(account string, to int) statediff.Diff {
	return statediff.Diff{AccountID: account, FromVersion: to - 1, ToVersion: to, IsMaterial: true,
		Changes: []statediff.Change{{Field: "blockers", Op: statediff.OpSet, Material: true}}}
}

func TestDiffAndSignalWritersAreIdempotent(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id, inserted, err := signalstore.InsertDiff(ctx, env.DB, diff(f.account, 1), t0)
	if err != nil || !inserted {
		t.Fatalf("%v %v", inserted, err)
	}
	if again, inserted, err := signalstore.InsertDiff(ctx, env.DB, diff(f.account, 1), t0); err != nil || inserted || again != id {
		t.Fatalf("a repeated diff must return the first: %v %v %v", again, inserted, err)
	}
	exp := t0.Add(signals.EventWindow)
	in := []signals.Signal{
		{Type: "security_blocker_appeared", Rule: "sig.security_blocker@1", SubjectClaimID: f.claim, OccurredAt: t0, DedupeKey: "k1",
			Details: map[string]any{"blocker": "x"}},
		{Type: "customer_replied", Rule: "sig.customer_replied@1", OccurredAt: t0, ExpiresAt: &exp, DedupeKey: "k2"},
	}
	sc := signalstore.Scope{AccountID: f.account, StateDiffID: id, CreatedAt: t0}
	first, err := signalstore.InsertSignals(ctx, env.DB, sc, in)
	if err != nil || len(first) != 2 {
		t.Fatalf("%v %v", first, err)
	}
	second, err := signalstore.InsertSignals(ctx, env.DB, sc, in)
	if err != nil || second[0] != first[0] || second[1] != first[1] {
		t.Fatalf("a repeat must return the same ids: %v vs %v (%v)", second, first, err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM signals`); n != "2" {
		t.Fatalf("signals = %s", n)
	}
	bad := []signals.Signal{{Type: "customer_replied", Rule: "r", ExpiresAt: &exp, OccurredAt: t0}}
	if _, err := signalstore.InsertSignals(ctx, env.DB, sc, bad); err == nil {
		t.Fatal("a signal without a dedupe key must be an error")
	}
}

func TestEvaluationIsOnePerDiffAndFeedsOpenRunAndCooldown(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id, _, err := signalstore.InsertDiff(ctx, env.DB, diff(f.account, 1), t0)
	if err != nil {
		t.Fatal(err)
	}
	if open, err := signalstore.OpenRunExists(ctx, env.DB, f.account); err != nil || open {
		t.Fatalf("no run yet: %v %v", open, err)
	}
	if last, err := signalstore.LastEligibleAt(ctx, env.DB, f.account); err != nil || last != nil {
		t.Fatalf("no eligible evaluation yet: %v %v", last, err)
	}
	ev := trigger.Evaluation{Workflow: trigger.Workflow, Eligible: true, ReasonCodes: []string{trigger.EligibleCustomerReplied}, Explanation: "x"}
	st, err := signalstore.InsertEvaluation(ctx, env.DB, f.account, id, ev, nil, t0)
	if err != nil || !st.Inserted || !st.Eligible {
		t.Fatalf("%+v %v", st, err)
	}
	other := trigger.Evaluation{Workflow: trigger.Workflow, ReasonCodes: []string{trigger.NoRelevantSignal}}
	again, err := signalstore.InsertEvaluation(ctx, env.DB, f.account, id, other, nil, t0.Add(time.Hour))
	if err != nil || again.Inserted || again.ID != st.ID || !again.Eligible {
		t.Fatalf("a second evaluation of a diff returns the first: %+v %v", again, err)
	}
	last, err := signalstore.LastEligibleAt(ctx, env.DB, f.account)
	if err != nil || last == nil || !last.Equal(t0) {
		t.Fatalf("last eligible = %v %v", last, err)
	}
	if _, err := env.DB.Exec(`INSERT INTO agent_runs (account_id, workflow, run_mode, trigger_evaluation_id, trigger_activity_ids)
		VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', $2::uuid, ARRAY[$3]::uuid[])`, f.account, st.ID, f.activity); err != nil {
		t.Fatal(err)
	}
	if open, err := signalstore.OpenRunExists(ctx, env.DB, f.account); err != nil || !open {
		t.Fatalf("open run: %v %v", open, err)
	}
}

func TestLoadRecordsResolvesTheItemKeyAndSituationAtSeesTheSignals(t *testing.T) {
	f := seed(t)
	ctx := context.Background()
	id, _, _ := signalstore.InsertDiff(ctx, env.DB, diff(f.account, 1), t0)
	sc := signalstore.Scope{AccountID: f.account, StateDiffID: id, CreatedAt: t0}
	_, err := signalstore.InsertSignals(ctx, env.DB, sc, []signals.Signal{
		{Type: "security_blocker_appeared", Rule: "sig.security_blocker@1", SubjectClaimID: f.claim, OccurredAt: t0, DedupeKey: "k1"}})
	if err != nil {
		t.Fatal(err)
	}
	recs, err := signalstore.LoadRecords(ctx, env.DB, f.account, time.Time{}, coalesce.WorldAsOf)
	if err != nil || len(recs) != 1 || recs[0].SubjectItemKey != "need the soc2 report" || recs[0].SubjectClaimID != f.claim {
		t.Fatalf("records = %+v %v", recs, err)
	}
	if _, err := env.DB.Exec(`UPDATE state_history SET state = $2::jsonb WHERE account_id = $1::uuid AND version = 1`, f.account,
		`{"account_id":"`+f.account+`","version":1,"as_of":"2026-09-29T12:00:00Z","computed_at":"2026-09-29T12:00:00Z","fields":{},"buying_group":[],"coverage_gaps":[]}`); err != nil {
		t.Fatal(err)
	}
	sit, found, err := signalstore.SituationAt(ctx, env.DB, f.account, t0.Add(time.Hour), coalesce.WorldAsOf)
	if err != nil || !found || len(sit.Signals) != 1 || sit.Signals[0].SubjectItemKey != "need the soc2 report" {
		t.Fatalf("situation: found=%v err=%v %+v", found, err, sit.Signals)
	}
	if _, found, err := signalstore.SituationAt(ctx, env.DB, f.account, t0.Add(-time.Hour), coalesce.WorldAsOf); err != nil || found {
		t.Fatalf("no state before t0: %v %v", found, err)
	}
}
