package strategystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/runs"
)

// Send is POST /runs/{run_id}/send: the explicit second action. In one transaction it records the
// HumanDecision (approve when nothing was edited, edit when something was, reject on discard), records the
// effect through the dry-run recording executor (nothing leaves the system), completes the human strategy
// decision and moves the episode to decided. A second call is 409 already_decided with the stored record,
// so a double click or a retry sends nothing twice. Generating the judgment inference is not part of this.
func (s *Service) Send(ctx context.Context, runID string, req SendRequest) ([]byte, error) {
	if !IsUUID(runID) {
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

	ep, err := s.lockEpisode(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	saved, err := lockDecision(ctx, tx, ep.ID)
	if err != nil {
		return nil, err
	}
	switch {
	case saved == nil:
		return nil, &ConflictError{Code: CodeNoChoice}
	case saved.SendDecision != "pending":
		return nil, conflictWithRecord(ctx, tx, runID, CodeAlreadyDecided)
	}
	cands, err := loadCandidates(ctx, tx, ep.SetID)
	if err != nil {
		return nil, err
	}
	chosen, ok := cands[saved.Selected]
	if !ok {
		return nil, fmt.Errorf("strategystore: selected candidate %s is not in set %s", saved.Selected, ep.SetID)
	}
	final := chosen.Draft
	if saved.Final != nil {
		final = *saved.Final
	}
	if err := s.checkSendable(ctx, tx, ep, runID, req, chosen, final); err != nil {
		return nil, err
	}
	if err := s.completeDecision(ctx, tx, ep, runID, req, saved, chosen, final); err != nil {
		return nil, err
	}
	doc, err := readDecision(ctx, tx, runID)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read decision of run %s: %w", runID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("strategystore: commit send of run %s: %w", runID, err)
	}
	s.log.InfoContext(ctx, "send decision recorded", "run_id", runID, "episode_id", ep.ID, "decision", req.Decision,
		"candidate_id", chosen.ID, "edits", saved.EditCount, "surface", req.Surface)
	return doc, nil
}

// checkSendable applies policy before anything is written: the actor exists, the run is waiting for a human,
// a send has someone to go to, the run mode can be executed here and no blocking eval of the candidate failed.
func (s *Service) checkSendable(ctx context.Context, tx *sql.Tx, ep episode, runID string, req SendRequest, chosen candidate, final Draft) error {
	if err := checkPeople(ctx, tx, ep.AccountID, Draft{}, req.ActorPersonID); err != nil {
		return err
	}
	var mode, status string
	if err := tx.QueryRowContext(ctx, `SELECT run_mode, status FROM agent_runs WHERE id = $1::uuid FOR UPDATE`, runID).Scan(&mode, &status); err != nil {
		return fmt.Errorf("strategystore: read run %s: %w", runID, err)
	}
	if status != "awaiting_human" && status != "drafted" {
		return refuse("run_not_awaiting", "the run is %s, not waiting for a human decision", status)
	}
	var decided bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM human_decisions WHERE agent_run_id = $1::uuid)`, runID).Scan(&decided); err != nil {
		return fmt.Errorf("strategystore: check human decision of run %s: %w", runID, err)
	}
	if decided {
		return refuse("run_already_decided", "the run already has a human decision recorded elsewhere")
	}
	if req.Decision == "discard" {
		return nil
	}
	if mode != runs.DryRun {
		return refuse("live_send_unavailable", "this run is live and core has no sender configured; only dry runs are executed here")
	}
	if len(final.To) == 0 {
		return refuse("no_recipient", "the action has no recipient to send to")
	}
	var blocked bool
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(jsonb_path_exists(b.items, '$[*] ? (@.verdict == "fail" && @.result.blocking == true)'), false)
FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id WHERE c.id = $1::uuid`, chosen.ID).Scan(&blocked); err != nil {
		return fmt.Errorf("strategystore: read evals of candidate %s: %w", chosen.ID, err)
	}
	if blocked {
		return refuse("blocking_eval", "a blocking eval of this candidate failed; the action cannot be sent until it is resolved")
	}
	return nil
}

