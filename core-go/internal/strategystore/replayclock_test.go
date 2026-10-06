package strategystore_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// HAR-144: knowledge learned from a send or a corrected verdict is stamped with the decided episode's world
// time (its run's trigger activity), not the database clock.

// pinWorldTime moves the fixture's trigger activity into the past so the world time and the wall clock differ.
func pinWorldTime(t *testing.T, f *fixture) time.Time {
	t.Helper()
	world := time.Date(2025, 3, 10, 9, 0, 0, 0, time.UTC)
	if _, err := env.DB.Exec(`UPDATE activities SET occurred_at = $2 WHERE id = $1::uuid`, f.seed.ActivityID, world); err != nil {
		t.Fatal(err)
	}
	return world
}

func knowledgeCreatedAt(t *testing.T, id string) time.Time {
	t.Helper()
	var ts time.Time
	if err := env.DB.QueryRow(`SELECT created_at FROM knowledge WHERE id = $1::uuid`, id).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts.UTC()
}

func TestUnexplainedDeltaKnowledgeIsLearnedAtTheEpisodesWorldTime(t *testing.T) {
	criterion := workerclient.DeltaCriterion{Statement: "The human rewrites the ask when the buyer slows the thread.",
		SuggestedEvalType: "cta_calibration"}
	labeler := &stubLabeler{resp: workerclient.LabelDeltaResponse{
		SemanticLabels: []string{"reduced_pressure"}, CandidateCriterion: &criterion, Model: "stub-labeler-v1"}}
	f := newFixtureWith(t, strategystore.WithLabeler(labeler))
	world := pinWorldTime(t, f)

	editAndSend(t, f)

	deltaID := scalar(t, `SELECT human_delta_id::text FROM decision_episodes WHERE id = $1::uuid`, f.seed.EpisodeID)
	kid := scalar(t, `SELECT candidate_criterion ->> 'knowledge_candidate_id' FROM human_deltas WHERE id = $1::uuid`, deltaID)
	if got := knowledgeCreatedAt(t, kid); !got.Equal(world) {
		t.Fatalf("knowledge created_at = %s, want the episode's world time %s", got, world)
	}
}

func TestCorrectedVerdictKnowledgeIsLearnedAtTheEpisodesWorldTime(t *testing.T) {
	f := sentFixture(t)
	world := pinWorldTime(t, f)

	if _, err := f.verdict(func(r *strategystore.VerdictRequest) {
		r.Verdict, r.CorrectedStatement = "corrected", "World-time correction: the buyer wanted a slower cadence."
	}); err != nil {
		t.Fatal(err)
	}

	kid := scalar(t, `SELECT knowledge_id::text FROM evaluator_versions WHERE evaluator = 'human_delta' ORDER BY version DESC LIMIT 1`)
	if got := knowledgeCreatedAt(t, kid); !got.Equal(world) {
		t.Fatalf("verdict knowledge created_at = %s, want the episode's world time %s", got, world)
	}
}
