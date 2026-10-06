package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

func lifecycleRules(t *testing.T) knowledge.Rules {
	t.Helper()
	rules, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatalf("load lifecycle rules: %v", err)
	}
	return rules
}

// newKnowledge inserts a fresh candidate from the contract example, optionally learned from an episode.
func newKnowledge(t *testing.T, sourceEpisode string) string {
	t.Helper()
	raw, err := strategytest.ExampleFile("knowledge")
	if err != nil {
		t.Fatal(err)
	}
	var k knowledge.Knowledge
	if err := json.Unmarshal(raw, &k); err != nil {
		t.Fatal(err)
	}
	k.ID, k.Key, k.Status, k.Counts = strategytest.NewID(), nil, knowledge.StatusCandidate, knowledge.Counts{}
	k.SupportingDecisionEpisodeIDs, k.Counterexamples, k.StatusHistory, k.LastValidatedAt, k.CreatedAt = nil, nil, nil, nil, time.Time{}
	k.Provenance = knowledge.Provenance{CreatedFrom: "seed_history"}
	if sourceEpisode != "" {
		k.Provenance = knowledge.Provenance{CreatedFrom: "human_delta", SourceDecisionEpisodeID: &sourceEpisode}
	}
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := knowledgestore.Insert(context.Background(), tx, k, "controlplane test"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return k.ID
}

func record(t *testing.T, knowledgeID string, ev knowledge.Evidence) {
	t.Helper()
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := knowledgestore.RecordEvidence(context.Background(), tx, knowledgeID, ev, lifecycleRules(t)); err != nil {
		_ = tx.Rollback()
		t.Fatalf("record %s evidence: %v", ev.Kind, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func reaction(t *testing.T, seed strategytest.Seeded, reactionType string) string {
	t.Helper()
	return scalar(t, `INSERT INTO customer_reactions (account_id, agent_run_id, activity_id, reaction_type, polarity, decision_episode_id)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'positive', $5::uuid) RETURNING id::text`, seed.AccountID, seed.RunID, seed.ActivityID, reactionType, seed.EpisodeID)
}

func TestKnowledgeMutationsFollowEvidenceSupportThenPromotion(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Promote"))
	record(t, seed.KnowledgeID, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()})
	react := reaction(t, seed, "replied")
	record(t, seed.KnowledgeID, knowledge.Evidence{Kind: knowledge.EvidenceCustomerReaction, RefID: react, Polarity: "positive", At: time.Now()})

	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EpisodeID != seed.EpisodeID || len(got.Items) != 2 {
		t.Fatalf("mutations = %+v", got)
	}
	support, promote := got.Items[0], got.Items[1]
	if support.Operation != "STRENGTHEN" || support.KnowledgeID != seed.KnowledgeID || support.Version != 1 || support.Status != "candidate" ||
		support.StatusBefore == nil || *support.StatusBefore != "candidate" || support.Evidence.Kind != "decision_episode" || support.Evidence.RefID != seed.EpisodeID {
		t.Fatalf("first mutation = %+v", support)
	}
	if promote.Operation != "STRENGTHEN" || promote.Version != 2 || promote.Status != "provisional" || promote.StatusBefore == nil || *promote.StatusBefore != "candidate" ||
		promote.Evidence.Kind != "customer_reaction" || promote.Evidence.RefID != react {
		t.Fatalf("second mutation = %+v", promote)
	}
	if support.ID == promote.ID || len(support.ID) != 36 {
		t.Fatalf("mutation ids must be distinct uuids: %s %s", support.ID, promote.ID)
	}
	if support.Title == "" || len(support.Preconditions) == 0 || string(support.Exceptions) == "" || support.EvidenceEpisodeID != seed.EpisodeID {
		t.Fatalf("the knowledge scope is carried: %+v", support)
	}
	if support.Scope != "undetermined" || support.HumanVerdict != "none" || support.HumanConfirmed {
		t.Fatalf("scope / verdict = %s / %s / %v", support.Scope, support.HumanVerdict, support.HumanConfirmed)
	}
	again, _ := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if again.Items[0].ID != support.ID || again.Items[1].ID != promote.ID {
		t.Fatal("mutation ids are stable across reads")
	}
}

func TestKnowledgeLearnedFromTheEpisodeIsACreateAndAbsorbsItsSeedEvidence(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Create"))
	created := newKnowledge(t, seed.EpisodeID)
	record(t, created, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()})

	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("the seed evidence is part of the CREATE, not a second mutation: %+v", got.Items)
	}
	m := got.Items[0]
	if m.Operation != "CREATE" || m.KnowledgeID != created || m.StatusBefore != nil || m.Version != 1 || m.Status != "candidate" {
		t.Fatalf("create = %+v", m)
	}
	if m.Evidence.Kind != "decision_episode" || m.Evidence.RefID != seed.EpisodeID {
		t.Fatalf("a CREATE's evidence is the episode that taught it: %+v", m.Evidence)
	}
}

func TestACreateWithoutRecordedEvidenceStillReads(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Create Bare"))
	created := newKnowledge(t, seed.EpisodeID)
	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || len(got.Items) != 1 || got.Items[0].Operation != "CREATE" || got.Items[0].KnowledgeID != created || got.Items[0].Version != 1 {
		t.Fatalf("bare create = %+v %v", got, err)
	}
}

func TestACounterexampleIsReportedAsOneAndMayDispute(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Counter"))
	k := newKnowledge(t, "")
	record(t, k, knowledge.Evidence{Kind: knowledge.EvidenceCounterexample, RefID: seed.EpisodeID, Note: "the buyer wanted the call, not the package", At: time.Now()})

	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("mutations = %+v %v", got, err)
	}
	m := got.Items[0]
	if m.Operation != "WEAKEN" || m.Evidence.Kind != "counterexample" || m.Evidence.Note == nil || *m.Evidence.Note == "" {
		t.Fatalf("counterexample = %+v", m)
	}
}

