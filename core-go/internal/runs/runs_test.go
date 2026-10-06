package runs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

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

type seed struct{ account, activity string }

// seedRun creates an account, one activity and n eligible evaluations.
func seedEvaluations(t *testing.T, n int) (seed, []string) {
	t.Helper()
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE agent_runs, trigger_evaluations, activities, source_events, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	s := seed{}
	s.account = scalar(t, `INSERT INTO accounts (name) VALUES ('Acme') RETURNING id::text`)
	ev := scalar(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', 'm1', 'received', repeat('a', 64), '{}') RETURNING id::text`)
	s.activity = scalar(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', 'm1', now(), $2::uuid, '{"source_system":"email","source_object_id":"m1"}') RETURNING id::text`, ev, s.account)
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, scalar(t, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes)
			VALUES ($1::uuid, 'post_interaction_followup', true, '{eligible_customer_replied}') RETURNING id::text`, s.account))
	}
	return s, ids
}

func newRun(s seed, eval, mode string) runs.New {
	return runs.New{AccountID: s.account, Mode: mode, EvaluationID: eval, TriggerActivityIDs: []string{s.activity}, StateVersion: 1}
}

type fakeSender struct {
	calls int
	err   error
	last  runs.Effect
}

func (f *fakeSender) Send(_ context.Context, e runs.Effect) (string, error) {
	f.calls++
	f.last = e
	return "msg-1", f.err
}

func TestCreateIsIdempotentPerEvaluationAndOneOpenRunPerAccount(t *testing.T) {
	s, evals := seedEvaluations(t, 2)
	ctx := context.Background()
	id, created, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
	if err != nil || !created || id == "" {
		t.Fatalf("create: %v %v", created, err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM agent_run_steps WHERE agent_run_id = $1::uuid`, id); n != "5" {
		t.Fatalf("steps = %s", n)
	}
	again, created, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
	if err != nil || created || again != id {
		t.Fatalf("same evaluation must return its run: %v %v %v", again, created, err)
	}
	if _, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.Live)); err == nil {
		t.Fatal("an evaluation's run in another mode must be an error")
	}
	if _, _, err := runs.Create(ctx, env.DB, newRun(s, evals[1], runs.DryRun)); !errors.Is(err, runs.ErrOpenRun) {
		t.Fatalf("a second open run must be ErrOpenRun, got %v", err)
	}
}

func TestCreateValidatesItsInput(t *testing.T) {
	ctx := context.Background()
	if _, _, err := runs.Create(ctx, env.DB, runs.New{Mode: "staging", TriggerActivityIDs: []string{"a"}}); err == nil {
		t.Fatal("unknown mode")
	}
	if _, _, err := runs.Create(ctx, env.DB, runs.New{Mode: runs.DryRun}); err == nil {
		t.Fatal("no trigger activities")
	}
}

func TestDryRunEndsRecordedAndCannotBeExecutedTwice(t *testing.T) {
	s, evals := seedEvaluations(t, 1)
	ctx := context.Background()
	id, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
	if err != nil {
		t.Fatal(err)
	}
	ex, _ := runs.NewExecutor(runs.DryRun, &fakeSender{})
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'drafted' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	res, err := runs.ExecuteStep(ctx, env.DB, ex, id, runs.Effect{Kind: "send_email", Target: "a@b.c"})
	if err != nil || res.Status != "recorded" {
		t.Fatalf("res %+v err %v", res, err)
	}
	if got := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, id); got != "recorded" {
		t.Fatalf("run status = %s", got)
	}
	if _, err := runs.ExecuteStep(ctx, env.DB, ex, id, runs.Effect{}); !errors.Is(err, runs.ErrNotRecordable) {
		t.Fatalf("second execute: %v", err)
	}
	if _, err := runs.ExecuteStep(ctx, env.DB, ex, "11111111-1111-4111-8111-111111111111", runs.Effect{}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing run: %v", err)
	}
}

