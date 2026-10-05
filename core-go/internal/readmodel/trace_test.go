package readmodel_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// newAccountWithActivities inserts an account and one activity per given time.
func newAccountWithActivities(t *testing.T, name string, times []time.Time) (string, []string) {
	t.Helper()
	account := scalar(t, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, name)
	ids := make([]string, len(times))
	for i, at := range times {
		obj := fmt.Sprintf("%s-%d", name, i)
		ids[i] = scalar(t, `WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', $1::text, 'received', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance, summary)
 SELECT id, 'EmailReceived', 'email', $1, $2, $3::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb, 'mail ' || $1 FROM ev
 RETURNING id::text`, obj, at, account)
	}
	return account, ids
}

// pageAll walks the whole timeline with the given page size and returns every id in order.
func pageAll(t *testing.T, account string, limit int) []string {
	t.Helper()
	var ids []string
	var before *time.Time
	for page := 0; page < 50; page++ {
		tl, err := reader(t).Timeline(context.Background(), account, limit, before)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range tl.Items {
			ids = append(ids, a.ID)
		}
		if tl.NextBefore == nil {
			return ids
		}
		before = tl.NextBefore
	}
	t.Fatal("timeline paging did not terminate")
	return nil
}

func TestTimelinePagingNeverSkipsOrRepeatsEvenAcrossTimestampTies(t *testing.T) {
	ctxfixture.Get(t, env.DB)
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	times := []time.Time{
		base.Add(1 * time.Hour), base.Add(2 * time.Hour),
		base.Add(3 * time.Hour), base.Add(3 * time.Hour), base.Add(3 * time.Hour), // a run of three ties
		base.Add(4 * time.Hour),
	}
	account, ids := newAccountWithActivities(t, "Ties", times)
	for _, limit := range []int{1, 2, 3, 4, 6, 7} {
		got := pageAll(t, account, limit)
		seen := map[string]int{}
		for _, id := range got {
			seen[id]++
		}
		if len(got) != len(ids) || len(seen) != len(ids) {
			t.Fatalf("limit %d returned %d rows (%d distinct) of %d: %v", limit, len(got), len(seen), len(ids), got)
		}
	}
	// Newest first, and a page does not end inside the three-way tie: limit 2 returns only the newest.
	first, err := reader(t).Timeline(context.Background(), account, 2, nil)
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != ids[5] || first.NextBefore == nil || !first.NextBefore.Equal(times[5]) {
		t.Fatalf("first page = %+v %v", first, err)
	}
	// A page that is nothing but ties is returned whole, even beyond the limit.
	tied, err := reader(t).Timeline(context.Background(), account, 2, first.NextBefore)
	if err != nil || len(tied.Items) != 3 || tied.NextBefore == nil || !tied.NextBefore.Equal(times[2]) {
		t.Fatalf("tie page = %d items, next %v, err %v", len(tied.Items), tied.NextBefore, err)
	}
}

