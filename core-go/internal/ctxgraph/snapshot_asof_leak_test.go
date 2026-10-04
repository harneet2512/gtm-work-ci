package ctxgraph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// seedUndecidedEpisode adds what the orchestrator writes when it opens an episode: a second run on the CRM
// activity and its episode, awaiting the human's choice (human_action and the decision are NULL).
func seedUndecidedEpisode(t *testing.T, s ids) {
	t.Helper()
	runSeed(t, s, []seedStep{
		{"TRIG2", `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, state_diff_id) VALUES ($A,'post_interaction_followup',true,'{eligible_meeting_completed}',NULL) RETURNING id`},
		{"RUN2", `INSERT INTO agent_runs (account_id, opportunity_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
 VALUES ($A,$OPP,'post_interaction_followup','dry_run','ignored',$TRIG2,ARRAY[$ACT_CRM]::uuid[]) RETURNING id`},
		{"EP2", `INSERT INTO decision_episodes (agent_run_id, account_id, state_version, state_diff_id, status) VALUES ($RUN2,$A,1,$DIFF,'awaiting_choice') RETURNING id`},
	})
}

var crmAt = time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC) // ACT_CRM, the trigger of the undecided episode

// H2: an episode that awaits the human's choice has no human_action; neither the current projection nor
// the world graph may fail on it.
func TestUndecidedEpisodeDoesNotBreakTheSnapshots(t *testing.T) {
	s := seedWorld(t)
	seedUndecidedEpisode(t, s)
	current, err := BuildSnapshot(context.Background(), pg.DB, s["A"])
	if err != nil {
		t.Fatalf("current snapshot with an undecided episode: %v", err)
	}
	ep, ok := findNode(current, s["EP2"])
	if !ok {
		t.Fatal("the undecided episode must be projected")
	}
	if ep.Props["human_action"] != "awaiting_choice" {
		t.Errorf("human_action = %v, want the episode status while undecided", ep.Props["human_action"])
	}
	if decided, _ := findNode(current, s["EP"]); decided.Props["human_action"] != "APPROVE_UNCHANGED" {
		t.Errorf("a decided episode keeps its action: %v", decided.Props["human_action"])
	}
	world := buildAsOf(t, s, crmAt.Add(time.Microsecond))
	if _, ok := findNode(world, s["EP2"]); !ok {
		t.Error("the undecided episode exists once its trigger is before T")
	}
}

// H1a: the human's verdict is written later, in place, and has no world time: no world snapshot carries it,
// whatever T, and neither does it carry the episode status.
func TestWorldSnapshotCarriesNoVerdictOrStatusOfAnEpisode(t *testing.T) {
	s := seedWorld(t)
	seedUndecidedEpisode(t, s)
	for _, at := range []time.Time{callAt.Add(time.Microsecond), farFuture} {
		snap := buildAsOf(t, s, at)
		for _, n := range snap.Nodes {
			if n.Primary() != LabelDecisionEpisode {
				continue
			}
			if _, has := n.Props["human_action"]; has {
				t.Errorf("T=%s: episode %s carries human_action %v", at, n.ID, n.Props["human_action"])
			}
			if _, has := n.Props["status"]; has {
				t.Errorf("T=%s: episode %s carries a status", at, n.ID)
			}
		}
	}
	// The decision made after T changes nothing at T: the snapshot is byte for byte the same.
	before := buildAsOf(t, s, farFuture).Hashes()
	runSeed(t, s, []seedStep{
		{"HD2", `INSERT INTO human_decisions (agent_run_id, decision, surface, actor_label) VALUES ($RUN2,'reject','web','Dana') RETURNING id`},
		{"", `INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output) VALUES ($RUN2,1,'account_agent','{}')`},
		{"", `UPDATE decision_episodes SET status='decided', human_decision_id=$HD2, human_action='REJECT', final_draft_index=1 WHERE id=$EP2`},
	})
	after := buildAsOf(t, s, farFuture).Hashes()
	for k, h := range before {
		if after[k] != h {
			t.Errorf("%s changed when the human decided after T", k)
		}
	}
}

// H1b: a run reads the world at its own trigger. An episode on the same trigger (an earlier A/B/C arm, a
// re-run) and its verdict are not part of that world; an episode on an earlier trigger is.
func TestRunScopedGraphExcludesEpisodesOfItsOwnTrigger(t *testing.T) {
	s := seedWorld(t)
	seedUndecidedEpisode(t, s)
	reader := NewReader(nil, pg.DB, nil)
	pull := func(trigger time.Time) string {
		items, _, err := reader.NeighborhoodItemsAsOf(context.Background(), s["A"], 20, nil, trigger.Add(time.Microsecond), trigger)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(items)
		return string(raw)
	}
	onCall := pull(callAt) // the run triggered by the call: the seeded EP has the same trigger
	if strings.Contains(onCall, s["EP"]) {
		t.Error("the earlier arm's episode on the same trigger must not be visible to the re-run")
	}
	if strings.Contains(onCall, "APPROVE_UNCHANGED") {
		t.Error("the earlier arm's verdict leaked")
	}
	onCRM := pull(crmAt) // a later run: the episode of the call is history, the one on its own trigger is not
	if !strings.Contains(onCRM, s["EP"]) {
		t.Error("an episode on an earlier trigger is part of the run's world")
	}
	if strings.Contains(onCRM, s["EP2"]) {
		t.Error("the episode on the run's own trigger must not be visible")
	}
	if strings.Contains(onCRM, "APPROVE_UNCHANGED") || strings.Contains(onCRM, "awaiting_choice") {
		t.Error("no verdict or status in the run's world")
	}
}

// The snapshot builder offers the same exclusion directly.
func TestSnapshotAsOfRunExcludesEpisodesFromItsTrigger(t *testing.T) {
	s := seedWorld(t)
	snap, err := BuildSnapshotAsOfRun(context.Background(), pg.DB, s["A"], callAt.Add(time.Microsecond), callAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findNode(snap, s["EP"]); ok {
		t.Error("the episode triggered at the run's own trigger time must be excluded")
	}
}

func TestNeighborhoodAsOfRefusesAnUnknownOrMalformedAccount(t *testing.T) {
	s := seedWorld(t)
	r := NewReader(nil, pg.DB, nil)
	ctx := context.Background()
	if _, err := r.NeighborhoodAsOf(ctx, "not-a-uuid", Params{}, nil, farFuture); err != ErrAccountUnknown {
		t.Errorf("malformed account: %v", err)
	}
	if _, err := r.NeighborhoodAsOf(ctx, "99999999-9999-4999-8999-999999999999", Params{}, nil, farFuture); err != ErrAccountUnknown {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := r.NeighborhoodAsOf(ctx, s["A"], Params{SectionLimit: -1}, nil, farFuture); err == nil {
		t.Error("an out-of-range section limit must be refused")
	}
	view, err := NewService(r, pg.DB).NeighborhoodAsOf(ctx, s["A"], Params{SectionLimit: MaxSectionLimit}, callAt.Add(time.Microsecond))
	if err != nil || !view.Projection.Complete {
		t.Fatalf("the operator world view: %+v %v", view, err)
	}
	for _, n := range view.Nodes {
		if n.Type == LabelPerson && n.Label != LabelPerson {
			t.Errorf("a world read has no display name, so the label is the type: %+v", n)
		}
	}
}
