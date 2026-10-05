package coalesce_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

const (
	debounce = 3 * time.Second
	maxWait  = 15 * time.Second
)

type basicWorld struct {
	account, dana, priya string
}

// seedBasic creates Acme (acme.com), Dana (employee) and Priya (contact) with email mappings.
func seedBasic(t *testing.T) basicWorld {
	t.Helper()
	resetAll(t)
	w := basicWorld{}
	w.account = scalar(t, `INSERT INTO accounts (name, domain) VALUES ('Acme Corp', 'acme.com') RETURNING id::text`)
	w.dana = scalar(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Dana Kim', 'dana@ghostvendor.com') RETURNING id::text`)
	w.priya = scalar(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Priya Shah', 'priya.shah@acme.com', $1::uuid) RETURNING id::text`, w.account)
	for _, m := range [][2]string{{w.dana, "dana@ghostvendor.com"}, {w.priya, "priya.shah@acme.com"}} {
		if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
			VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func ingestService(t *testing.T, clk clock.Clock) *ingest.Service {
	t.Helper()
	s, err := ingest.NewService(env.DB, ingest.Options{Clock: clk, Debounce: debounce, MaxWait: maxWait})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func inbound(t *testing.T, n int, at time.Time, body string) normalize.SourceEvent {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"kind": "email", "direction": "inbound", "message_id": fmt.Sprintf("<m%d@acme.com>", n), "thread_id": "thr-1",
		"from": map[string]any{"email": "priya.shah@acme.com", "name": "Priya Shah"},
		"to":   []map[string]any{{"email": "dana@ghostvendor.com", "name": "Dana Kim"}},
		"date": at.Format(time.RFC3339), "subject": "Re: rollout", "body_text": body,
	})
	return normalize.SourceEvent{SourceSystem: "email", SourceObjectID: fmt.Sprintf("<m%d@acme.com>", n), SourceEventKey: "received",
		Connector: "gmail", ConnectorVersion: "1", OccurredAt: &at, Payload: payload}
}

func ingestAll(t *testing.T, svc *ingest.Service, evs ...normalize.SourceEvent) []string {
	t.Helper()
	var ids []string
	for _, ev := range evs {
		res, err := svc.Ingest(context.Background(), ev)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		ids = append(ids, res.ActivityID)
	}
	return ids
}

// blockerExtractor emits one blocker per email, quoting the first sentence.
func blockerExtractor() *claimstest.FakeExtractor {
	return &claimstest.FakeExtractor{Candidates: func(req claims.ExtractRequest) []claims.Candidate {
		quote := strings.SplitN(req.Text, ".", 2)[0]
		return []claims.Candidate{{FieldPath: claims.FieldBlockers, Value: json.RawMessage(`"` + quote + `"`), Confidence: 0.9, EvidenceQuote: quote}}
	}}
}

func recordingHook() (coalesce.Hook, *[]hookCall) {
	var calls []hookCall
	h := coalesce.HookFunc(func(_ context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState, acts []string, conflicts []claims.Conflict) error {
		var inTx string
		if err := tx.QueryRow(`SELECT version::text FROM account_state WHERE account_id = $1::uuid`, next.AccountID).Scan(&inTx); err != nil {
			return fmt.Errorf("the hook must see the new state inside its transaction: %w", err)
		}
		calls = append(calls, hookCall{prev: prev, next: next, acts: acts, conflicts: conflicts, versionSeenInTx: inTx})
		return nil
	})
	return h, &calls
}

type hookCall struct {
	prev            *reducer.AccountState
	next            reducer.AccountState
	acts            []string
	conflicts       []claims.Conflict
	versionSeenInTx string
}

func validate(t *testing.T, raw string) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("account_state", []byte(raw)); err != nil {
		t.Fatalf("stored state violates account_state.v1.json: %v\n%s", err, raw)
	}
}

func TestRecomputeWritesStateHistoryAndClearsTheJob(t *testing.T) {
	w := seedBasic(t)
	clk := clock.NewFixed(t0)
	acts := ingestAll(t, ingestService(t, clk), inbound(t, 1, t0.Add(-time.Hour), "We need the SOC2 report before signing. Thanks"))
	ex := blockerExtractor()
	hook, calls := recordingHook()
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{Extractor: ex, Hook: hook, WorkerID: "w1"})

	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 1 || res.Failed != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := scalar(t, `SELECT version::text FROM account_state WHERE account_id = $1::uuid`, w.account); got != "1" {
		t.Fatalf("version = %s", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM state_history WHERE account_id = $1::uuid`, w.account); got != "1" {
		t.Fatalf("history rows = %s", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM recompute_jobs`); got != "0" {
		t.Fatalf("the finished job must be deleted, %s left", got)
	}
	stored := scalar(t, `SELECT state::text FROM account_state WHERE account_id = $1::uuid`, w.account)
	validate(t, stored)
	if hist := scalar(t, `SELECT state::text FROM state_history WHERE account_id = $1::uuid AND version = 1`, w.account); hist == "" {
		t.Fatal("history must hold the state")
	}
	var st reducer.AccountState
	if err := json.Unmarshal([]byte(stored), &st); err != nil {
		t.Fatal(err)
	}
	items, _ := st.Fields.Blockers.Value.([]any)
	if !st.Fields.Blockers.Known || len(items) != 1 || st.AccountName != "Acme Corp" || st.LastActivityID == nil || *st.LastActivityID != acts[0] {
		t.Fatalf("state = %+v", st)
	}
	lci := st.Fields.LastCustomerInteraction
	if !lci.Known || !lci.Derived || lci.Value != t0.Add(-time.Hour).Format(time.RFC3339) {
		t.Fatalf("last_customer_interaction = %+v", lci)
	}
	if len(*calls) != 1 || (*calls)[0].prev != nil || (*calls)[0].versionSeenInTx != "1" || len((*calls)[0].acts) != 1 {
		t.Fatalf("hook calls = %+v", *calls)
	}
	if got := scalar(t, `SELECT (claims_status.n)::text FROM (SELECT count(*) AS n FROM claims WHERE status = 'active') claims_status`); got != "1" {
		t.Fatalf("active claims = %s", got)
	}
}

func TestBurstOfActivitiesInTheDebounceWindowIsOneRecompute(t *testing.T) {
	w := seedBasic(t)
	ingestClock := clock.NewFixed(t0)
	svc := ingestService(t, ingestClock)
	var evs []normalize.SourceEvent
	for i := 1; i <= 5; i++ {
		evs = append(evs, inbound(t, i, t0.Add(time.Duration(i)*time.Second-time.Hour), fmt.Sprintf("Blocker number %d. More text", i)))
	}
	var acts []string
	for _, ev := range evs { // one every 300 ms of wall time: all inside the 3 s debounce window
		ingestClock.Advance(300 * time.Millisecond)
		acts = append(acts, ingestAll(t, svc, ev)...)
	}
	if got := scalar(t, `SELECT count(*)::text FROM recompute_jobs`); got != "1" {
		t.Fatalf("ingest enqueued %s jobs for one burst, want 1", got)
	}

	ex := blockerExtractor()
	hook, calls := recordingHook()
	clk := clock.NewFixed(t0)
	s := newService(t, clk, coalesce.Options{Extractor: ex, Hook: hook})
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 0 {
		t.Fatalf("nothing is due inside the debounce window: %+v %v", res, err)
	}
	clk.Advance(debounce + 2*time.Second)
	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM state_history WHERE account_id = $1::uuid`, w.account); got != "1" {
		t.Fatalf("history rows = %s, want exactly one for the burst", got)
	}
	if len(*calls) != 1 || len((*calls)[0].acts) != 5 || len(ex.Calls()) != 5 {
		t.Fatalf("hook calls=%d activities=%d extractor calls=%d", len(*calls), len((*calls)[0].acts), len(ex.Calls()))
	}
	if got := scalar(t, `SELECT count(*)::text FROM claims`); got != "5" {
		t.Fatalf("claims = %s", got)
	}
	_ = acts
}

func TestRecomputeIsRepeatableWithoutDuplicatingClaimsOrCallingTheWorkerAgain(t *testing.T) {
	w := seedBasic(t)
	clk := clock.NewFixed(t0)
	acts := ingestAll(t, ingestService(t, clk), inbound(t, 1, t0.Add(-time.Hour), "Budget is tight. More"))
	ex := blockerExtractor()
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{Extractor: ex})
	if _, err := s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	insertJob(t, w.account, t0, acts, "", time.Time{}) // the same activity enqueued again
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 1 || res.Recomputes[0].Version != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM claims`); got != "1" {
		t.Fatalf("claims = %s, the dedupe constraint must hold", got)
	}
	if n := ex.CallsFor(acts[0]); n != 1 {
		t.Fatalf("worker called %d times for one activity, the cache must serve the second", n)
	}
	if got := scalar(t, `SELECT count(*)::text FROM extraction_cache`); got != "1" {
		t.Fatalf("cache rows = %s", got)
	}
	if got := scalar(t, `SELECT count(*)::text FROM state_history WHERE account_id = $1::uuid`, w.account); got != "2" {
		t.Fatalf("history = %s", got)
	}
}

func TestHookErrorRollsBackTheWholeRecomputeAndTheJobIsRetried(t *testing.T) {
	w := seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-time.Hour), "Budget is tight. More"))
	fail := true
	hook := coalesce.HookFunc(func(context.Context, *sql.Tx, *reducer.AccountState, reducer.AccountState, []string, []claims.Conflict) error {
		if fail {
			return errors.New("diff writer down")
		}
		return nil
	})
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{Extractor: blockerExtractor(), Hook: hook, RetryDelay: 30 * time.Second})

	res, err := s.Drain(context.Background())
	if err == nil || res.Failed != 1 || !strings.Contains(err.Error(), "diff writer down") {
		t.Fatalf("%+v %v", res, err)
	}
	for _, table := range []string{"account_state", "state_history", "claims"} {
		if got := scalar(t, `SELECT count(*)::text FROM `+table); got != "0" {
			t.Fatalf("%s has %s rows after a rolled-back recompute", table, got)
		}
	}
	if got := scalar(t, `SELECT last_error || '|' || attempts::text || '|' || (claimed_at IS NULL)::text FROM recompute_jobs WHERE account_id = $1::uuid`, w.account); got != "coalesce: after-recompute hook: diff writer down|1|true" {
		t.Fatalf("job = %s", got)
	}
	fail = false
	clk.Advance(time.Minute)
	if res, err := s.Drain(context.Background()); err != nil || len(res.Recomputes) != 1 || res.Recomputes[0].Version != 1 {
		t.Fatalf("retry: %+v %v", res, err)
	}
}