// completeDecision writes everything a send or discard changes. It runs after checkSendable inside the
// episode lock, so each statement can rely on the run being awaiting a human and the decision pending.
func (s *Service) completeDecision(ctx context.Context, tx *sql.Tx, ep episode, runID string, req SendRequest, saved *savedDecision, chosen candidate, final Draft) error {
	edited := saved.EditCount > 0
	decision, action, runStatus := "reject", "REJECT", "rejected"
	if req.Decision == "send" {
		decision, action, runStatus = "approve", "APPROVE_UNCHANGED", "approved"
		if edited {
			decision, action, runStatus = "edit", "APPROVE_WITH_EDIT", "edited"
		}
	}
	art, err := marshal(final.Artifact)
	if err != nil {
		return err
	}
	var editedArtifact any
	if decision == "edit" {
		editedArtifact = string(art)
	}
	var humanDecisionID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO human_decisions (agent_run_id, decision, surface, actor_person_id, actor_label, edited_artifact)
VALUES ($1::uuid, $2, $3, $4::uuid, $5, $6::jsonb) RETURNING id::text`,
		runID, decision, req.Surface, req.ActorPersonID, req.ActorLabel, editedArtifact).Scan(&humanDecisionID); err != nil {
		return fmt.Errorf("strategystore: record human decision of run %s: %w", runID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = $2, updated_at = now() WHERE id = $1::uuid`, runID, runStatus); err != nil {
		return fmt.Errorf("strategystore: mark run %s %s: %w", runID, runStatus, err)
	}
	if req.Decision == "send" {
		if err := s.recordEffect(ctx, tx, runID, chosen, final); err != nil {
			return err
		}
		if err := storeFinal(ctx, tx, saved.ID, final); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE human_strategy_decisions SET send_decision = $2, send_decided_at = now(), human_decision_id = $3::uuid WHERE id = $1::uuid`,
		saved.ID, req.Decision, humanDecisionID); err != nil {
		return fmt.Errorf("strategystore: complete decision %s: %w", saved.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE decision_episodes SET status = 'decided', human_decision_id = $2::uuid, human_action = $3, final_draft_index = $4,
  human_final_artifact = $5::jsonb WHERE id = $1::uuid`, ep.ID, humanDecisionID, action, chosen.DraftIndex, editedArtifact); err != nil {
		return fmt.Errorf("strategystore: move episode %s to decided: %w", ep.ID, err)
	}
	return nil
}

// recordEffect records the send through the dry-run recording executor of WP8. The recorded effect is what
// would have been sent; no connector is reachable from it.
func (s *Service) recordEffect(ctx context.Context, tx *sql.Tx, runID string, chosen candidate, final Draft) error {
	ex, err := runs.NewExecutor(runs.DryRun, nil)
	if err != nil {
		return fmt.Errorf("strategystore: dry-run executor: %w", err)
	}
	body, err := marshal(map[string]any{"to": final.To, "cc": final.CC, "artifact": final.Artifact})
	if err != nil {
		return err
	}
	effect := runs.Effect{Kind: chosen.ActionType, Target: final.To[0].PersonID, Body: body}
	if _, err := runs.RecordDryRunTx(ctx, tx, ex, runID, effect); err != nil {
		if errors.Is(err, runs.ErrAlreadyExecuted) || errors.Is(err, runs.ErrNotRecordable) {
			return refuse("not_recordable", "the run's execute step cannot be recorded: %v", err)
		}
		return fmt.Errorf("strategystore: record effect of run %s: %w", runID, err)
	}
	return nil
}

// storeFinal writes the sent recipients and artifact (an unedited send stores the candidate's).
func storeFinal(ctx context.Context, tx *sql.Tx, decisionID string, final Draft) error {
	to, cc, art, _, err := encodeFinal(Draft{}, final, true)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE human_strategy_decisions SET final_to = $2::jsonb, final_cc = $3::jsonb, final_artifact = $4::jsonb WHERE id = $1::uuid`,
		decisionID, to, cc, art); err != nil {
		return fmt.Errorf("strategystore: store final artifact of decision %s: %w", decisionID, err)
	}
	return nil
}
