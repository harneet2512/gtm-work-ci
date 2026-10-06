package reactions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SupervisionDoc is Supervision marshaled for GET /episodes/{episode_id}/reactions.
func (s *Service) SupervisionDoc(ctx context.Context, episodeID string) ([]byte, error) {
	sup, err := s.Supervision(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	doc, err := json.Marshal(sup)
	if err != nil {
		return nil, fmt.Errorf("reactions: encode supervision: %w", err)
	}
	return doc, nil
}

// Supervision returns everything the episode's supervision recorded — customer_reactions then
// business_outcomes, each in stored order. An unknown (or malformed) episode id is ErrNotFound; a
// known episode with nothing detected returns empty arrays, not an error.
func (s *Service) Supervision(ctx context.Context, episodeID string) (Supervision, error) {
	out := Supervision{CustomerReactions: []CustomerReaction{}, BusinessOutcomes: []BusinessOutcome{}}
	if !uuidPattern.MatchString(episodeID) {
		return out, fmt.Errorf("%w: decision episode %s", ErrNotFound, episodeID)
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM decision_episodes WHERE id = $1::uuid)`, episodeID).Scan(&exists); err != nil {
		return out, fmt.Errorf("reactions: episode lookup: %w", err)
	}
	if !exists {
		return out, fmt.Errorf("%w: decision episode %s", ErrNotFound, episodeID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, account_id::text, agent_run_id::text,
	       activity_id::text, reaction_type, polarity, evidence_refs::text, created_at
	FROM customer_reactions WHERE decision_episode_id = $1::uuid ORDER BY created_at, id`, episodeID)
	if err != nil {
		return out, fmt.Errorf("reactions: load reactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r CustomerReaction
		var refs string
		if err := rows.Scan(&r.ID, &r.AccountID, &r.AgentRunID, &r.ActivityID, &r.ReactionType,
			&r.Polarity, &refs, &r.CreatedAt); err != nil {
			return out, fmt.Errorf("reactions: reaction scan: %w", err)
		}
		r.EvidenceRefs = []byte(refs)
		out.CustomerReactions = append(out.CustomerReactions, r)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("reactions: reaction rows: %w", err)
	}
	orows, err := s.db.QueryContext(ctx, `SELECT id::text, account_id::text, agent_run_id::text,
	       opportunity_id::text, activity_id::text, outcome_type, value::text, evidence_refs::text, occurred_at
	FROM business_outcomes WHERE decision_episode_id = $1::uuid ORDER BY occurred_at, id`, episodeID)
	if err != nil {
		return out, fmt.Errorf("reactions: load outcomes: %w", err)
	}
	defer orows.Close()
	for orows.Next() {
		var o BusinessOutcome
		var value, refs string
		if err := orows.Scan(&o.ID, &o.AccountID, &o.AgentRunID, &o.OpportunityID, &o.ActivityID,
			&o.OutcomeType, &value, &refs, &o.OccurredAt); err != nil {
			return out, fmt.Errorf("reactions: outcome scan: %w", err)
		}
		o.Value = []byte(value)
		o.EvidenceRefs = []byte(refs)
		out.BusinessOutcomes = append(out.BusinessOutcomes, o)
	}
	if err := orows.Err(); err != nil {
		return out, fmt.Errorf("reactions: outcome rows: %w", err)
	}
	return out, nil
}

// ReactionsForRun is the trace read: the run's reactions in stored order.
func ReactionsForRun(ctx context.Context, q querier, runID string) ([]CustomerReaction, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text, account_id::text, agent_run_id::text,
	       activity_id::text, reaction_type, polarity, evidence_refs::text, created_at
	FROM customer_reactions WHERE agent_run_id = $1::uuid ORDER BY created_at, id`, runID)
	if err != nil {
		return nil, fmt.Errorf("reactions: run reactions: %w", err)
	}
	defer rows.Close()
	out := []CustomerReaction{}
	for rows.Next() {
		var r CustomerReaction
		var refs string
		if err := rows.Scan(&r.ID, &r.AccountID, &r.AgentRunID, &r.ActivityID, &r.ReactionType,
			&r.Polarity, &refs, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("reactions: run reaction scan: %w", err)
		}
		r.EvidenceRefs = []byte(refs)
		out = append(out, r)
	}
	return out, rows.Err()
}

// querier is *sql.DB or *sql.Tx — the trace loader runs inside the read tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
