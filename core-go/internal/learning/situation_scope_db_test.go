package learning_test

import (
	"github.com/harneet2512/gtm-work/core-go/internal/changedim"
	"strings"
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
	for _, c := range append(append([]knowledge.Condition{}, sig...), app...) {
		if c.Op == "is_unknown" || c.Value == "unknown" {
			t.Fatalf("seeded scope states an unknown value: %+v", c)
		}
	}
	if len(knowledge.LessonFeatures(knowledge.Knowledge{SituationSignature: sig, ApplicabilityConditions: app})) == 0 {
		t.Fatalf("seeded scope carries no comparable feature: %+v %+v", sig, app)
	}
}

// The episode's topic is read from its own triggering diff, the same dimensions the BI update's claims carry.
func TestEpisodeSituationReadsItsTopicFromItsTriggeringDiff(t *testing.T) {
	seed := seedEpisode(t)
	sit, err := learning.EpisodeSituation(ctx(), env.DB, seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	var diffID *string
	if err := env.DB.QueryRowContext(ctx(), `SELECT state_diff_id::text FROM decision_episodes WHERE id = $1::uuid`, seed.EpisodeID).Scan(&diffID); err != nil {
		t.Fatal(err)
	}
	want, err := changedim.DiffTopics(ctx(), env.DB, deref(diffID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sit.Topics, ",") != strings.Join(want, ",") {
		t.Fatalf("topics = %v, the diff's are %v", sit.Topics, want)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
