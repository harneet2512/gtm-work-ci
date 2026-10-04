package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

const soc2Email = "We need the SOC2 report before signing. Thanks"

func TestMaterialEmailWritesDiffSignalsEvaluationAndADryRun(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	acts := ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))

	if count(t, "state_diffs") != "1" || scalar(t, `SELECT is_material::text FROM state_diffs`) != "true" {
		t.Fatal("one material diff expected")
	}
	types := scalar(t, `SELECT string_agg(signal_type, ',' ORDER BY signal_type) FROM signals`)
	for _, want := range []string{"customer_replied", "security_blocker_appeared"} {
		if !strings.Contains(types, want) {
			t.Errorf("signals = %s, missing %s", types, want)
		}
	}
	if got := scalar(t, `SELECT eligible::text || ' ' || reason_codes::text FROM trigger_evaluations`); got != "true {eligible_blocker_change,eligible_customer_replied}" {
		t.Fatalf("evaluation = %s", got)
	}
	run := onlyRun(t)
	if got := scalar(t, `SELECT run_mode || ' ' || status || ' ' || state_version::text FROM agent_runs`); got != "dry_run pending 1" {
		t.Fatalf("run = %s", got)
	}
	if got := scalar(t, `SELECT string_agg(step || ':' || status, ',' ORDER BY seq) FROM agent_run_steps WHERE agent_run_id = $1::uuid`, run); got !=
		"build_context:pending,draft:pending,crm_intent:pending,await_human:pending,execute:pending" {
		t.Fatalf("steps = %s", got)
	}
	if got := scalar(t, `SELECT trigger_activity_ids::text FROM agent_runs`); got != "{"+acts[0]+"}" {
		t.Fatalf("trigger activities = %s", got)
	}
}

func TestRunTraceLeadsBackToTheActivitiesAndTheState(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	acts := ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	run := onlyRun(t)

	tr, err := runs.LoadTrace(context.Background(), env.DB, run)
	if err != nil {
		t.Fatal(err)
	}
	if tr.StateVersion == nil || *tr.StateVersion != 1 || tr.Mode != runs.DryRun || tr.Diff == nil || !tr.Diff.IsMaterial {
		t.Fatalf("trace = %+v", tr)
	}
	if len(tr.Activities) != 1 || tr.Activities[0].ID != acts[0] || len(tr.Diff.ActivityIDs) != 1 || tr.Diff.ActivityIDs[0] != acts[0] {
		t.Fatalf("activities = %+v diff activities = %v", tr.Activities, tr.Diff.ActivityIDs)
	}
	if len(tr.Evaluation.ReasonCodes) != 2 || tr.Evaluation.ReasonCodes[1] != "eligible_customer_replied" || len(tr.Signals) < 2 || len(tr.Steps) != 5 {
		t.Fatalf("evaluation = %+v, %d signals, %d steps", tr.Evaluation, len(tr.Signals), len(tr.Steps))
	}
	found := false
	for _, r := range tr.EvidenceRefs {
		found = found || r.ActivityID == acts[0]
	}
	if !found {
		t.Fatalf("the trace must cite the trigger activity as evidence: %+v", tr.EvidenceRefs)
	}
	// the state version the run read is the stored history row
	if got := scalar(t, `SELECT version::text FROM state_history WHERE account_id = $1::uuid`, tr.AccountID); got != "1" {
		t.Fatalf("history version %s", got)
	}
}

func TestBurstIsOneDiffOneEvaluationAndOneRun(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	for i := 1; i <= 5; i++ { // five emails inside the debounce window: one coalesced job
		s.ingestClock.Advance(300 * time.Millisecond)
		ingestAll(t, s.svc, email(t, i, t0.Add(time.Duration(i)*time.Second-time.Hour), "inbound", fmt.Sprintf("We need item %d. More", i)))
	}
	s.drain(t, t0.Add(debounce+2*time.Second))
	for table, want := range map[string]string{"state_diffs": "1", "trigger_evaluations": "1", "agent_runs": "1"} {
		if got := count(t, table); got != want {
			t.Errorf("%s = %s, want %s for one burst", table, got, want)
		}
	}
	if got := scalar(t, `SELECT cardinality(trigger_activity_ids)::text FROM agent_runs`); got != "5" {
		t.Fatalf("the run must carry all five trigger activities, has %s", got)
	}
}

func TestNonMaterialChangeIsIneligibleWithAReasonAndNoRun(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-2*time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	// a bare reply that asks for nothing and introduces nobody changes only last_customer_interaction
	later := t0.Add(time.Hour)
	s.ingestClock.Set(later)
	ingestAll(t, s.svc, email(t, 2, later, "inbound", "Thanks for the call. Talk soon"))
	s.drain(t, later.Add(time.Minute))
	if got := scalar(t, `SELECT is_material::text FROM state_diffs WHERE to_version = 2`); got != "false" {
		t.Fatalf("a bare reply changes nothing material, got is_material=%s: %s", got, scalar(t, `SELECT changes::text FROM state_diffs WHERE to_version = 2`))
	}
	got := scalar(t, `SELECT eligible::text || ' ' || reason_codes::text || ' ' || explanation FROM trigger_evaluations ORDER BY evaluated_at DESC LIMIT 1`)
	if !strings.HasPrefix(got, "false {no_material_change} state changed but no action needed") {
		t.Fatalf("evaluation = %s", got)
	}
	if count(t, "agent_runs") != "1" {
		t.Fatal("an ineligible evaluation must not create a run (only the first, material burst has one)")
	}
	if got := scalar(t, `SELECT count(*)::text FROM signals WHERE state_diff_id = (SELECT id FROM state_diffs WHERE to_version = 2)`); got != "1" {
		t.Fatalf("the bare reply still emits its customer_replied signal, got %s signals", got)
	}
}

