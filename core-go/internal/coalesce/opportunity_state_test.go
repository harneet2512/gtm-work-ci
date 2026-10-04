package coalesce_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

const crmAccountKey = "account:AC-1"

type dealWorld struct {
	basicWorld
	owen, dealA, dealB string
}

// seedDeals adds a second rep (Owen) and two deals of the account, both mapped from CRM record ids.
func seedDeals(t *testing.T) dealWorld {
	t.Helper()
	w := dealWorld{basicWorld: seedBasic(t)}
	mapping(t, "account", w.account, "crm", crmAccountKey)
	w.owen = scalar(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Owen Clarke', 'owen@ghostvendor.com') RETURNING id::text`)
	mapping(t, "person", w.owen, "email", "owen@ghostvendor.com")
	for key, dst := range map[string]*string{"opp:AC-1-A": &w.dealA, "opp:AC-1-B": &w.dealB} {
		*dst = scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, $2, 'expansion') RETURNING id::text`, w.account, key)
		mapping(t, "opportunity", *dst, "crm", key)
	}
	return w
}

func crmDeal(t *testing.T, record string, at time.Time, stage, ownerEmail string, amount int) normalize.SourceEvent {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"kind": "crm_change", "object_type": "Opportunity", "record_id": record, "account_record_id": crmAccountKey,
		"changed_at": at.Format(time.RFC3339), "changed_by": ownerEmail, "created": false,
		"fields": map[string]any{"StageName": map[string]any{"new": stage}, "OwnerEmail": map[string]any{"new": ownerEmail}, "Amount": map[string]any{"new": amount}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return normalize.SourceEvent{SourceSystem: "crm", SourceObjectID: record, SourceEventKey: "field:StageName:" + stage,
		Connector: "salesforce", ConnectorVersion: "1", OccurredAt: &at, Payload: payload}
}

func dealStateOf(t *testing.T, w dealWorld, deal string) reducer.OpportunityState {
	t.Helper()
	st, found, err := coalesce.OpportunityState(context.Background(), env.DB, w.account, deal)
	if err != nil || !found {
		t.Fatalf("deal %s: found=%v err=%v", deal, found, err)
	}
	return st
}

func TestRecomputeKeepsEveryDealOnItsOwnState(t *testing.T) {
	w := seedDeals(t)
	ctx := context.Background()
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	coalClock := clock.NewFixed(t0.Add(time.Hour))
	s := newService(t, coalClock, coalesce.Options{Extractor: blockerExtractor()})

	ingestAll(t, svc,
		crmDeal(t, "opp:AC-1-A", t0.Add(-5*time.Hour), "Negotiation", "dana@ghostvendor.com", 120000),
		crmDeal(t, "opp:AC-1-B", t0.Add(-9*time.Hour), "Discovery", "owen@ghostvendor.com", 30000))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	a, b := dealStateOf(t, w, w.dealA), dealStateOf(t, w, w.dealB)
	if a.Fields.Stage.Value != "Negotiation" || a.Fields.Owner.Value != w.dana || b.Fields.Stage.Value != "Discovery" || b.Fields.Owner.Value != w.owen {
		t.Fatalf("A = %v/%v, B = %v/%v: each deal must hold its own stage and owner", a.Fields.Stage.Value, a.Fields.Owner.Value, b.Fields.Stage.Value, b.Fields.Owner.Value)
	}
	if v, _ := a.Fields.Amount.Value.(float64); v != 120000 || a.Version != 1 || b.Version != 1 {
		t.Fatalf("amount = %v, versions = %d/%d", a.Fields.Amount.Value, a.Version, b.Version)
	}

	acct, found, err := coalesce.StateAt(ctx, env.DB, w.account, t0.Add(2*time.Hour), coalesce.KnownAt)
	if err != nil || !found || acct.OpportunityID == nil || *acct.OpportunityID != w.dealA || acct.Fields.Owner.Value != w.dana {
		t.Fatalf("the account headline is the primary deal (A, the latest activity): %+v found=%v err=%v", acct.OpportunityID, found, err)
	}
	if len(acct.Opportunities) != 2 || !acct.Opportunities[0].IsPrimary || acct.Opportunities[1].Owner != w.owen {
		t.Fatalf("summaries = %+v", acct.Opportunities)
	}
	validateDeal(t, scalar(t, `SELECT state::text FROM opportunity_state WHERE opportunity_id = $1::uuid`, w.dealA))
	validate(t, scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, w.account))
}

func validateDeal(t *testing.T, raw string) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("opportunity_state", []byte(raw)); err != nil {
		t.Fatalf("stored state violates opportunity_state.v1.json: %v\n%s", err, raw)
	}
}

func TestADealsVersionAdvancesOnlyWhenThatDealChanges(t *testing.T) {
	w := seedDeals(t)
	ctx := context.Background()
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	coalClock := clock.NewFixed(t0.Add(time.Hour))
	s := newService(t, coalClock, coalesce.Options{Extractor: blockerExtractor()})
	ingestAll(t, svc,
		crmDeal(t, "opp:AC-1-A", t0.Add(-5*time.Hour), "Discovery", "dana@ghostvendor.com", 120000),
		crmDeal(t, "opp:AC-1-B", t0.Add(-9*time.Hour), "Discovery", "owen@ghostvendor.com", 30000))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	// An email that names no deal moves the account, not the deals: no new deal version (idempotent recompute).
	ingestClock.Set(t0.Add(2 * time.Hour))
	ingestAll(t, svc, inbound(t, 1, t0.Add(2*time.Hour), "We need the SOC2 report. Thanks"))
	coalClock.Set(t0.Add(3 * time.Hour))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT version::text FROM account_state WHERE account_id = $1::uuid`, w.account); got != "2" {
		t.Fatalf("account version = %s, want 2", got)
	}
	if a, b := dealStateOf(t, w, w.dealA), dealStateOf(t, w, w.dealB); a.Version != 1 || b.Version != 1 {
		t.Fatalf("unrelated recompute re-versioned the deals: %d/%d", a.Version, b.Version)
	}

	// A stage change on deal A versions A alone.
	ingestClock.Set(t0.Add(24 * time.Hour))
	ingestAll(t, svc, crmDeal(t, "opp:AC-1-A", t0.Add(24*time.Hour), "Negotiation", "dana@ghostvendor.com", 120000))
	coalClock.Set(t0.Add(25 * time.Hour))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	a, b := dealStateOf(t, w, w.dealA), dealStateOf(t, w, w.dealB)
	if a.Version != 2 || b.Version != 1 || a.Fields.Stage.Value != "Negotiation" {
		t.Fatalf("A v%d %v, B v%d", a.Version, a.Fields.Stage.Value, b.Version)
	}
	if got := scalar(t, `SELECT count(*)::text FROM opportunity_state_history WHERE account_id = $1::uuid`, w.account); got != "3" {
		t.Fatalf("history rows = %s, want 3 (A v1, A v2, B v1)", got)
	}
}

