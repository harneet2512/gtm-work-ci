package coalesce_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// failingFor makes the worker fail for activities whose text contains marker and answer for the rest.
func failingFor(marker string, err error) *claimstest.FakeExtractor {
	ok := blockerExtractor()
	return &claimstest.FakeExtractor{
		Candidates: ok.Candidates,
		Err: func(req claims.ExtractRequest) error {
			if strings.Contains(req.Text, marker) {
				return err
			}
			return nil
		},
	}
}

func TestPermanentExtractionFailureQuarantinesTheActivityNotTheAccount(t *testing.T) {
	w := seedBasic(t)
	svc := ingestService(t, clock.NewFixed(t0))
	acts := ingestAll(t, svc,
		inbound(t, 1, t0.Add(-2*time.Hour), "POISON text the worker rejects. More"),
		inbound(t, 2, t0.Add(-time.Hour), "A healthy blocker. More"))
	ex := failingFor("POISON", claims.Permanent(errors.New("worker 422 invalid_request")))
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{Extractor: ex})

	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 1 || res.Failed != 0 {
		t.Fatalf("the healthy activity must still produce state: %+v %v", res, err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM claims WHERE account_id = $1::uuid`, w.account); got != "1" {
		t.Fatalf("claims = %s, want only the healthy activity's", got)
	}
	clk.Advance(time.Hour)
	q, err := s.Quarantined(context.Background())
	if err != nil || len(q) != 1 || q[0].ActivityID != acts[0] || !q[0].Permanent || q[0].Attempts != 1 || q[0].AccountID != w.account ||
		!strings.Contains(q[0].Reason, "422") || q[0].Age != time.Hour {
		t.Fatalf("quarantine = %+v %v", q, err)
	}
	if n := ex.CallsFor(acts[0]); n != 1 {
		t.Fatalf("worker calls for the poison activity = %d", n)
	}

	// A later recompute never asks the worker about it again, until an operator lifts the quarantine.
	insertJob(t, w.account, t0, []string{acts[0], acts[1]}, "", time.Time{})
	if _, err := s.Drain(context.Background()); err != nil || ex.CallsFor(acts[0]) != 1 {
		t.Fatalf("quarantined activity re-extracted: calls=%d err=%v", ex.CallsFor(acts[0]), err)
	}
	ex.Err = nil
	if err := s.Unquarantine(context.Background(), acts[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Unquarantine(context.Background(), acts[0]); !errors.Is(err, coalesce.ErrNotQuarantined) {
		t.Fatal("unquarantining twice must fail")
	}
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 1 || ex.CallsFor(acts[0]) != 2 {
		t.Fatalf("after unquarantine the activity is extracted: %+v calls=%d err=%v", res, ex.CallsFor(acts[0]), err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM claims WHERE account_id = $1::uuid`, w.account); got != "2" {
		t.Fatalf("claims = %s", got)
	}
}

// insertCRMStage adds a CRM stage-change activity (a deterministic, rule-extracted source) to the account.
func insertCRMStage(t *testing.T, account, stage string, at time.Time) string {
	t.Helper()
	payload := `{"kind":"crm_change","object_type":"Opportunity","record_id":"opp:X","created":false,"fields":{"StageName":{"new":"` + stage + `"}}}`
	ev := scalar(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('crm', 'opp:X', $1::text, encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), $2::jsonb) RETURNING id::text`, "field:StageName:"+stage, payload)
	return scalar(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'OpportunityStageChanged', 'crm', 'opp:X', $2, $3::uuid, '{"source_system":"crm","source_object_id":"opp:X"}'::jsonb) RETURNING id::text`, ev, at, account)
}