func TestTimelineItemsCarryParticipantsAndProvenance(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	tl, err := reader(t).Timeline(context.Background(), w.AccountA, 5, nil)
	if err != nil || len(tl.Items) == 0 {
		t.Fatalf("timeline = %+v %v", tl, err)
	}
	withPeople := 0
	for _, a := range tl.Items {
		if a.AccountID == nil || *a.AccountID != w.AccountA || a.PayloadRef != "source_events/"+a.SourceEventID || len(a.IdempotencyKey) != 64 {
			t.Fatalf("activity %+v is malformed or of another account", a)
		}
		if len(a.Participants) > 0 {
			withPeople++
		}
	}
	if withPeople == 0 {
		t.Fatal("no recent sample activity has participants")
	}
	if _, err := reader(t).Timeline(context.Background(), missing, 5, nil); !errors.Is(err, readmodel.ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := reader(t).Timeline(context.Background(), w.AccountA, -1, nil); !errors.Is(err, readmodel.ErrInvalid) {
		t.Fatalf("negative limit: %v", err)
	}
}

func TestTraceFollowsRunToStateToActivities(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	ctx := context.Background()
	run := ctxfixture.FreshRun(t, env.DB, w.AccountA, "awaiting_human")
	version := scalar(t, `SELECT state_version::text FROM agent_runs WHERE id = $1::uuid`, run)
	trigger := scalar(t, `SELECT trigger_activity_ids[1]::text FROM agent_runs WHERE id = $1::uuid`, run)
	correlated := scalar(t, `SELECT id::text FROM activities WHERE account_id = $1::uuid AND id <> $2::uuid ORDER BY occurred_at LIMIT 1`, w.AccountA, trigger)
	corr := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	exec(t, `UPDATE activities SET correlation_id = $2::uuid WHERE id = ANY(ARRAY[$1::uuid, $3::uuid])`, trigger, corr, correlated)
	exec(t, `UPDATE agent_runs SET correlation_id = $2::uuid, model = 'fake', output = '{"evidence_refs":[{"activity_id":"`+trigger+`"}]}' WHERE id = $1::uuid`, run, corr)
	insertDiff(t, w.AccountA, 3001, true, `[{"field":"stage","op":"changed","material":true}]`)
	diff := scalar(t, `SELECT id::text FROM state_diffs WHERE account_id = $1::uuid AND to_version = 3001`, w.AccountA)
	exec(t, `INSERT INTO signals (account_id, signal_type, state_diff_id, rule, evidence_refs) VALUES ($1::uuid, 'stage_advanced', $2::uuid, 'sig.stage@1', '[]')`, w.AccountA, diff)
	signal := scalar(t, `SELECT id::text FROM signals WHERE account_id = $1::uuid AND signal_type = 'stage_advanced'`, w.AccountA)
	exec(t, `UPDATE trigger_evaluations SET state_diff_id = $2::uuid, signal_ids = ARRAY[$3::uuid]
 WHERE id = (SELECT trigger_evaluation_id FROM agent_runs WHERE id = $1::uuid)`, run, diff, signal)
	exec(t, `INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES ($1::uuid, 1, 'build_context', 'dry_run', 'succeeded'), ($1::uuid, 2, 'draft', 'dry_run', 'succeeded')`, run)
	exec(t, `INSERT INTO context_access_log (agent_run_id, tool, returned_ids, bytes, truncated) VALUES ($1::uuid, 'state', '[]'::jsonb, 10, true)`, run)
	exec(t, `INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label, reason) VALUES ($1::uuid, 'approve', 'web', 'rep', 'ok')`, run)

	tr, err := reader(t).Trace(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Run.ID != run || tr.Run.AccountID != w.AccountA || tr.Run.Status != "awaiting_human" || *tr.Run.Model != "fake" {
		t.Fatalf("run = %+v", tr.Run)
	}
	// The fixture triggers the run by every activity at the newest instant (no tie may stay outside the trigger).
	want := scalar(t, `SELECT cardinality(trigger_activity_ids)::text FROM agent_runs WHERE id = $1::uuid`, run)
	found := false
	for _, a := range tr.TriggerActivities {
		found = found || a.ID == trigger
	}
	if strconv.Itoa(len(tr.TriggerActivities)) != want || !found {
		t.Fatalf("trigger activities = %+v (want %s, including %s)", tr.TriggerActivities, want, trigger)
	}
	if len(tr.CorrelatedActivities) != 1 || tr.CorrelatedActivities[0].ID != correlated {
		t.Fatalf("correlated activities = %+v (want %s, and not the trigger itself)", tr.CorrelatedActivities, correlated)
	}
	if !tr.TriggerEvaluation.Eligible || tr.TriggerEvaluation.AgentRunID == nil || *tr.TriggerEvaluation.AgentRunID != run {
		t.Fatalf("trigger evaluation = %+v", tr.TriggerEvaluation)
	}
	if len(tr.Signals) != 1 || tr.Signals[0].ID != signal {
		t.Fatalf("signals = %+v", tr.Signals)
	}
	if tr.StateDiff == nil || tr.StateDiff.ID != diff || tr.StateDiff.ToVersion != 3001 {
		t.Fatalf("state diff = %+v", tr.StateDiff)
	}
	if string(tr.StateAtRun) == "null" || string(tr.StateBefore) == "null" {
		t.Fatalf("state_before/state_at_run missing for state version %s", version)
	}
	if len(tr.Run.Steps) != 2 || tr.Run.Steps[0].Step != "build_context" || len(tr.Run.InputContextRefs) != 1 || len(tr.ContextAccesses) != 1 || !tr.ContextAccesses[0].Truncated {
		t.Fatalf("steps %+v refs %+v accesses %+v", tr.Run.Steps, tr.Run.InputContextRefs, tr.ContextAccesses)
	}
	if len(tr.Decisions) != 1 || tr.Decisions[0].Decision != "approve" || string(tr.Decisions[0].EditedArtifact) != "null" {
		t.Fatalf("decisions = %+v", tr.Decisions)
	}
	if string(tr.Run.EvidenceRefs) == "[]" {
		t.Fatalf("run evidence refs not taken from the output: %s", tr.Run.EvidenceRefs)
	}
	if len(tr.Placeholders.EvalRuns)+len(tr.Placeholders.CustomerReactions)+len(tr.Placeholders.KnowledgeUpdates) != 0 {
		t.Fatalf("placeholders = %+v", tr.Placeholders)
	}
}

func TestTraceOfARunWithoutDiffOrDecisionsIsStillWhole(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountB, "context_built")
	tr, err := reader(t).Trace(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if tr.StateDiff != nil || string(tr.StateBefore) != "null" || string(tr.StateAtRun) == "null" {
		t.Fatalf("diff %+v before %s at-run-null=%v", tr.StateDiff, tr.StateBefore, string(tr.StateAtRun) == "null")
	}
	if tr.Signals == nil || tr.Decisions == nil || tr.CorrelatedActivities == nil || tr.ContextAccesses == nil || tr.Run.Steps == nil {
		t.Fatalf("empty collections must be [] not null: %+v", tr)
	}
	if string(tr.Run.Output) != "null" {
		t.Fatalf("output = %s", tr.Run.Output)
	}
}

// TestTraceSurfacesTheRunsCustomerReactions: a supervision row written for the run (HAR-120) shows
// up in placeholders.customer_reactions, schema-shaped.
func TestTraceSurfacesTheRunsCustomerReactions(t *testing.T) {
	w := ctxfixture.Get(t, env.DB)
	run := ctxfixture.FreshRun(t, env.DB, w.AccountB, "context_built")
	act := scalar(t, `SELECT id::text FROM activities WHERE account_id = $1::uuid ORDER BY occurred_at LIMIT 1`, w.AccountB)
	reactionID := scalar(t, `INSERT INTO customer_reactions
 (account_id, agent_run_id, activity_id, reaction_type, polarity, evidence_refs)
 VALUES ($1::uuid, $2::uuid, $3::uuid, 'replied', 'positive',
         jsonb_build_array(jsonb_build_object('activity_id', $3::text))) RETURNING id::text`,
		w.AccountB, run, act)
	tr, err := reader(t).Trace(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Placeholders.CustomerReactions) != 1 || tr.Placeholders.CustomerReactions[0].ID != reactionID {
		t.Fatalf("customer_reactions = %+v, want the stored row", tr.Placeholders.CustomerReactions)
	}
	got := tr.Placeholders.CustomerReactions[0]
	if got.ReactionType != "replied" || got.Polarity != "positive" || got.AgentRunID == nil || *got.AgentRunID != run {
		t.Fatalf("reaction = %+v", got)
	}
}

func TestTraceErrors(t *testing.T) {
	ctxfixture.Get(t, env.DB)
	for _, id := range []string{missing, "nope"} {
		if _, err := reader(t).Trace(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("trace of %q: %v", id, err)
		}
	}
}
