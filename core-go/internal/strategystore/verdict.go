package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

// SubmitVerdict is POST /episodes/{episode_id}/judgment-verdict: the human answers Ghost's inference. The
// first verdict (confirmed or corrected) closes the episode as judged; later calls may add a note, repeat the
// same verdict or turn confirmed into corrected, and are logged. A verdict never reaches company knowledge:
// the record only feeds a candidate criterion that HAR-97 scopes and backtests.
func (s *Service) SubmitVerdict(ctx context.Context, episodeID string, req VerdictRequest) ([]byte, error) {
	if !IsUUID(episodeID) {
		return nil, &NotFoundError{Code: "not_found"}
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("strategystore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var inferenceID, verdict string
	err = tx.QueryRowContext(ctx, `
SELECT id::text, human_verdict FROM judgment_inferences WHERE decision_episode_id = $1::uuid FOR UPDATE`, episodeID).Scan(&inferenceID, &verdict)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := s.Inference(ctx, episodeID); err != nil {
			return nil, err
		}
		return nil, &NotReadyError{Code: CodeInferenceWait}
	}
	if err != nil {
		return nil, fmt.Errorf("strategystore: lock inference of episode %s: %w", episodeID, err)
	}
	if req.ActorPersonID != nil {
		if err := checkPeople(ctx, tx, "", Draft{}, req.ActorPersonID); err != nil {
			return nil, err
		}
	}
	if err := applyVerdict(ctx, tx, inferenceID, verdict, req); err != nil {
		return nil, err
	}
	if req.Verdict == "corrected" {
		// The correction feeds the learning loop only: a candidate criterion + candidate knowledge,
		// never a promotion (HAR-119). A repeated identical correction replays the same seed.
		// The criterion is learned at the decided episode's world time (HAR-144), not the wall clock.
		at, err := episodeWorldTime(ctx, tx, episodeID)
		if err != nil {
			return nil, err
		}
		if _, err := learning.SeedVerdictCriterion(ctx, tx, episodeID, req.CorrectedStatement, at, true, s.rules); err != nil {
			return nil, fmt.Errorf("strategystore: seed criterion of corrected verdict on %s: %w", episodeID, err)
		}
	}
	if req.Verdict != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE decision_episodes SET status = 'judged' WHERE id = $1::uuid AND status = 'decided'`, episodeID); err != nil {
			return nil, fmt.Errorf("strategystore: close episode %s: %w", episodeID, err)
		}
	}
	doc, err := readInference(ctx, tx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read inference of episode %s: %w", episodeID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("strategystore: commit verdict of episode %s: %w", episodeID, err)
	}
	s.log.InfoContext(ctx, "judgment verdict recorded", "episode_id", episodeID, "previous", verdict,
		"verdict", req.Verdict, "note", req.Note != "", "surface", req.Surface)
	return doc, nil
}

// applyVerdict writes the verdict and note. The inference row keeps the latest answer for reads; every
// accepted verdict and note is also appended to judgment_verdicts / judgment_notes, the append-only
// history (HAR-139, migration 0027). corrected to confirmed is refused: a correction is the human's
// replacement explanation and is not withdrawn by a click.
func applyVerdict(ctx context.Context, tx *sql.Tx, inferenceID, current string, req VerdictRequest) error {
	if req.Verdict == "confirmed" && current == "corrected" {
		return refuse("verdict_locked", "a corrected inference cannot be confirmed; correct it again instead")
	}
	if req.Verdict != "" {
		var statement any
		if req.Verdict == "corrected" {
			statement = req.CorrectedStatement
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO judgment_verdicts (judgment_inference_id, verdict, corrected_statement, surface, actor_person_id, actor_label)
VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6)`,
			inferenceID, req.Verdict, statement, req.Surface, req.ActorPersonID, req.ActorLabel); err != nil {
			return fmt.Errorf("strategystore: append verdict history of inference %s: %w", inferenceID, err)
		}
	}
	if req.Verdict != "" && !(req.Verdict == current && req.Verdict == "confirmed") {
		var statement any
		if req.Verdict == "corrected" {
			statement = req.CorrectedStatement
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE judgment_inferences SET human_verdict = $2, corrected_statement = $3, verdict_surface = $4, verdict_actor_label = $5,
  verdict_at = now() WHERE id = $1::uuid`, inferenceID, req.Verdict, statement, req.Surface, req.ActorLabel); err != nil {
			return fmt.Errorf("strategystore: record verdict on inference %s: %w", inferenceID, err)
		}
	}
	if req.Note != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE judgment_inferences SET human_note = $2 WHERE id = $1::uuid`, inferenceID, req.Note); err != nil {
			return fmt.Errorf("strategystore: record note on inference %s: %w", inferenceID, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO judgment_notes (judgment_inference_id, note, surface, actor_person_id, actor_label)
VALUES ($1::uuid, $2, $3, $4::uuid, $5)`,
			inferenceID, req.Note, req.Surface, req.ActorPersonID, req.ActorLabel); err != nil {
			return fmt.Errorf("strategystore: append note history of inference %s: %w", inferenceID, err)
		}
	}
	return nil
}

// episodeWorldTime is the decided episode's world time: the replay clock of the run that produced it, the
// newest trigger activity's occurred_at (ADR-0019).
func episodeWorldTime(ctx context.Context, tx *sql.Tx, episodeID string) (time.Time, error) {
	var ids []byte
	if err := tx.QueryRowContext(ctx, `SELECT to_jsonb(ar.trigger_activity_ids) FROM strategy_sets ss
 JOIN agent_runs ar ON ar.id = ss.agent_run_id WHERE ss.decision_episode_id = $1::uuid`, episodeID).Scan(&ids); err != nil {
		return time.Time{}, fmt.Errorf("strategystore: run of episode %s: %w", episodeID, err)
	}
	var triggers []string
	if err := json.Unmarshal(ids, &triggers); err != nil {
		return time.Time{}, fmt.Errorf("strategystore: decode trigger activities of episode %s: %w", episodeID, err)
	}
	at, err := evalinput.ReplayClock(ctx, tx, triggers)
	if err != nil {
		return time.Time{}, fmt.Errorf("strategystore: world time of episode %s: %w", episodeID, err)
	}
	return at, nil
}