func TestLostLeaseAbortsTheRecomputeWithoutWritingState(t *testing.T) {
	w := seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-time.Hour), "Budget is tight. More"))
	steal := coalesce.HookFunc(func(ctx context.Context, tx *sql.Tx, _ *reducer.AccountState, _ reducer.AccountState, _ []string, _ []claims.Conflict) error {
		_, err := tx.ExecContext(ctx, `UPDATE recompute_jobs SET claimed_by = 'someone-else'`)
		return err
	})
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{Extractor: blockerExtractor(), Hook: steal, WorkerID: "w1"})
	_, err := s.Drain(context.Background())
	if !errors.Is(err, coalesce.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM account_state WHERE account_id = $1::uuid`, w.account); got != "0" {
		t.Fatal("state written despite a lost lease")
	}
}

func TestPersistStateRejectsAStaleVersionAndADuplicateFirstState(t *testing.T) {
	w := seedBasic(t)
	ctx := context.Background()
	mk := func(version int) reducer.AccountState {
		st, _ := reducer.Reduce(reducer.Input{AccountID: w.account, AccountName: "Acme Corp", Version: version, ComputedAt: t0, People: map[string]reducer.Person{}})
		return st
	}
	run := func(prev *reducer.AccountState, st reducer.AccountState) error {
		tx, err := env.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := coalesce.PersistState(ctx, tx, prev, st, nil); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := run(nil, mk(1)); err != nil {
		t.Fatal(err)
	}
	if err := run(nil, mk(1)); !errors.Is(err, coalesce.ErrVersionConflict) && err == nil {
		t.Fatal("a second 'first' state must conflict")
	}
	first := mk(1)
	stale := mk(5)
	if err := run(&stale, mk(6)); !errors.Is(err, coalesce.ErrVersionConflict) {
		t.Fatalf("a stale previous version must conflict, got %v", err)
	}
	if err := run(&first, mk(2)); err != nil {
		t.Fatalf("matching previous version: %v", err)
	}
}

func TestNewValidatesOptionsAndAppliesDefaults(t *testing.T) {
	if _, err := coalesce.New(nil, coalesce.Options{}); err == nil {
		t.Fatal("nil db accepted")
	}
	if _, err := coalesce.New(env.DB, coalesce.Options{Lease: -time.Second}); err == nil {
		t.Fatal("negative lease accepted")
	}
	if _, err := coalesce.New(env.DB, coalesce.Options{}); err != nil {
		t.Fatalf("zero options must be valid: %v", err)
	}
}

func TestDrainStopsOnCancelledContextAndRunRejectsBadPoll(t *testing.T) {
	seedBasic(t)
	s := newService(t, clock.NewFixed(t0), coalesce.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Run(context.Background(), 0); err == nil {
		t.Fatal("zero poll interval accepted")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx, 20*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run must return the context error, got %v", err)
	}
}

func TestPGTextArrayParsing(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{"{a,b}", "a,b"}, {[]byte("{x}"), "x"}, {"{}", ""}, {nil, ""}, {[]string{"p", "q"}, "p,q"}, {[]any{"m", 3}, "m,3"}, {`{"a","b"}`, "a,b"},
	} {
		got, err := coalesce.ParsePGTextArray(tc.in)
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Errorf("%v -> %v %v, want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []any{"nope", 42, "{"} {
		if _, err := coalesce.ParsePGTextArray(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