func TestOpportunityStateAtReadsDealHistoryOnBothClocks(t *testing.T) {
	w := seedDeals(t)
	ctx := context.Background()
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	coalClock := clock.NewFixed(t0.Add(time.Hour))
	s := newService(t, coalClock, coalesce.Options{})
	ingestAll(t, svc, crmDeal(t, "opp:AC-1-A", t0.Add(-5*time.Hour), "Discovery", "dana@ghostvendor.com", 120000))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	ingestClock.Set(t0.Add(24 * time.Hour))
	ingestAll(t, svc, crmDeal(t, "opp:AC-1-A", t0.Add(24*time.Hour), "Negotiation", "dana@ghostvendor.com", 120000))
	coalClock.Set(t0.Add(25 * time.Hour))
	if _, err := s.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if _, found, err := coalesce.OpportunityStateAt(ctx, env.DB, w.account, w.dealA, t0, coalesce.KnownAt); err != nil || found {
		t.Fatalf("before the first computation there is no state: found=%v err=%v", found, err)
	}
	for _, tc := range []struct {
		name    string
		at      time.Time
		basis   coalesce.Basis
		version int
		stage   string
	}{
		{"known between the two computations", t0.Add(12 * time.Hour), coalesce.KnownAt, 1, "Discovery"},
		{"known at the second computation", t0.Add(25 * time.Hour), coalesce.KnownAt, 2, "Negotiation"},
		{"world before the stage change", t0.Add(12 * time.Hour), coalesce.WorldAsOf, 1, "Discovery"},
		{"world at the stage change", t0.Add(24 * time.Hour), coalesce.WorldAsOf, 2, "Negotiation"},
	} {
		st, found, err := coalesce.OpportunityStateAt(ctx, env.DB, w.account, w.dealA, tc.at, tc.basis)
		if err != nil || !found || st.Version != tc.version || st.Fields.Stage.Value != tc.stage {
			t.Errorf("%s: v%d %v found=%v err=%v, want v%d %s", tc.name, st.Version, st.Fields.Stage.Value, found, err, tc.version, tc.stage)
		}
	}
	if _, found, err := coalesce.OpportunityStateAt(ctx, env.DB, w.account, w.dealB, t0.Add(100*time.Hour), coalesce.KnownAt); err != nil || found {
		t.Fatalf("a deal with no evidence has no state: found=%v err=%v", found, err)
	}
	if _, found, _ := coalesce.OpportunityStateAt(ctx, env.DB, w.owen, w.dealA, t0.Add(100*time.Hour), coalesce.KnownAt); found {
		t.Fatal("a deal must not be readable through another account")
	}
	if _, _, err := coalesce.OpportunityStateAt(ctx, env.DB, w.account, w.dealA, t0, "sometime"); err == nil {
		t.Fatal("an unknown basis must be rejected")
	}
	if _, found, err := coalesce.OpportunityState(ctx, env.DB, w.account, w.dealB); err != nil || found {
		t.Fatalf("current state of an untouched deal: found=%v err=%v", found, err)
	}
}