func TestLiveSendFailureCanBeRetriedAndSuccessIsFinal(t *testing.T) {
	s, evals := seedEvaluations(t, 1)
	ctx := context.Background()
	id, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.Live))
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeSender{err: errors.New("smtp down")}
	live, err := runs.NewExecutor(runs.Live, sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.ExecuteStep(ctx, env.DB, live, id, runs.Effect{Kind: "send_email"}); !errors.Is(err, runs.ErrNotApproved) {
		t.Fatalf("unapproved: %v", err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'edited' WHERE id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.ExecuteStep(ctx, env.DB, live, id, runs.Effect{Kind: "send_email"}); err == nil {
		t.Fatal("the failed send must be returned")
	}
	if got := scalar(t, `SELECT status FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute'`, id); got != "failed" {
		t.Fatalf("step = %s", got)
	}
	sender.err = nil
	res, err := runs.ExecuteStep(ctx, env.DB, live, id, runs.Effect{Kind: "send_email"})
	if err != nil || res.ExternalEffectID != "msg-1" || sender.calls != 2 {
		t.Fatalf("retry: %+v %v calls=%d", res, err, sender.calls)
	}
	if sender.last.IdempotencyKey != id+":execute" {
		t.Fatalf("the send must carry an idempotency key, got %q", sender.last.IdempotencyKey)
	}
	if got := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, id); got != "executed" {
		t.Fatalf("run = %s", got)
	}
	if _, err := runs.ExecuteStep(ctx, env.DB, live, id, runs.Effect{}); !errors.Is(err, runs.ErrAlreadyExecuted) && !errors.Is(err, runs.ErrNotApproved) {
		t.Fatalf("a sent run must not send again: %v", err)
	}
	if sender.calls != 2 {
		t.Fatal("the connector was called again")
	}
}

func TestLoadTraceOfARunWithoutADiff(t *testing.T) {
	s, evals := seedEvaluations(t, 1)
	ctx := context.Background()
	id, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO context_access_log (agent_run_id, tool, bytes) VALUES ($1::uuid, 'state', 42)`, id); err != nil {
		t.Fatal(err)
	}
	tr, err := runs.LoadTrace(ctx, env.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Diff != nil || tr.StateVersion == nil || *tr.StateVersion != 1 || len(tr.Steps) != 5 || len(tr.Activities) != 1 ||
		len(tr.ContextRefs) != 1 || tr.ContextRefs[0].Bytes != 42 || len(tr.Evaluation.ReasonCodes) != 1 {
		t.Fatalf("trace = %+v", tr)
	}
	if _, err := runs.LoadTrace(ctx, env.DB, "11111111-1111-4111-8111-111111111111"); err == nil {
		t.Fatal("a missing run is an error")
	}
}

func TestRecordingExecutorKeepsACopyAndPerformsNoIO(t *testing.T) {
	ex := &runs.RecordingExecutor{}
	if _, err := ex.Execute(context.Background(), runs.Effect{Kind: "crm_next_step", Target: "opp"}); err != nil {
		t.Fatal(err)
	}
	got := ex.Recorded()
	got[0].Kind = "mutated"
	if ex.Recorded()[0].Kind != "crm_next_step" || ex.Mode() != runs.DryRun {
		t.Fatal("Recorded must return a copy")
	}
}

// A dry run is recorded only from drafted, approved or edited. Every other status (nothing drafted yet, no human
// decision yet, or a final decision) is refused and left untouched, so recording never overwrites the human
// decision HAR-97 learns from (ADR-0015).
func TestDryRunIsRecordedOnlyFromDraftedApprovedOrEdited(t *testing.T) {
	ctx := context.Background()
	ex, _ := runs.NewExecutor(runs.DryRun, nil)
	for _, c := range []struct {
		status string
		ok     bool
	}{
		{"pending", false}, {"context_built", false}, {"awaiting_human", false},
		{"rejected", false}, {"ignored", false}, {"cancelled", false}, {"failed", false}, {"recorded", false},
		{"drafted", true}, {"approved", true}, {"edited", true},
	} {
		s, evals := seedEvaluations(t, 1)
		id, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.DB.Exec(`UPDATE agent_runs SET status = $2 WHERE id = $1::uuid`, id, c.status); err != nil {
			t.Fatal(err)
		}
		_, err = runs.ExecuteStep(ctx, env.DB, ex, id, runs.Effect{Kind: "send_email"})
		gotStatus := scalar(t, `SELECT status FROM agent_runs WHERE id = $1::uuid`, id)
		step := scalar(t, `SELECT status FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute'`, id)
		if c.ok {
			if err != nil || gotStatus != "recorded" || step != "recorded" {
				t.Errorf("%s: err=%v run=%s step=%s", c.status, err, gotStatus, step)
			}
			continue
		}
		if !errors.Is(err, runs.ErrNotRecordable) || gotStatus != c.status || step != "pending" {
			t.Errorf("%s must be refused and untouched: err=%v run=%s step=%s", c.status, err, gotStatus, step)
		}
	}
}

