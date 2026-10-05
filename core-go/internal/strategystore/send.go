package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/runs"
)

// Send is POST /runs/{run_id}/send: the explicit second action (HAR-129 section 10, HAR-139). In one
// transaction it re-evaluates the FINAL artifact — the selected candidate with the human's saved edits —
// through the deterministic evals at the run's replay clock, refuses the send when a blocking eval still
// fails (422 blocking_eval; the decision stays pending, so the human can edit again or discard), persists
// the send-time results, writes the HumanDelta when the artifact changed, records the HumanDecision
// (approve, edit or, on discard, reject), records the effect through the dry-run recording executor (nothing
// leaves the system), completes the human strategy decision and moves the episode to decided. A second call
// is 409 already_decided with the stored record, so a double click or a retry sends nothing twice.
// Generating the judgment inference is not part of this.
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
	run, err := s.checkSendable(ctx, tx, ep, runID, req, final)
	if err != nil {
		return nil, err
	}
	var sev sendEval
	if req.Decision == "send" {
		sev, err = s.evalFinal(ctx, tx, ep, run, runID, chosen, final)
		if err != nil {
			return nil, err
		}
		if blocked := blockingFailures(sev.Results); len(blocked) > 0 {
			// The refusal rolls the whole send transaction back; persist the fresh results separately so the
			// blocked final artifact is auditable and a retry re-derives the same content-hashed rows.
			_ = tx.Rollback()
			s.persistRefused(ctx, runID, sev.Results)
			return nil, refuse("blocking_eval", "%s", blockingReason(blocked))
		}
		if err := saveFinalResults(ctx, tx, sev.Results); err != nil {
			return nil, err
		}
		if err := linkSendResults(ctx, tx, saved.ID, sev); err != nil {
			return nil, err
		}
	}
	var deltaID string
	if req.Decision == "send" && saved.EditCount > 0 {
		if deltaID, err = s.writeDelta(ctx, tx, ep, runID, chosen, final, sev); err != nil {
			return nil, err
		}
	}
	if err := s.completeDecision(ctx, tx, ep, runID, req, saved, chosen, final, deltaID); err != nil {
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
		"candidate_id", chosen.ID, "edits", saved.EditCount, "human_delta_id", deltaID, "surface", req.Surface)
	return doc, nil
}

// checkSendable applies policy before anything is written: the actor exists, the run is waiting for a
// human, a send has someone to go to and the run mode can be executed here. It returns the locked run row.
// The eval gate is separate (evalFinal): it judges the final artifact fresh, not the candidate's stored
// bundle — an edit can repair a blocking failure, and no edit can bypass evaluation.
func (s *Service) checkSendable(ctx context.Context, tx *sql.Tx, ep episode, runID string, req SendRequest, final Draft) (sendRun, error) {
	if err := checkPeople(ctx, tx, ep.AccountID, Draft{}, req.ActorPersonID); err != nil {
		return sendRun{}, err
	}
	var run sendRun
	var opp sql.NullString
	var ids []byte
	if err := tx.QueryRowContext(ctx, `SELECT run_mode, status, opportunity_id::text, to_jsonb(trigger_activity_ids)
FROM agent_runs WHERE id = $1::uuid FOR UPDATE`, runID).Scan(&run.Mode, &run.Status, &opp, &ids); err != nil {
		return run, fmt.Errorf("strategystore: read run %s: %w", runID, err)
	}
	if err := json.Unmarshal(ids, &run.TriggerIDs); err != nil {
		return run, fmt.Errorf("strategystore: decode trigger activities of run %s: %w", runID, err)
	}
	run.OpportunityID = opp.String
	if run.Status != "awaiting_human" && run.Status != "drafted" {
		return run, refuse("run_not_awaiting", "the run is %s, not waiting for a human decision", run.Status)
	}
	var decided bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM human_decisions WHERE agent_run_id = $1::uuid)`, runID).Scan(&decided); err != nil {
		return run, fmt.Errorf("strategystore: check human decision of run %s: %w", runID, err)
	}
	if decided {
		return run, refuse("run_already_decided", "the run already has a human decision recorded elsewhere")
	}
	if req.Decision == "discard" {
		return run, nil
	}
	if run.Mode != runs.DryRun {
		return run, refuse("live_send_unavailable", "this run is live and core has no sender configured; only dry runs are executed here")
	}
	if len(final.To) == 0 {
		return run, refuse("no_recipient", "the action has no recipient to send to")
	}
	return run, nil
}

// completeDecision writes everything a send or discard changes. It runs after the send-time evaluation
// inside the episode lock, so each statement can rely on the run being awaiting a human and the decision
// pending. deltaID is the episode's HumanDelta (set when the human edited the artifact, "" otherwise).
func (s *Service) completeDecision(ctx context.Context, tx *sql.Tx, ep episode, runID string, req SendRequest, saved *savedDecision, chosen candidate, final Draft, deltaID string) error {
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
  human_final_artifact = $5::jsonb, human_delta_id = $6::uuid WHERE id = $1::uuid`,
		ep.ID, humanDecisionID, action, chosen.DraftIndex, editedArtifact, nullable(deltaID)); err != nil {
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