func TestWorkerOutageParksTheJobQuarantinesNothingKeepsRuleClaimsAndRecovers(t *testing.T) {
	w := seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-2*time.Hour), "A blocker the worker will read. More"))
	crm := insertCRMStage(t, w.account, "Technical evaluation", t0.Add(-time.Hour))
	if _, err := env.DB.Exec(`UPDATE recompute_jobs SET activity_ids = activity_ids || $1::uuid`, crm); err != nil {
		t.Fatal(err)
	}
	ex := failingFor("blocker", errors.New("worker 503 service unavailable"))
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{Extractor: ex, MaxAttempts: 3, RetryDelay: time.Second, ParkRetry: 10 * time.Minute})

	for attempt := 1; attempt <= 3; attempt++ {
		res, err := s.Drain(context.Background())
		if err == nil || res.Failed != 1 {
			t.Fatalf("attempt %d must fail: %+v %v", attempt, res, err)
		}
		clk.Advance(time.Minute)
	}
	if q, _ := s.Quarantined(context.Background()); len(q) != 0 {
		t.Fatalf("a worker outage must never quarantine an activity: %+v", q)
	}
	parked, _ := s.Parked(context.Background())
	if len(parked) != 1 || parked[0].AccountID != w.account || !strings.Contains(parked[0].LastError, "503") {
		t.Fatalf("the job must be parked with the cause: %+v", parked)
	}
	if got := scalar(t, `SELECT string_agg(field_path || ':' || standing, ',') FROM claims WHERE account_id = $1::uuid`, w.account); got != "stage:crm_explicit" {
		t.Fatalf("the deterministic CRM claim must be written despite the model failure, got %s", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM account_state`); got != "0" {
		t.Fatal("no state until the AI claims can be extracted")
	}

	// The backoff sweep un-parks the job once the worker is back; nothing was lost.
	ex.Err = nil
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 0 {
		t.Fatalf("still inside the park backoff: %+v %v", res, err)
	}
	clk.Advance(11 * time.Minute)
	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 1 || len(res.Recomputes[0].ActivityIDs) != 2 {
		t.Fatalf("recovery: %+v %v", res, err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM claims WHERE account_id = $1::uuid`, w.account); got != "2" {
		t.Fatalf("claims = %s, want the CRM claim and the AI claim", got)
	}
	if parked, _ := s.Parked(context.Background()); len(parked) != 0 {
		t.Fatalf("still parked: %+v", parked)
	}
}

func TestCancelledContextIsNeverAReasonToQuarantine(t *testing.T) {
	seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-time.Hour), "Budget is tight. More"))
	ex := &claimstest.FakeExtractor{Err: func(claims.ExtractRequest) error { return context.DeadlineExceeded }}
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{Extractor: ex, MaxAttempts: 1})
	if _, err := s.Drain(context.Background()); err == nil {
		t.Fatal("a timeout must fail the job")
	}
	if q, _ := s.Quarantined(context.Background()); len(q) != 0 {
		t.Fatalf("a timeout quarantined an activity: %+v", q)
	}
}

func TestPersistentRecomputeFailureParksTheAccountUntilNewActivityArrives(t *testing.T) {
	w := seedBasic(t)
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	ingestAll(t, svc, inbound(t, 1, t0.Add(-time.Hour), "Budget is tight. More"))
	fail := true
	hook := coalesce.HookFunc(func(context.Context, *sql.Tx, *reducer.AccountState, reducer.AccountState, []string, []claims.Conflict) error {
		if fail {
			return errors.New("diff writer down")
		}
		return nil
	})
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{Extractor: blockerExtractor(), Hook: hook, MaxAttempts: 2, RetryDelay: time.Second})
	for attempt := 1; attempt <= 2; attempt++ {
		if res, err := s.Drain(context.Background()); err == nil || res.Failed != 1 {
			t.Fatalf("attempt %d: %+v %v", attempt, res, err)
		}
		clk.Advance(time.Minute)
	}
	if res, err := s.Drain(context.Background()); err != nil || res.Failed != 0 || len(res.Recomputes) != 0 {
		t.Fatalf("a parked job must not be retried: %+v %v", res, err)
	}
	parked, _ := s.Parked(context.Background())
	if len(parked) != 1 || parked[0].AccountID != w.account || !strings.Contains(parked[0].LastError, "diff writer down") {
		t.Fatalf("parked = %+v", parked)
	}

	fail = false
	ingestClock.Set(clk.Now())
	ingestAll(t, svc, inbound(t, 2, clk.Now().Add(-time.Minute), "Another thing. More"))
	clk.Advance(debounce + time.Second)
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 1 || len(res.Recomputes[0].ActivityIDs) != 2 {
		t.Fatalf("new activity must un-park the account: %+v %v", res, err)
	}
	if parked, _ := s.Parked(context.Background()); len(parked) != 0 {
		t.Fatalf("still parked: %+v", parked)
	}
}
