package learning_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

// HAR-97 B9: the scope a seed mints comes from the episode's own state (stage, relationship), so a seed on a
// fixture episode is not a transition-status-only rule.
func TestEpisodeSituationCarriesTheStateTheSeedIsScopedTo(t *testing.T) {
	seed := seedEpisode(t)
	sit, err := learning.EpisodeSituation(ctx(), env.DB, seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if sit.Stage == "" && sit.Relationship == "" {
		t.Fatalf("the situation carries no state: %+v", sit)
	}
	if sit.LearningScope == "" {
		t.Fatalf("the D5 learning scope is not read: %+v", sit)
	}
	sig, app := sit.Signature()
	if knowledge.ScopeTooBroad(knowledge.Knowledge{SituationSignature: sig, ApplicabilityConditions: app}) {
		t.Fatalf("seeded scope is too broad: %+v %+v", sig, app)
	}
}
