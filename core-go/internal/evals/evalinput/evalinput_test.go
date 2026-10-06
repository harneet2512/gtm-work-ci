package evalinput_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func TestDefaultsAndNormalizeGiveTheUnconfiguredWorkspace(t *testing.T) {
	d := evalinput.Defaults("ws-1")
	if d.WorkspaceID != "ws-1" || d.Policy.AutonomyLevel != "customer_facing" || len(d.Policy.AllowedTools) != 6 {
		t.Fatalf("defaults = %+v", d)
	}
	if len(d.CRM.Stages) == 0 || d.CRM.MaxForwardSteps != 1 {
		t.Fatalf("default crm = %+v", d.CRM)
	}

	// An empty Params normalizes to the defaults of the named workspace.
	got := evalinput.Params{}.Normalize("ws-2")
	if got.WorkspaceID != "ws-2" || got.Policy.WorkspaceID != "ws-2" || len(got.Policy.AllowedTools) == 0 || len(got.CRM.Stages) == 0 {
		t.Fatalf("normalized empty params = %+v", got)
	}
	// A configured policy survives; only the missing parts are filled.
	custom := evalinput.Params{WorkspaceID: "ws-3",
		Policy: deterministic.Policy{AutonomyLevel: "assist", AllowedTools: []string{"email.send"}}}
	got = custom.Normalize("ignored")
	if got.WorkspaceID != "ws-3" || got.Policy.AutonomyLevel != "assist" || len(got.Policy.AllowedTools) != 1 ||
		got.Policy.WorkspaceID != "ws-3" || len(got.CRM.Stages) == 0 {
		t.Fatalf("normalized partial params = %+v", got)
	}
}

func TestReplayClockIsTheNewestTriggerActivity(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	ctx := context.Background()

	var want time.Time
	if err := env.DB.QueryRowContext(ctx,
		`SELECT occurred_at FROM activities WHERE id = $1::uuid`, w.TriggerA).Scan(&want); err != nil {
		t.Fatal(err)
	}
	at, err := evalinput.ReplayClock(ctx, env.DB, []string{w.TriggerA})
	if err != nil || !at.Equal(want.UTC()) {
		t.Fatalf("replay clock = %v, %v; want %v", at, err, want)
	}
	// A missing trigger activity means no replay clock.
	if _, err := evalinput.ReplayClock(ctx, env.DB, []string{"99999999-9999-4999-8999-999999999999"}); err == nil {
		t.Fatal("a run without trigger activities must have no replay clock")
	}
}

