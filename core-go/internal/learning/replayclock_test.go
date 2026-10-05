package learning_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

// HAR-144: the time a replayed episode's knowledge was learned is that episode's world time, never the
// database clock. These tests pin it for the delta seed, the corrected-verdict seed and the backtest.

var (
	episodeK = time.Date(2025, 3, 10, 9, 0, 0, 0, time.UTC) // world time of the decided episode k
	laterEp  = episodeK.Add(48 * time.Hour)                 // a later chronological episode
	earlier  = episodeK.Add(-48 * time.Hour)                // an earlier chronological episode
)

func beginTx(t *testing.T) *sql.Tx {
	t.Helper()
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func deltaSeedOf(t *testing.T, tx *sql.Tx, episodeID, accountID, deltaID string, at time.Time, replay bool) learning.DeltaSeed {
	t.Helper()
	sit, err := learning.EpisodeSituation(ctx(), tx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	return learning.DeltaSeed{DeltaID: deltaID, EpisodeID: episodeID, AccountID: accountID,
		Criterion: learning.Criterion{Statement: "soften the ask", SuggestedEvalType: "cta_calibration"},
		Situation: sit, At: at, Replay: replay}
}

func createdAt(t *testing.T, query, id string) time.Time {
	t.Helper()
	var ts time.Time
	if err := env.DB.QueryRow(query, id).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts.UTC()
}

func seedReplay(t *testing.T, episodeID, accountID, deltaID string, rules *knowledge.Rules) learning.SeedResult {
	t.Helper()
	tx := beginTx(t)
	res, err := learning.SeedDeltaCriterion(ctx(), tx, deltaSeedOf(t, tx, episodeID, accountID, deltaID, episodeK, true), rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestReplaySeedDeltaCriterionStampsKnowledgeWithTheEpisodesWorldTime(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[]`)
	rules := testRules(t)

	res := seedReplay(t, seed.EpisodeID, seed.AccountID, deltaID, &rules)

	if got := createdAt(t, `SELECT created_at FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); !got.Equal(episodeK) {
		t.Fatalf("knowledge created_at = %s, want the episode's world time %s", got, episodeK)
	}
	if got := createdAt(t, `SELECT created_at FROM evaluator_versions WHERE knowledge_id = $1::uuid`, res.KnowledgeID); !got.Equal(episodeK) {
		t.Fatalf("candidate evaluator created_at = %s, want %s", got, episodeK)
	}
}

func TestReplayKnowledgeIsVisibleToLaterEpisodesAndHiddenFromEarlierOnes(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[]`)
	rules := testRules(t)
	res := seedReplay(t, seed.EpisodeID, seed.AccountID, deltaID, &rules)
	// Earn the knowledge in replay time so it is applicable (a candidate is never read as guidance).
	tx := beginTx(t)
	if _, _, err := knowledgestore.RecordEvidence(ctx(), tx, res.KnowledgeID, knowledge.Evidence{
		Kind: knowledge.EvidenceCustomerReaction, RefID: seed.ActivityID, Polarity: "positive",
		At: episodeK.Add(time.Hour)}, rules); err != nil {
		t.Fatal(err)
	}

	has := func(at time.Time) bool {
		got, err := knowledgestore.ListApplicableAsOf(ctx(), tx, rules, at)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range got {
			if k.ID == res.KnowledgeID {
				return true
			}
		}
		return false
	}
	if !has(laterEp) {
		t.Fatal("an as-of read at an episode after k must see the knowledge learned at k")
	}
	if has(earlier) {
		t.Fatal("an as-of read at an episode before k must not see the knowledge learned at k")
	}
}

func TestReplaySeedDeltaCriterionRejectsTheDatabaseClockDefault(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[]`)
	tx := beginTx(t)

	_, err := learning.SeedDeltaCriterion(ctx(), tx, deltaSeedOf(t, tx, seed.EpisodeID, seed.AccountID, deltaID, time.Time{}, true), nil)

	if !errors.Is(err, knowledgestore.ErrCreatedAtRequired) {
		t.Fatalf("a replay seed without a world time = %v, want ErrCreatedAtRequired", err)
	}
}

func TestLiveSeedDeltaCriterionKeepsTheWallClock(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[]`)
	tx := beginTx(t)
	before := time.Now().Add(-time.Minute)

	res, err := learning.SeedDeltaCriterion(ctx(), tx, deltaSeedOf(t, tx, seed.EpisodeID, seed.AccountID, deltaID, episodeK, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := createdAt(t, `SELECT created_at FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); got.Before(before) {
		t.Fatalf("live created_at = %s, want the wall clock (after %s)", got, before)
	}
}

func TestReplaySeedVerdictCriterionStampsKnowledgeWithTheEpisodesWorldTime(t *testing.T) {
	seed := seedEpisode(t)
	rules := testRules(t)
	tx := beginTx(t)

	res, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID, "The buyer wanted a slower cadence.", episodeK, true, &rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := createdAt(t, `SELECT created_at FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); !got.Equal(episodeK) {
		t.Fatalf("verdict knowledge created_at = %s, want %s", got, episodeK)
	}
	if got := createdAt(t, `SELECT created_at FROM evaluator_versions WHERE knowledge_id = $1::uuid`, res.KnowledgeID); !got.Equal(episodeK) {
		t.Fatalf("verdict candidate evaluator created_at = %s, want %s", got, episodeK)
	}
}

func TestReplaySeedVerdictCriterionRejectsTheDatabaseClockDefault(t *testing.T) {
	seed := seedEpisode(t)
	tx := beginTx(t)

	_, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID, "a correction", time.Time{}, true, nil)

	if !errors.Is(err, knowledgestore.ErrCreatedAtRequired) {
		t.Fatalf("a replay verdict seed without a world time = %v, want ErrCreatedAtRequired", err)
	}
}

func TestLiveSeedVerdictCriterionKeepsTheWallClock(t *testing.T) {
	seed := seedEpisode(t)
	tx := beginTx(t)
	before := time.Now().Add(-time.Minute)

	res, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID, "a live correction", time.Time{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := createdAt(t, `SELECT created_at FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); got.Before(before) {
		t.Fatalf("live verdict created_at = %s, want the wall clock", got)
	}
}

func TestReplayBacktestRejectsTheWallClockDefault(t *testing.T) {
	_, err := learning.Backtest(ctx(), env.DB, learning.BacktestOpts{Evaluator: "cta_calibration", Version: 2,
		GoldDirs: goldDirs, Replay: true})
	if !errors.Is(err, knowledgestore.ErrCreatedAtRequired) {
		t.Fatalf("a replay backtest without Now = %v, want ErrCreatedAtRequired", err)
	}
}
