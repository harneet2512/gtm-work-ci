package controlplane_test

import (
	"context"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func strategies(t *testing.T) *strategystore.Service {
	t.Helper()
	svc, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func choose(t *testing.T, seed strategytest.Seeded, candidate string) {
	t.Helper()
	req := strategystore.DecisionRequest{SelectedCandidateID: candidate, Surface: "slack", ActorLabel: "Dana Kim"}
	if _, _, err := strategies(t).RecordDecision(context.Background(), seed.RunID, req); err != nil {
		t.Fatalf("choose: %v", err)
	}
}

func send(t *testing.T, seed strategytest.Seeded, decision string) {
	t.Helper()
	req := strategystore.SendRequest{Decision: decision, Surface: "slack", ActorLabel: "Dana Kim"}
	if _, err := strategies(t).Send(context.Background(), seed.RunID, req); err != nil {
		t.Fatalf("send %s: %v", decision, err)
	}
}

func TestEpisodeAwaitingChoiceNamesAccountTriggerRunAndGhostsPick(t *testing.T) {
	acct := seedAccount(t, "Episode Summary Early")
	seed := seedEpisode(t, acct)

	ep, err := reader(t).Episode(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if ep.ID != seed.EpisodeID || ep.AgentRunID != seed.RunID || ep.AccountID != acct || ep.AccountName != "Episode Summary Early" {
		t.Fatalf("identity = %+v", ep)
	}
	if ep.StrategySetID == nil || *ep.StrategySetID != seed.SetID {
		t.Fatalf("strategy set = %v", ep.StrategySetID)
	}
	if ep.Status != "awaiting_choice" || ep.FinalStatus != "awaiting_choice" || ep.JudgmentStatus != "none" {
		t.Fatalf("status = %s / %s / %s", ep.Status, ep.FinalStatus, ep.JudgmentStatus)
	}
	te := ep.TriggeringEvent
	if te == nil || te.ActivityID != seed.ActivityID || te.ActivityType != "EmailReceived" || te.SourceSystem != "email" ||
		te.TriggerActivityCount != 1 || te.Summary == nil || *te.Summary != "Marco asks for the security documents" {
		t.Fatalf("triggering event = %+v", te)
	}
	if ep.Run.ID != seed.RunID || ep.Run.Status != "awaiting_human" || ep.Run.Phase != "published" {
		t.Fatalf("run = %+v", ep.Run)
	}
	rec := ep.RecommendedAction
	if rec == nil || rec.CandidateID != seed.Candidates[0] || rec.Ranking != 1 || !rec.PreferredByAgent || rec.Title == "" || rec.ActionType != "send_email" {
		t.Fatalf("recommended = %+v", rec)
	}
	if ep.SelectedAction != nil || ep.HumanOutcome != nil || ep.Replay != nil {
		t.Fatalf("nothing is chosen yet: %+v %+v %+v", ep.SelectedAction, ep.HumanOutcome, ep.Replay)
	}
}

func TestEpisodeShowsTheHumanOverridingGhostThenSending(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Episode Summary Flow"))
	choose(t, seed, seed.Candidates[1])

	ep, err := reader(t).Episode(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Status != "chosen" || ep.FinalStatus != "awaiting_send" {
		t.Fatalf("after choosing: %s / %s", ep.Status, ep.FinalStatus)
	}
	if sel := ep.SelectedAction; sel == nil || sel.CandidateID != seed.Candidates[1] || sel.Ranking != 2 || sel.PreferredByAgent {
		t.Fatalf("selected = %+v", ep.SelectedAction)
	}
	o := ep.HumanOutcome
	if o == nil || o.Agreement != "overrode" || o.SendDecision != "pending" || o.HumanAction != nil || o.Edited || o.ActorLabel != "Dana Kim" || o.SendDecidedAt != nil {
		t.Fatalf("outcome after choosing = %+v", o)
	}
	if ep.RecommendedAction == nil || ep.RecommendedAction.CandidateID != seed.Candidates[0] {
		t.Fatal("Ghost's recommendation stays visible after the human overrides it")
	}

	send(t, seed, "send")
	ep, err = reader(t).Episode(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	o = ep.HumanOutcome
	if ep.Status != "decided" || ep.FinalStatus != "send_recorded" || o == nil || o.SendDecision != "send" || o.HumanAction == nil || *o.HumanAction != "APPROVE_UNCHANGED" || o.SendDecidedAt == nil {
		t.Fatalf("after sending: %s / %s %+v", ep.Status, ep.FinalStatus, o)
	}
	if ep.JudgmentStatus != "none" {
		t.Fatalf("judgment before an inference exists = %s", ep.JudgmentStatus)
	}

	strategytest.SeedInference(t, env.DB, seed)
	if ep, err = reader(t).Episode(context.Background(), seed.EpisodeID); err != nil || ep.JudgmentStatus != "pending" {
		t.Fatalf("judgment after the inference: %v %+v", err, ep.JudgmentStatus)
	}
	exec(t, `UPDATE judgment_inferences SET human_verdict = 'confirmed', verdict_surface = 'slack', verdict_actor_label = 'Dana Kim', verdict_at = now()
 WHERE decision_episode_id = $1::uuid`, seed.EpisodeID)
	if ep, err = reader(t).Episode(context.Background(), seed.EpisodeID); err != nil || ep.JudgmentStatus != "confirmed" {
		t.Fatalf("judgment after the verdict: %v %+v", err, ep.JudgmentStatus)
	}
}

func TestEpisodeDiscardedAndAgreedAndEditedAreReadFromTheDecision(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Episode Summary Discard"))
	choose(t, seed, seed.Candidates[0])
	send(t, seed, "discard")
	ep, err := reader(t).Episode(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if ep.FinalStatus != "discarded" || ep.HumanOutcome == nil || ep.HumanOutcome.Agreement != "agreed" || ep.HumanOutcome.SendDecision != "discard" {
		t.Fatalf("discarded = %s %+v", ep.FinalStatus, ep.HumanOutcome)
	}

	edited := seedEpisode(t, seedAccount(t, "Episode Summary Edit"))
	subject := "A different subject"
	body := "A shorter note."
	req := strategystore.DecisionRequest{SelectedCandidateID: edited.Candidates[0], Surface: "web", ActorLabel: "Dana Kim",
		FinalArtifact: &strategystore.Artifact{Channel: "email", Subject: &subject, Body: body}}
	if _, _, err := strategies(t).RecordDecision(context.Background(), edited.RunID, req); err != nil {
		t.Fatal(err)
	}
	ep, err = reader(t).Episode(context.Background(), edited.EpisodeID)
	if err != nil || ep.HumanOutcome == nil || !ep.HumanOutcome.Edited {
		t.Fatalf("an edited choice is flagged: %v %+v", err, ep.HumanOutcome)
	}
}

func TestEpisodeWithoutAStrategySetHasNoCandidatesAndStillReads(t *testing.T) {
	acct := seedAccount(t, "Episode Summary Legacy")
	runID, _, err := ctxfixture.InsertRun(context.Background(), env.DB, acct, "awaiting_human")
	if err != nil {
		t.Fatal(err)
	}
	episode := scalar(t, `INSERT INTO decision_episodes (agent_run_id, account_id, state_version, status) VALUES ($1::uuid, $2::uuid, 1, 'awaiting_choice') RETURNING id::text`, runID, acct)

	ep, err := reader(t).Episode(context.Background(), episode)
	if err != nil {
		t.Fatal(err)
	}
	if ep.StrategySetID != nil || ep.RecommendedAction != nil || ep.SelectedAction != nil || ep.HumanOutcome != nil {
		t.Fatalf("no set, so no actions: %+v", ep)
	}
	if ep.TriggeringEvent == nil || ep.Run.Phase == "" || ep.FinalStatus != "awaiting_choice" {
		t.Fatalf("still has a trigger, a run phase and a status: %+v", ep)
	}
}

func TestEpisodeReportsWhereItSitsInAReplay(t *testing.T) {
	acct := seedAccount(t, "Episode Summary Replay")
	seed := seedEpisode(t, acct)
	opp := scalar(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Replay opp', 'expansion') RETURNING id::text`, acct)
	manifest := scalar(t, `INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
 VALUES ($1::uuid, $2::uuid, now(), '[{"event":{"replay_position":11}}]'::jsonb,
         jsonb_build_object('event_id', gen_random_uuid()::text, 'replay_position', 12, 'payload_sha256', repeat('a', 64)), 'test', repeat('b', 64)) RETURNING id::text`, acct, opp)
	exec(t, `INSERT INTO demo_episodes (manifest_id, position, account_id, event_id, source_event_id, activity_id, material, decision_episode_id)
 SELECT $1::uuid, 12, $2::uuid, gen_random_uuid(), a.source_event_id, a.id, true, $4::uuid FROM activities a WHERE a.id = $3::uuid`, manifest, acct, seed.ActivityID, seed.EpisodeID)

	ep, err := reader(t).Episode(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Replay == nil || ep.Replay.ManifestID != manifest || ep.Replay.Position != 12 {
		t.Fatalf("replay = %+v", ep.Replay)
	}
}

func TestEpisodeRefusesUnknownAndMalformedIds(t *testing.T) {
	for _, id := range []string{missing, "not-a-uuid", ""} {
		if _, err := reader(t).Episode(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("Episode(%q) = %v, want ErrNotFound", id, err)
		}
	}
}
