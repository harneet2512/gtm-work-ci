package strategystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// applyVerdict writes the verdict and note. corrected to confirmed is refused: a correction is the human's
// replacement explanation and is not withdrawn by a click.
func applyVerdict(ctx context.Context, tx *sql.Tx, inferenceID, current string, req VerdictRequest) error {
	if req.Verdict == "confirmed" && current == "corrected" {
		return refuse("verdict_locked", "a corrected inference cannot be confirmed; correct it again instead")
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
	}
	return nil
}