func TestADisputingOrStaleningChangeWinsOverTheEvidenceKind(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Dispute"))
	k := newKnowledge(t, "")
	// One earlier counterexample from another episode, then this episode's: 2 counterexamples, share 1.0 -> disputed here.
	record(t, k, knowledge.Evidence{Kind: knowledge.EvidenceCounterexample, RefID: strategytest.NewID(), Note: "earlier miss", At: time.Now()})
	record(t, k, knowledge.Evidence{Kind: knowledge.EvidenceCounterexample, RefID: seed.EpisodeID, Note: "this miss", At: time.Now()})

	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("mutations = %+v %v", got, err)
	}
	if m := got.Items[0]; m.Operation != "DISPUTE" || m.Status != "disputed" || m.Version != 2 || m.StatusBefore == nil {
		t.Fatalf("dispute = %+v", m)
	}
}

func TestAStaleStatusChangeIsReportedAsStale(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Stale"))
	k := newKnowledge(t, "")
	// Evidence that arrives long after the knowledge was last validated moves it to stale.
	record(t, k, knowledge.Evidence{Kind: knowledge.EvidenceCounterexample, RefID: seed.EpisodeID, Note: "late", At: time.Now().AddDate(2, 0, 0)})
	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("mutations = %+v %v", got, err)
	}
	if m := got.Items[0]; m.Operation != "MARK_STALE" || m.Status != "stale" {
		t.Fatalf("stale = %+v", m)
	}
}

func TestHumanConfirmedMeansTheJudgmentVerdictWasConfirmed(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Verdict"))
	choose(t, seed, seed.Candidates[1])
	send(t, seed, "send")
	strategytest.SeedInference(t, env.DB, seed)
	record(t, seed.KnowledgeID, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()})

	for _, c := range []struct {
		verdict string
		want    bool
	}{{"pending", false}, {"corrected", false}, {"confirmed", true}} {
		exec(t, `UPDATE judgment_inferences SET human_verdict = $2,
 corrected_statement = CASE WHEN $2 = 'corrected' THEN 'The buyer sets timing.' ELSE NULL END,
 verdict_surface = CASE WHEN $2 = 'pending' THEN NULL ELSE 'slack' END,
 verdict_actor_label = CASE WHEN $2 = 'pending' THEN NULL ELSE 'Dana Kim' END,
 verdict_at = CASE WHEN $2 = 'pending' THEN NULL ELSE now() END
 WHERE decision_episode_id = $1::uuid`, seed.EpisodeID, c.verdict)
		got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
		if err != nil || len(got.Items) != 1 {
			t.Fatalf("%s: %+v %v", c.verdict, got, err)
		}
		if m := got.Items[0]; m.HumanVerdict != c.verdict || m.HumanConfirmed != c.want {
			t.Errorf("%s: human_verdict=%s human_confirmed=%v", c.verdict, m.HumanVerdict, m.HumanConfirmed)
		}
	}
}

func TestEpisodeScopeIsTheLearningScopeOfTheEvidenceEpisode(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations Scope"))
	exec(t, `UPDATE decision_episodes SET learning_scope = 'reusable_candidate' WHERE id = $1::uuid`, seed.EpisodeID)
	record(t, seed.KnowledgeID, knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()})
	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || len(got.Items) != 1 || got.Items[0].Scope != "reusable_candidate" {
		t.Fatalf("scope = %+v %v", got, err)
	}
}

func TestAnEpisodeThatChangedNoKnowledgeHasAnEmptyListNotAnError(t *testing.T) {
	seed := seedEpisode(t, seedAccount(t, "Mutations None"))
	got, err := reader(t).KnowledgeMutations(context.Background(), seed.EpisodeID)
	if err != nil || got.EpisodeID != seed.EpisodeID || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("empty = %+v %v", got, err)
	}
}

func TestKnowledgeMutationsOfAnUnknownEpisodeAreNotFound(t *testing.T) {
	for _, id := range []string{missing, "nope"} {
		if _, err := reader(t).KnowledgeMutations(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}
