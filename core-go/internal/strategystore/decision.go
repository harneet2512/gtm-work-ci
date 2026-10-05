package strategystore

import (
	"context"
	"database/sql"
	"fmt"
)

// RecordDecision is POST /runs/{run_id}/strategy-decision. The first call records the choice (created is
// true); later calls with the same candidate save edits or, with none, return the stored decision unchanged.
// A different candidate after the choice is selection_locked and anything after Send or Discard is
// already_decided. Choosing never sends.
func (s *Service) RecordDecision(ctx context.Context, runID string, req DecisionRequest) (doc []byte, created bool, err error) {
	if !IsUUID(runID) {
		return nil, false, &NotFoundError{Code: "not_found"}
	}
	if err := req.Validate(); err != nil {
		return nil, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("strategystore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	ep, err := s.lockEpisode(ctx, tx, runID)
	if err != nil {
		return nil, false, err
	}
	cands, err := loadCandidates(ctx, tx, ep.SetID)
	if err != nil {
		return nil, false, err
	}
	chosen, ok := cands[req.SelectedCandidateID]
	if !ok {
		return nil, false, refuse("unknown_candidate", "the candidate is not in this run's strategy set")
	}
	saved, err := lockDecision(ctx, tx, ep.ID)
	if err != nil {
		return nil, false, err
	}
	if saved == nil {
		created = true
		err = s.createDecision(ctx, tx, runID, ep, cands, chosen, req)
	} else {
		err = s.updateDecision(ctx, tx, runID, ep, chosen, saved, req)
	}
	if err != nil {
		return nil, false, err
	}
	if doc, err = readDecision(ctx, tx, runID); err != nil {
		return nil, false, fmt.Errorf("strategystore: read decision of run %s: %w", runID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("strategystore: commit decision of run %s: %w", runID, err)
	}
	s.log.InfoContext(ctx, "strategy decision recorded", "run_id", runID, "episode_id", ep.ID,
		"candidate_id", chosen.ID, "created", created, "edited", req.HasEdits(), "surface", req.Surface)
	return doc, created, nil
}

func (s *Service) createDecision(ctx context.Context, tx *sql.Tx, runID string, ep episode, cands map[string]candidate, chosen candidate, req DecisionRequest) error {
	if ep.Status != "awaiting_choice" {
		return &ConflictError{Code: CodeAlreadyDecided}
	}
	var preferred string
	for id, c := range cands {
		if c.Preferred {
			preferred = id
		}
	}
	if preferred == "" {
		return refuse("no_preference", "the strategy set has no preferred candidate")
	}
	final := overlay(chosen.Draft, req)
	if err := checkPeople(ctx, tx, ep.AccountID, final, req.ActorPersonID); err != nil {
		return err
	}
	to, cc, art, edits, err := encodeFinal(chosen.Draft, final, req.HasEdits())
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO human_strategy_decisions (decision_episode_id, agent_run_id, strategy_set_id, selected_candidate_id,
  original_agent_preference, surface, actor_person_id, actor_label, chosen_at, final_to, final_cc, final_artifact, edits)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7::uuid, $8, now(), $9::jsonb, $10::jsonb, $11::jsonb, $12::jsonb)`,
		ep.ID, runID, ep.SetID, chosen.ID, preferred, req.Surface, req.ActorPersonID, req.ActorLabel, to, cc, art, string(edits)); err != nil {
		return fmt.Errorf("strategystore: insert decision of run %s: %w", runID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decision_episodes SET status = 'chosen' WHERE id = $1::uuid`, ep.ID); err != nil {
		return fmt.Errorf("strategystore: move episode %s to chosen: %w", ep.ID, err)
	}
	return nil
}

func (s *Service) updateDecision(ctx context.Context, tx *sql.Tx, runID string, ep episode, chosen candidate, saved *savedDecision, req DecisionRequest) error {
	if saved.SendDecision != "pending" {
		return conflictWithRecord(ctx, tx, runID, CodeAlreadyDecided)
	}
	if saved.Selected != chosen.ID {
		return &ConflictError{Code: CodeSelectionLocked}
	}
	if !req.HasEdits() {
		return nil // the same choice again: the stored decision, unchanged
	}
	base := chosen.Draft
	if saved.Final != nil {
		base = *saved.Final
	}
	final := overlay(base, req)
	if err := checkPeople(ctx, tx, ep.AccountID, final, nil); err != nil {
		return err
	}
	to, cc, art, edits, err := encodeFinal(chosen.Draft, final, true)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE human_strategy_decisions SET final_to = $2::jsonb, final_cc = $3::jsonb, final_artifact = $4::jsonb, edits = $5::jsonb
WHERE id = $1::uuid`, saved.ID, to, cc, art, string(edits)); err != nil {
		return fmt.Errorf("strategystore: save edits of run %s: %w", runID, err)
	}
	return nil
}

// encodeFinal renders the final draft and its edits against the candidate for the jsonb columns. Without
// edits the final_* columns stay NULL and edits is [].
func encodeFinal(candidate, final Draft, edited bool) (to, cc, art any, edits []byte, err error) {
	if !edited {
		return nil, nil, nil, []byte("[]"), nil
	}
	if edits, err = marshal(ComputeEdits(candidate, final)); err != nil {
		return nil, nil, nil, nil, err
	}
	var tb, cb, ab []byte
	for _, e := range []struct {
		v   any
		dst *[]byte
	}{{nonNil(final.To), &tb}, {nonNil(final.CC), &cb}, {final.Artifact, &ab}} {
		if *e.dst, err = marshal(e.v); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return string(tb), string(cb), string(ab), edits, nil
}

func nonNil(r []Recipient) []Recipient {
	if r == nil {
		return []Recipient{}
	}
	return r
}
