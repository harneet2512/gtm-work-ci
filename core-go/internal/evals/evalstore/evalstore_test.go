package evalstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// seedRun creates an account, a trigger, a run and its draft 1, and returns the run id.
func seedRun(t *testing.T) (runID, activityID string) {
	t.Helper()
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE eval_runs, agent_run_drafts, agent_runs, trigger_evaluations, activities, source_events, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	acct := scalar(t, `INSERT INTO accounts (name, domain) VALUES ('acct-1', 'acct-1.test') RETURNING id::text`)
	ev := scalar(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', 'm1', 'received', repeat('a', 64), '{}') RETURNING id::text`)
	activityID = scalar(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', 'm1', now(), $2::uuid, '{"source_system":"email","source_object_id":"m1"}') RETURNING id::text`, ev, acct)
	trig := scalar(t, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes)
		VALUES ($1::uuid, 'post_interaction_followup', true, '{eligible_customer_replied}') RETURNING id::text`, acct)
	runID = scalar(t, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
		VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', 'awaiting_human', $2::uuid, ARRAY[$3]::uuid[]) RETURNING id::text`, acct, trig, activityID)
	if _, err := env.DB.Exec(`INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($1::uuid, 1, 'account_agent', '{}')`, runID); err != nil {
		t.Fatal(err)
	}
	return runID, activityID
}

// failingInput is a draft that fails several evals and cites a real activity.
func failingInput(runID, activityID string) deterministic.Input {
	acct := "acct-1"
	body := "Total is $9,999 for 12 seats."
	return deterministic.Input{
		AgentRunID: runID, DraftIndex: 1, WorkspaceID: "ws-1", AccountID: acct, RunMode: "dry_run",
		EvaluatedAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		Draft: deterministic.Output{
			ProposedActionType: "send_email",
			Recipients:         []deterministic.Recipient{{PersonID: "p-404", Role: "to"}},
			FinishedArtifact:   deterministic.Artifact{Channel: "email", Body: body},
			EvidenceRefs:       []deterministic.EvidenceRef{{ActivityID: activityID}},
		},
		State:  reducer.AccountState{AccountID: acct},
		Policy: deterministic.Policy{WorkspaceID: "ws-1", AutonomyLevel: "customer_facing", AllowedTools: []string{"email_send"}},
	}
}

func TestSaveWritesEveryEvalResultAndIsIdempotent(t *testing.T) {
	runID, activityID := seedRun(t)
	results := deterministic.Results(deterministic.Evaluate(failingInput(runID, activityID)))
	ctx := context.Background()

	n, err := Save(ctx, env.DB, results)
	if err != nil || n != 7 {
		t.Fatalf("first save = %d, %v; want 7", n, err)
	}
	if n, err = Save(ctx, env.DB, results); err != nil || n != 0 {
		t.Fatalf("second save = %d, %v; want 0 new rows", n, err)
	}
	if got := scalar(t, `SELECT count(*)::text FROM eval_runs WHERE agent_run_id = $1::uuid AND kind = 'deterministic' AND model IS NULL`, runID); got != "7" {
		t.Fatalf("deterministic rows = %s, want 7", got)
	}
}

func TestSaveRoundTripsTheContractFields(t *testing.T) {
	runID, activityID := seedRun(t)
	results := deterministic.Results(deterministic.Evaluate(failingInput(runID, activityID)))
	if _, err := Save(context.Background(), env.DB, results); err != nil {
		t.Fatal(err)
	}
	var verdict, version, reason, correction, class string
	var blocking bool
	var stateRefs string
	err := env.DB.QueryRow(`SELECT verdict, evaluator_version, rationale, suggested_correction, evidence_class, blocking, state_refs::text
		FROM eval_runs WHERE agent_run_id = $1::uuid AND evaluator = 'recipient_correctness'`, runID).
		Scan(&verdict, &version, &reason, &correction, &class, &blocking, &stateRefs)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != "fail" || !blocking || version != "recipient_correctness:v1" || class != "product_rule" ||
		!strings.Contains(reason, "p-404") || correction == "" || !strings.Contains(stateRefs, "buying_group") {
		t.Fatalf("row = %s %s %q %q %s %v %s", verdict, version, reason, correction, class, blocking, stateRefs)
	}
}

func TestSaveStoresActivityRefsAsUUIDs(t *testing.T) {
	runID, activityID := seedRun(t)
	r := deterministic.Results(deterministic.Evaluate(failingInput(runID, activityID)))[0]
	r.ActivityRefs = []string{activityID}
	if _, err := Save(context.Background(), env.DB, []deterministic.EvalResult{r}); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT activity_refs[1]::text FROM eval_runs WHERE id = $1::uuid`, r.ID); got != activityID {
		t.Fatalf("activity_refs[1] = %s, want %s", got, activityID)
	}
}

func TestSaveRejectsAResultForAMissingDraft(t *testing.T) {
	runID, activityID := seedRun(t)
	in := failingInput(runID, activityID)
	in.DraftIndex = 5
	_, err := Save(context.Background(), env.DB, deterministic.Results(deterministic.Evaluate(in))[:1])
	if err == nil || !strings.Contains(err.Error(), "recipient_correctness") {
		t.Fatalf("want a wrapped error naming the eval, got %v", err)
	}
}

func TestSaveRejectsABlockingPass(t *testing.T) {
	runID, activityID := seedRun(t)
	r := deterministic.Results(deterministic.Evaluate(failingInput(runID, activityID)))[0]
	r.Verdict = "pass"
	if _, err := Save(context.Background(), env.DB, []deterministic.EvalResult{r}); err == nil {
		t.Fatal("eval_runs_blocking_is_fail must reject a blocking pass")
	}
}