func TestAssembleReadsTheWorldTheEvalsRead(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	ctx := context.Background()

	at, err := evalinput.ReplayClock(ctx, env.DB, []string{w.TriggerA})
	if err != nil {
		t.Fatal(err)
	}
	// One prior send inside the duplicate window, addressed to a person of the account.
	person := scalar(t, `SELECT id::text FROM people WHERE account_id = $1::uuid AND primary_email IS NOT NULL LIMIT 1`, w.AccountA)
	sent := scalar(t, `WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
  VALUES ('email', 'evalinput-prior', 'sent', encode(sha256(convert_to('evalinput-prior'::text, 'UTF8')), 'hex'), '{}'::jsonb, $2) RETURNING id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance, summary)
SELECT id, 'EmailSent', 'email', 'evalinput-prior', $2, $1::uuid,
       '{"source_system":"email","source_object_id":"evalinput-prior"}'::jsonb, 'Earlier note' FROM se RETURNING id::text`,
		w.AccountA, at.Add(-time.Hour))
	exec(t, `INSERT INTO activity_participants (activity_id, raw_identity, role, person_id) VALUES ($1::uuid, 'x@example.test', 'to', $2::uuid)`,
		sent, person)

	var runID, mode string
	var opp *string
	if err := env.DB.QueryRowContext(ctx,
		`SELECT id::text, run_mode, opportunity_id::text FROM agent_runs WHERE id = $1::uuid`, w.RunA).Scan(&runID, &mode, &opp); err != nil {
		t.Fatal(err)
	}
	exec(t, `INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status, started_at, finished_at)
VALUES ($1::uuid, 2, 'draft', 'dry_run', 'succeeded', $2::timestamptz, $2::timestamptz + interval '1 second'),
       ($1::uuid, 1, 'build_context', 'dry_run', 'succeeded', $2::timestamptz, $2::timestamptz)`,
		w.RunA, at.Add(-2*time.Hour))

	st, found, err := evalinput.State(ctx, env.DB, w.AccountA, at)
	if err != nil || !found || st.Version < 1 {
		t.Fatalf("state at replay clock = found %v version %d err %v", found, st.Version, err)
	}

	in, err := evalinput.Assemble(ctx, env.DB,
		evalinput.Run{ID: w.RunA, AccountID: w.AccountA, Mode: mode, OpportunityID: deref(opp), TriggerIDs: []string{w.TriggerA}},
		deterministic.Output{ProposedActionType: "send_email",
			FinishedArtifact: deterministic.Artifact{Channel: "email", Body: "b"},
			EvidenceRefs:     []deterministic.EvidenceRef{{ActivityID: w.TriggerA}}},
		2, at, st, evalinput.Params{WorkspaceID: "ws-demo"})
	if err != nil {
		t.Fatal(err)
	}
	if in.AgentRunID != w.RunA || in.DraftIndex != 2 || in.WorkspaceID != "ws-demo" ||
		in.RunMode != "dry_run" || in.ExecuteMode != "record_only" || !in.EvaluatedAt.Equal(at) {
		t.Fatalf("input header = %+v", in)
	}
	if len(in.RunSteps) != 2 || in.RunSteps[0].Step != "build_context" || in.RunSteps[1].Step != "draft" {
		t.Fatalf("steps must come back ordered by seq: %+v", in.RunSteps)
	}
	var sawTrigger, sawPrior bool
	for _, a := range in.Activities {
		if a.ActivityID == w.TriggerA {
			sawTrigger = true
		}
	}
	for _, p := range in.PriorActions {
		if p.RefID == sent {
			sawPrior = true
			if len(p.RecipientPersonIDs) != 1 || p.RecipientPersonIDs[0] != person || p.Action != "send_email" {
				t.Fatalf("prior send = %+v", p)
			}
		}
	}
	if !sawTrigger {
		t.Fatal("the cited trigger activity was not loaded")
	}
	if !sawPrior {
		t.Fatal("the EmailSent inside the window was not read as a prior action")
	}
	if len(in.People) == 0 || len(in.Opportunities) == 0 {
		t.Fatalf("the account's directory must be loaded: people %d opportunities %d", len(in.People), len(in.Opportunities))
	}
	if in.Assets == nil || in.Commercial.Catalog == nil {
		t.Fatal("unstored inputs must be empty lists, not null")
	}
	// The defaults flow through Normalize, so a partial Params still evaluates.
	if len(in.Policy.AllowedTools) == 0 || len(in.CRM.Stages) == 0 {
		t.Fatal("a Params without policy/crm must evaluate with the workspace defaults")
	}
}

func TestPriorSendsStaysInsideTheWindow(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	at, err := evalinput.ReplayClock(context.Background(), env.DB, []string{w.TriggerB})
	if err != nil {
		t.Fatal(err)
	}
	// B's account: a send just outside the duplicate window must not be read back.
	old := scalar(t, `WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
  VALUES ('email', 'evalinput-old', 'sent', encode(sha256(convert_to('evalinput-old'::text, 'UTF8')), 'hex'), '{}'::jsonb, $2) RETURNING id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
SELECT id, 'EmailSent', 'email', 'evalinput-old', $2, $1::uuid, '{"source_system":"email","source_object_id":"evalinput-old"}'::jsonb FROM se RETURNING id::text`,
		w.AccountB, at.Add(-deterministic.DuplicateWindow-time.Hour))
	priors, err := evalinput.PriorSends(context.Background(), env.DB, w.AccountB, at)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range priors {
		if p.RefID == old {
			t.Fatal("a send older than the duplicate window leaked into the input")
		}
	}
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

func exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := env.DB.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
