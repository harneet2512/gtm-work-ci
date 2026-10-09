package learning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrLearningDeclined: the human answered the episode's judgment inference with no_learning (Slack "Don't
// learn this", HAR-129 Cliff). Nothing is learned from the episode, so the seeds refuse it.
var ErrLearningDeclined = errors.New("learning: the human declined learning from this episode")

// rowQuerier is the one read LearningDeclined needs; *sql.DB and *sql.Tx both have it.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// LearningDeclined reports whether the human declined learning from the episode.
func LearningDeclined(ctx context.Context, q rowQuerier, episodeID string) (bool, error) {
	var declined bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM judgment_inferences
 WHERE decision_episode_id = $1::uuid AND human_verdict = 'no_learning')`, episodeID).Scan(&declined)
	if err != nil {
		return false, fmt.Errorf("learning: read the verdict of episode %s: %w", episodeID, err)
	}
	return declined, nil
}

// refuseDeclined is the guard both seeds start with.
func refuseDeclined(ctx context.Context, q rowQuerier, episodeID string) error {
	declined, err := LearningDeclined(ctx, q, episodeID)
	if err != nil {
		return err
	}
	if declined {
		return fmt.Errorf("%w: %s", ErrLearningDeclined, episodeID)
	}
	return nil
}

// DeclineEpisode applies a no_learning verdict to what the episode already seeded. The send seeds the
// unexplained delta's candidate before the human sees Message 3, so the opt-out has to reach back: every
// candidate evaluator version learned from the episode (through its delta or through its knowledge's
// provenance) is retired with an audited reason, and the explained delta's generator feedback no run has
// consumed yet is withdrawn (kept for audit, never offered), so the account's next strategies request never
// carries it. A shadow is left alone: it was gated on every supporting episode, and the backtest no longer
// counts this one, so the human's opt-out never discards other episodes' evidence; an active version is never
// touched. The seeded knowledge row stays a candidate and its linked version is now retired, so the narrow-candidate rule no
// longer offers it (knowledgestore.offered): no decision can cite it.
// The audit rows carry at, the decided episode's world time (a zero at keeps the database clock). It returns
// how many versions it retired.
func DeclineEpisode(ctx context.Context, tx *sql.Tx, episodeID, decidedBy string, at time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ev.evaluator::text, ev.version, ev.status
 FROM evaluator_versions ev
 LEFT JOIN human_deltas hd ON hd.id = ev.source_human_delta_id
 LEFT JOIN knowledge k ON k.id = ev.knowledge_id
 WHERE ev.status = 'candidate'
   AND (hd.decision_episode_id = $1::uuid OR k.provenance ->> 'source_decision_episode_id' = $1::text)
 ORDER BY ev.evaluator, ev.version FOR UPDATE OF ev`, episodeID)
	if err != nil {
		return 0, fmt.Errorf("learning: find what episode %s seeded: %w", episodeID, err)
	}
	type version struct {
		evaluator, status string
		version           int
	}
	var seeded []version
	for rows.Next() {
		var v version
		if err := rows.Scan(&v.evaluator, &v.version, &v.status); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("learning: scan a seeded version of episode %s: %w", episodeID, err)
		}
		seeded = append(seeded, v)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	reason := "the human declined learning from decision episode " + episodeID
	for _, v := range seeded {
		if _, err := transitionAt(ctx, tx, v.evaluator, v.version, v.status, "retired", nil, decidedBy, reason, at); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE generator_feedback
 SET withdrawn_at = COALESCE($2::timestamptz, now()), withdrawn_reason = $3
 WHERE decision_episode_id = $1::uuid AND consumed_by_run_id IS NULL AND withdrawn_at IS NULL`,
		episodeID, nullTime(at), reason); err != nil {
		return 0, fmt.Errorf("learning: withdraw the pending feedback of episode %s: %w", episodeID, err)
	}
	return len(seeded), nil
}
