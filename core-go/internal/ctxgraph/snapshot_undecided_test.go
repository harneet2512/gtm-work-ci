package ctxgraph

import (
	"context"
	"testing"
)

// The orchestrator (HAR-117) publishes a decision episode in status awaiting_choice, with no human_action yet. The
// projection must carry it (a published set is part of the account's story) instead of failing the account's rebuild.
func TestAnEpisodeAwaitingTheHumansChoiceIsProjectedWithoutAHumanAction(t *testing.T) {
	s := seedWorld(t)
	for _, q := range []string{
		`INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes) VALUES ('` + s["B"] + `','post_interaction_followup',true,'{eligible_meeting_completed}')`,
		`INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
 SELECT '` + s["B"] + `','post_interaction_followup','dry_run','awaiting_human', id, ARRAY[gen_random_uuid()] FROM trigger_evaluations WHERE account_id = '` + s["B"] + `'`,
		`INSERT INTO decision_episodes (agent_run_id, account_id, state_version, status)
 SELECT id, account_id, 1, 'awaiting_choice' FROM agent_runs WHERE account_id = '` + s["B"] + `'`,
	} {
		if _, err := pg.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	snap, err := BuildSnapshot(context.Background(), pg.DB, s["B"])
	if err != nil {
		t.Fatalf("an undecided episode must not fail the account's snapshot: %v", err)
	}
	if got := countLabel(snap, LabelDecisionEpisode); got != 1 {
		t.Fatalf("episode nodes = %d, want 1", got)
	}
	if err := ValidateSnapshot(snap); err != nil {
		t.Fatalf("the snapshot must still conform to the ontology: %v", err)
	}
	var episode string
	if err := pg.DB.QueryRow(`SELECT id::text FROM decision_episodes WHERE account_id = $1::uuid`, s["B"]).Scan(&episode); err != nil {
		t.Fatal(err)
	}
	if got := stringProp(nodeByID(t, snap, episode).Props, "human_action"); got != "awaiting_choice" {
		t.Fatalf("human_action = %q, want the episode's status while no human action exists", got)
	}
}