// HAR-129 multi-event replay: a finished (recorded) dry run frees the account, so two eligible events on one
// account both get a run (ADR-0015).
func TestFinishedDryRunDoesNotBlockTheNextEligibleEvent(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	first := onlyRun(t)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'drafted'`); err != nil {
		t.Fatal(err)
	}
	ex, _ := runs.NewExecutor(runs.DryRun, nil)
	if _, err := runs.ExecuteStep(context.Background(), env.DB, ex, first, runs.Effect{Kind: "send_email", Target: "priya.shah@acme.com"}); err != nil {
		t.Fatal(err)
	}
	s.ingestClock.Set(t0.Add(2 * time.Hour))
	ingestAll(t, s.svc, email(t, 2, t0.Add(2*time.Hour), "inbound", "We need the pen-test summary too. Thanks"))
	s.drain(t, t0.Add(2*time.Hour+time.Minute))
	if got := scalar(t, `SELECT string_agg(eligible::text, ',' ORDER BY evaluated_at) FROM trigger_evaluations`); got != "true,true" {
		t.Fatalf("both events must be eligible, got %s", got)
	}
	if got := scalar(t, `SELECT string_agg(status, ',' ORDER BY created_at) FROM agent_runs`); got != "recorded,pending" {
		t.Fatalf("runs = %s", got)
	}
}

func TestOpenRunBlocksTheNextOne(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	s.ingestClock.Set(t0.Add(2 * time.Hour))
	ingestAll(t, s.svc, email(t, 2, t0.Add(2*time.Hour), "inbound", "We need the pen-test summary too. Thanks"))
	s.drain(t, t0.Add(2*time.Hour+time.Minute))
	if got := scalar(t, `SELECT string_agg(eligible::text || ':' || reason_codes::text, ' ' ORDER BY evaluated_at) FROM trigger_evaluations`); got != "true:{eligible_blocker_change,eligible_customer_replied} false:{open_run_exists}" {
		t.Fatalf("evaluations = %s", got)
	}
	if count(t, "agent_runs") != "1" {
		t.Fatal("the second burst must not create a second run")
	}
}

func TestRepsOwnEmailIsRepAlreadyReplied(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc,
		email(t, 1, t0.Add(-2*time.Hour), "inbound", soc2Email),
		email(t, 2, t0.Add(-time.Hour), "outbound", "Sending the SOC2 report now. Dana"))
	s.drain(t, t0.Add(time.Minute))
	if got := scalar(t, `SELECT eligible::text || ':' || reason_codes::text FROM trigger_evaluations`); got != "false:{rep_already_replied}" {
		t.Fatalf("evaluation = %s", got)
	}
}

// A retried recompute (hook called again for a version already processed) writes nothing a second time.
func TestRetriedHookWritesNothingTwice(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	acts := ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	before := fmt.Sprint(count(t, "state_diffs"), count(t, "signals"), count(t, "trigger_evaluations"), count(t, "agent_runs"))

	var next reducer.AccountState
	raw := scalar(t, `SELECT state::text FROM state_history WHERE version = 1`)
	if err := json.Unmarshal([]byte(raw), &next); err != nil {
		t.Fatal(err)
	}
	tx, err := env.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	p := newPipeline(t, runs.DryRun)
	if err := p.AfterRecompute(context.Background(), tx, nil, next, acts, []claims.Conflict(nil)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if after := fmt.Sprint(count(t, "state_diffs"), count(t, "signals"), count(t, "trigger_evaluations"), count(t, "agent_runs")); after != before {
		t.Fatalf("rows changed on retry: %s -> %s", before, after)
	}
}

func TestStoredDiffAndSignalsConformToTheContracts(t *testing.T) {
	seedWorld(t)
	s := newStack(t, runs.DryRun)
	ingestAll(t, s.svc, email(t, 1, t0.Add(-time.Hour), "inbound", soc2Email))
	s.drain(t, t0.Add(time.Minute))
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	diff := scalar(t, `SELECT jsonb_build_object('id', id, 'account_id', account_id, 'from_version', from_version, 'to_version', to_version,
		'is_material', is_material, 'changes', changes, 'activity_ids', to_jsonb(activity_ids), 'created_at', to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'))::text FROM state_diffs`)
	if err := v.Validate("state_diff", []byte(diff)); err != nil {
		t.Fatalf("stored diff violates state_diff.v1.json: %v\n%s", err, diff)
	}
	rows, err := env.DB.Query(`SELECT jsonb_strip_nulls(jsonb_build_object('id', id, 'account_id', account_id, 'opportunity_id', opportunity_id,
		'signal_type', signal_type, 'state_diff_id', state_diff_id, 'subject_person_id', subject_person_id, 'subject_claim_id', subject_claim_id,
		'occurred_at', to_char(occurred_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		'expires_at', CASE WHEN expires_at IS NULL THEN NULL ELSE to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') END,
		'dedupe_key', dedupe_key, 'rule', rule, 'details', details, 'evidence_refs', evidence_refs,
		'created_at', to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))::text FROM signals`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		n++
		if err := v.Validate("signal", []byte(raw)); err != nil {
			t.Fatalf("stored signal violates signal.v1.json: %v\n%s", err, raw)
		}
	}
	if n < 2 {
		t.Fatalf("validated %d signals", n)
	}
}