// loadDiff, loadSignals and collectEvidence: a run whose evaluation links a diff and signals traces to them.
func TestLoadTraceFollowsTheDiffAndSignalsAndCollectsEvidenceOnce(t *testing.T) {
	s, evals := seedEvaluations(t, 1)
	ctx := context.Background()
	for v := 1; v <= 1; v++ {
		if _, err := env.DB.Exec(`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, $2, now(), '{}')`, s.account, v); err != nil {
			t.Fatal(err)
		}
	}
	diff := scalar(t, `INSERT INTO state_diffs (account_id, from_version, to_version, is_material, changes, activity_ids)
		VALUES ($1::uuid, 0, 1, true, $2::jsonb, ARRAY[$3]::uuid[]) RETURNING id::text`, s.account,
		`[{"field":"blockers","op":"set","material":true,"evidence_refs":[{"activity_id":"`+s.activity+`","occurred_at":"2026-09-29T12:00:00Z"}]}]`, s.activity)
	refs := `[{"activity_id":"` + s.activity + `","occurred_at":"2026-09-29T12:00:00Z"},{"activity_id":"` + s.activity + `","claim_id":"c1","occurred_at":"2026-09-29T12:00:00Z"}]`
	sig := scalar(t, `INSERT INTO signals (account_id, signal_type, state_diff_id, rule, evidence_refs, occurred_at)
		VALUES ($1::uuid, 'customer_replied', $2::uuid, 'sig.customer_replied@1', $3::jsonb, '2026-09-29T12:00:00Z') RETURNING id::text`, s.account, diff, refs)
	if _, err := env.DB.Exec(`UPDATE trigger_evaluations SET state_diff_id = $2::uuid, signal_ids = ARRAY[$3]::uuid[] WHERE id = $1::uuid`, evals[0], diff, sig); err != nil {
		t.Fatal(err)
	}
	id, _, err := runs.Create(ctx, env.DB, newRun(s, evals[0], runs.DryRun))
	if err != nil {
		t.Fatal(err)
	}
	tr, err := runs.LoadTrace(ctx, env.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Diff == nil || tr.Diff.ID != diff || !tr.Diff.IsMaterial || tr.Diff.ToVersion != 1 || len(tr.Diff.ActivityIDs) != 1 {
		t.Fatalf("diff = %+v", tr.Diff)
	}
	if len(tr.Signals) != 1 || tr.Signals[0].Type != "customer_replied" || len(tr.Signals[0].EvidenceRefs) != 2 {
		t.Fatalf("signals = %+v", tr.Signals)
	}
	// the activity cited by the diff and by both signal refs; the claim-qualified ref is distinct: two entries
	if len(tr.EvidenceRefs) != 2 {
		t.Fatalf("evidence must be deduplicated by activity and claim: %+v", tr.EvidenceRefs)
	}
}
