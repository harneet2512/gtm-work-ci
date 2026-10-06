package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/runs"
)

// tripwire is a Sender that fails the test if anything reaches it.
type tripwire struct {
	t     *testing.T
	calls atomic.Int32
	fail  bool
}

func (w *tripwire) Send(_ context.Context, e runs.Effect) (string, error) {
	w.calls.Add(1)
	if w.fail {
		return "", errors.New("connector unavailable")
	}
	return "gmail:msg-1", nil
}

var sendEmail = runs.Effect{Kind: "send_email", Target: "priya.shah@acme.com", Body: []byte(`{"subject":"SOC2","body":"attached"}`)}

func dryRunWithDraft(t *testing.T) string {
	t.Helper()
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	run := onlyRun(t)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'drafted'`); err != nil { // the draft is recorded
		t.Fatal(err)
	}
	return run
}

// HAR-106 acceptance: a dry run cannot write externally. Three independent layers are proven here:
// the dry-run executor holds no sender, an executor of the wrong mode is refused, and the database
// itself rejects an external effect or an executed status on a dry run.
func TestDryRunCannotWriteExternally(t *testing.T) {
	run := dryRunWithDraft(t)
	sender := &tripwire{t: t}
	ex, err := runs.NewExecutor(runs.DryRun, sender) // the sender is offered and must be ignored
	if err != nil {
		t.Fatal(err)
	}
	res, err := runs.ExecuteStep(context.Background(), env.DB, ex, run, sendEmail)
	if err != nil || res.Status != "recorded" || res.ExternalEffectID != "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if sender.calls.Load() != 0 {
		t.Fatal("a dry run reached the sender")
	}
	rec := ex.(*runs.RecordingExecutor).Recorded()
	if len(rec) != 1 || rec[0].Target != "priya.shah@acme.com" {
		t.Fatalf("recorded = %+v", rec)
	}
	if got := scalar(t, `SELECT status || ':' || COALESCE(external_effect_id, 'none') FROM agent_run_steps WHERE step = 'execute'`); got != "recorded:none" {
		t.Fatalf("execute step = %s", got)
	}
	if got := scalar(t, `SELECT status FROM agent_runs`); got != "recorded" {
		t.Fatalf("a dry run ends recorded (never executed), got %s", got)
	}
	if got := scalar(t, `SELECT detail::text FROM agent_run_steps WHERE step = 'execute'`); !strings.Contains(got, "recorded_effect") {
		t.Fatalf("the step records what would have been sent: %s", got)
	}
}

func TestLiveExecutorIsRefusedOnADryRun(t *testing.T) {
	run := dryRunWithDraft(t)
	sender := &tripwire{t: t}
	live, err := runs.NewExecutor(runs.Live, sender)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runs.ExecuteStep(context.Background(), env.DB, live, run, sendEmail); !errors.Is(err, runs.ErrModeMismatch) {
		t.Fatalf("err = %v", err)
	}
	if sender.calls.Load() != 0 {
		t.Fatal("the live sender was called for a dry run")
	}
}

func TestDatabaseRejectsExternalEffectsOnADryRun(t *testing.T) {
	dryRunWithDraft(t)
	for _, q := range []string{
		`UPDATE agent_run_steps SET status = 'succeeded', external_effect_id = 'gmail:msg-1' WHERE step = 'execute'`,
		`UPDATE agent_run_steps SET external_effect_id = 'gmail:msg-1' WHERE step = 'execute'`,
		`UPDATE agent_run_steps SET status = 'succeeded' WHERE step = 'execute'`,
		`UPDATE agent_run_steps SET detail = '{"external_effect": {"id": "x"}}' WHERE step = 'execute'`,
		`UPDATE agent_runs SET status = 'executed'`,
	} {
		if _, err := env.DB.Exec(q); err == nil || !strings.Contains(err.Error(), "23514") {
			t.Errorf("the database must reject (check violation): %s -> %v", q, err)
		}
	}
}

func TestLiveRunExecutesOnlyAfterApproval(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.Live)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	run := onlyRun(t)
	sender := &tripwire{t: t}
	live, _ := runs.NewExecutor(runs.Live, sender)

	if _, err := runs.ExecuteStep(context.Background(), env.DB, live, run, sendEmail); !errors.Is(err, runs.ErrNotApproved) || sender.calls.Load() != 0 {
		t.Fatalf("an unapproved live run must not execute: %v (calls %d)", err, sender.calls.Load())
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'approved'`); err != nil {
		t.Fatal(err)
	}
	res, err := runs.ExecuteStep(context.Background(), env.DB, live, run, sendEmail)
	if err != nil || res.ExternalEffectID != "gmail:msg-1" || sender.calls.Load() != 1 {
		t.Fatalf("res = %+v err = %v calls = %d", res, err, sender.calls.Load())
	}
	if got := scalar(t, `SELECT status FROM agent_runs`); got != "executed" {
		t.Fatalf("run status = %s", got)
	}
	if got := scalar(t, `SELECT status || ':' || external_effect_id FROM agent_run_steps WHERE step = 'execute'`); got != "succeeded:gmail:msg-1" {
		t.Fatalf("execute step = %s", got)
	}
}

func TestLiveFailureIsRecordedAndReturned(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.Live)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	run := onlyRun(t)
	live, _ := runs.NewExecutor(runs.Live, &tripwire{t: t, fail: true})
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'approved'`); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.ExecuteStep(context.Background(), env.DB, live, run, sendEmail); err == nil || !strings.Contains(err.Error(), "connector unavailable") {
		t.Fatalf("err = %v", err)
	}
	if got := scalar(t, `SELECT status FROM agent_run_steps WHERE step = 'execute'`); got != "failed" {
		t.Fatalf("execute step = %s", got)
	}
	if got := scalar(t, `SELECT status FROM agent_runs`); got != "approved" {
		t.Fatalf("a failed send must not mark the run executed, got %s", got)
	}
}

func TestExecutorConstruction(t *testing.T) {
	if _, err := runs.NewExecutor(runs.Live, nil); !errors.Is(err, runs.ErrLiveNeedsSender) {
		t.Fatalf("err = %v", err)
	}
	if _, err := runs.NewExecutor("staging", nil); err == nil {
		t.Fatal("unknown mode must be an error")
	}
	if _, _, err := runs.Create(context.Background(), env.DB, runs.New{Mode: "dry_run"}); err == nil {
		t.Fatal("a run without trigger activities must be an error")
	}
	if _, _, err := runs.Create(context.Background(), env.DB, runs.New{Mode: "x", TriggerActivityIDs: []string{"a"}}); err == nil {
		t.Fatal("unknown mode must be an error")
	}
}
