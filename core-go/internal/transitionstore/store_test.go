package transitionstore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var (
	ctx = context.Background()
	t0  = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

type seeded struct{ account, activity string }

// seed creates one account with one activity and a state_history row per version 1..3 (the FK of a transition).
func seed(t *testing.T) seeded {
	t.Helper()
	if err := storetest.Purge(ctx, env.DB, `TRUNCATE state_history, account_state, activities, source_events, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	var s seeded
	one := func(q string, args ...any) string {
		var out string
		if err := env.DB.QueryRow(q, args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return out
	}
	s.account = one(`INSERT INTO accounts (name) VALUES ('Acme') RETURNING id::text`)
	ev := one(`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', 'm1', 'received', encode(sha256(convert_to('m1', 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id::text`)
	s.activity = one(`INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', 'm1', $2, $3::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb) RETURNING id::text`, ev, t0, s.account)
	for v := 1; v <= 3; v++ {
		if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, computed_at, trigger_activity_ids, state)
			VALUES ($1::uuid, $2, $3, $3, ARRAY[$4]::uuid[], '{}'::jsonb)`, s.account, v, t0, s.activity); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func facts(key string, required, satisfied bool, activity string) []transitions.FactResult {
	f := transitions.FactResult{Key: key, Description: key, Required: required, Satisfied: satisfied, EvidenceRefs: []transitions.Ref{}, SignalIDs: []string{}}
	if satisfied {
		f.EvidenceRefs = []transitions.Ref{{ActivityID: activity}}
	}
	return []transitions.FactResult{f}
}

func candidate(s seeded, version int) transitions.Record {
	to := "EXPANSION"
	return transitions.Record{AccountID: s.account, FromState: "REORG", ToStateCandidate: &to, Status: transitions.StatusCandidate,
		TriggerActivityIDs: []string{s.activity}, SupportingFacts: facts("expansion_need_stated", true, true, s.activity),
		MissingFacts: facts("owner_stabilized", true, false, ""), ContradictingFacts: []transitions.FactResult{}, Confidence: 0.4,
		StateVersion: version, RuleSetVersion: "transition_rules:v1", FirstObservedAt: t0, LastUpdatedAt: t0}
}

func TestSaveInsertsThenUpdatesAndKeepsHistory(t *testing.T) {
	s := seed(t)
	stored, err := Save(ctx, env.DB, candidate(s, 1))
	if err != nil || stored.ID == "" || stored.Status != transitions.StatusCandidate || stored.Confidence != 0.4 {
		t.Fatalf("insert = %+v, %v", stored, err)
	}
	open, err := LoadOpen(ctx, env.DB, s.account)
	if err != nil || open == nil || open.ID != stored.ID || len(open.SupportingFacts) != 1 || open.SupportingFacts[0].EvidenceRefs[0].ActivityID != s.activity {
		t.Fatalf("open = %+v, %v", open, err)
	}

	next := *open
	at := t0.Add(48 * time.Hour)
	next.Status, next.Confidence, next.StateVersion, next.ConfirmedAt, next.LastUpdatedAt = transitions.StatusConfirmed, 1, 2, &at, at
	next.MissingFacts = []transitions.FactResult{}
	next.SupportingFacts = append(next.SupportingFacts, facts("owner_stabilized", true, true, s.activity)...)
	updated, err := Save(ctx, env.DB, next)
	if err != nil || updated.ID != stored.ID || updated.ConfirmedAt == nil || !updated.ConfirmedAt.Equal(at) {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	hist, err := History(ctx, env.DB, stored.ID)
	if err != nil || len(hist) != 2 || hist[0].Status != transitions.StatusCandidate || hist[1].Status != transitions.StatusConfirmed {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if open, err := LoadOpen(ctx, env.DB, s.account); err != nil || open != nil {
		t.Errorf("a CONFIRMED transition is not open: %+v %v", open, err)
	}
	rel, err := Current(ctx, env.DB, s.account)
	if err != nil || rel.State != "EXPANSION" || rel.TransitionID != stored.ID || rel.ConfirmedAt == nil {
		t.Errorf("current = %+v, %v", rel, err)
	}
}

func TestCurrentIsUnknownBeforeAnyConfirmation(t *testing.T) {
	s := seed(t)
	if _, err := Save(ctx, env.DB, candidate(s, 1)); err != nil {
		t.Fatal(err)
	}
	rel, err := Current(ctx, env.DB, s.account)
	if err != nil || rel.State != Unknown || rel.TransitionID != "" || rel.ConfirmedAt != nil {
		t.Errorf("current = %+v, %v; a CANDIDATE never makes a state", rel, err)
	}
}

func TestOnlyOneTransitionIsOpenPerAccount(t *testing.T) {
	s := seed(t)
	if _, err := Save(ctx, env.DB, candidate(s, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(ctx, env.DB, candidate(s, 2)); err == nil {
		t.Fatal("a second open transition must be refused explicitly")
	}
}

func TestListIsNewestFirstFilterableAndKeepsTerminalTransitions(t *testing.T) {
	s := seed(t)
	rejected := candidate(s, 1)
	no := true
	rejected.Status, rejected.RejectedAt, rejected.LastUpdatedAt = transitions.StatusRejected, &t0, t0
	rejected.ContradictingFacts = facts("support_risk_high", false, true, s.activity)
	rejected.ContradictingFacts[0].Rejects = &no
	if _, err := Save(ctx, env.DB, rejected); err != nil {
		t.Fatal(err)
	}
	later := candidate(s, 2)
	later.FirstObservedAt, later.LastUpdatedAt = t0.Add(time.Hour), t0.Add(time.Hour)
	if _, err := Save(ctx, env.DB, later); err != nil {
		t.Fatal(err)
	}
	all, err := List(ctx, env.DB, s.account, "", 0)
	if err != nil || len(all) != 2 || all[0].Status != transitions.StatusCandidate || all[1].Status != transitions.StatusRejected {
		t.Fatalf("list = %+v, %v; want the open candidate first, the rejected one still inspectable", all, err)
	}
	only, err := List(ctx, env.DB, s.account, transitions.StatusRejected, 10)
	if err != nil || len(only) != 1 || only[0].RejectedAt == nil {
		t.Fatalf("rejected list = %+v, %v", only, err)
	}
}

func TestForActivityFindsTheTransitionTheActivityTouchedAndOnlyThat(t *testing.T) {
	s := seed(t)
	stored, err := Save(ctx, env.DB, candidate(s, 1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ForActivity(ctx, env.DB, s.account, s.activity)
	if err != nil || got == nil || got.ID != stored.ID || got.Status != transitions.StatusCandidate || len(got.MissingFacts) != 1 {
		t.Fatalf("ForActivity = %+v, %v", got, err)
	}
	other := "99999999-9999-4999-8999-999999999999"
	if got, err := ForActivity(ctx, env.DB, s.account, other); err != nil || got != nil {
		t.Fatalf("an activity no transition names: %+v, %v", got, err)
	}
	if got, err := ForActivity(ctx, env.DB, other, s.activity); err != nil || got != nil {
		t.Fatalf("another account's transitions are not this account's: %+v, %v", got, err)
	}
}

func TestForActivityPrefersTheNewestTransition(t *testing.T) {
	s := seed(t)
	rejected := candidate(s, 1)
	no := true
	rejected.Status, rejected.RejectedAt, rejected.LastUpdatedAt = transitions.StatusRejected, &t0, t0
	rejected.ContradictingFacts = facts("support_risk_high", false, true, s.activity)
	rejected.ContradictingFacts[0].Rejects = &no
	if _, err := Save(ctx, env.DB, rejected); err != nil {
		t.Fatal(err)
	}
	later := candidate(s, 2)
	later.FirstObservedAt, later.LastUpdatedAt = t0.Add(time.Hour), t0.Add(time.Hour)
	stored, err := Save(ctx, env.DB, later)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ForActivity(ctx, env.DB, s.account, s.activity)
	if err != nil || got == nil || got.ID != stored.ID {
		t.Fatalf("ForActivity = %+v, %v; want the newest (open) transition", got, err)
	}
}

func TestOpenReadsTheAccountsOpenTransitionWithoutLockingIt(t *testing.T) {
	s := seed(t)
	if got, err := Open(ctx, env.DB, s.account); err != nil || got != nil {
		t.Fatalf("no transition yet: %+v, %v", got, err)
	}
	stored, err := Save(ctx, env.DB, candidate(s, 1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(ctx, env.DB, s.account)
	if err != nil || got == nil || got.ID != stored.ID || got.Status != transitions.StatusCandidate {
		t.Fatalf("Open = %+v, %v", got, err)
	}
	other := "99999999-9999-4999-8999-999999999999"
	if got, err := Open(ctx, env.DB, other); err != nil || got != nil {
		t.Fatalf("another account: %+v, %v", got, err)
	}
	// a plain read must not queue behind a transaction that holds the open row
	tx, err := env.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := LoadOpen(ctx, tx, s.account); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Open(ctx, env.DB, s.account); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Open waited on a row lock")
	}
}
