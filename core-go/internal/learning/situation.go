package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Situation is the part of an episode's published situation a learned candidate is scoped to: the open
// StateTransition the episode recorded, or the account's confirmed relationship state when it had none.
type Situation struct {
	TransitionStatus string // decision_episodes.transition_status ("" = no open transition)
	TransitionTo     string // state_transition.to_state_candidate
	TransitionFrom   string // state_transition.from_state
	Relationship     string // newest CONFIRMED relationship state at the episode's time; "" = unknown
	Stage            string // the opportunity stage in the episode's state version; "" = unknown
	LearningScope    string // decision_episodes.learning_scope: the D5 reading of how reusable the lesson is
}

// EpisodeSituation reads the situation a decision episode stored at publish (its transition_status and
// the open transition's endpoints), plus the account's confirmed relationship state AT THE EPISODE's
// state version — the as-of semantics a replayable loop needs, not the current projection.
func EpisodeSituation(ctx context.Context, q claimstore.DB, episodeID string) (Situation, error) {
	var s Situation
	var raw []byte
	var accountID string
	var stateVersion *int
	err := q.QueryRowContext(ctx, `SELECT COALESCE(transition_status, ''), COALESCE(state_transition::text, ''),
 account_id::text, state_version FROM decision_episodes WHERE id = $1::uuid`, episodeID).
		Scan(&s.TransitionStatus, &raw, &accountID, &stateVersion)
	if err != nil {
		return s, fmt.Errorf("learning: situation of episode %s: %w", episodeID, err)
	}
	var t struct {
		FromState        string  `json:"from_state"`
		ToStateCandidate *string `json:"to_state_candidate"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &t); err == nil {
			s.TransitionFrom = t.FromState
			if t.ToStateCandidate != nil {
				s.TransitionTo = *t.ToStateCandidate
			}
		}
	}
	rel, err := relationshipAsOf(ctx, q, accountID, stateVersion)
	if err != nil {
		return s, err
	}
	s.Relationship = rel
	if s.Stage, err = stageAsOf(ctx, q, accountID, stateVersion); err != nil {
		return s, err
	}
	if err := q.QueryRowContext(ctx, `SELECT learning_scope FROM decision_episodes WHERE id = $1::uuid`, episodeID).
		Scan(&s.LearningScope); err != nil {
		return s, fmt.Errorf("learning: learning scope of episode %s: %w", episodeID, err)
	}
	return s, nil
}

// stageAsOf is the opportunity stage in the account state at the episode's state version ("" when no
// history row covers it: the scope then simply does not constrain the stage).
func stageAsOf(ctx context.Context, q claimstore.DB, accountID string, version *int) (string, error) {
	if version == nil {
		return "", nil
	}
	var stage *string
	err := q.QueryRowContext(ctx, `SELECT state -> 'fields' -> 'stage' ->> 'value' FROM state_history
 WHERE account_id = $1::uuid AND version <= $2 ORDER BY version DESC LIMIT 1`, accountID, *version).Scan(&stage)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && stage == nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("learning: stage history of account %s: %w", accountID, err)
	}
	return *stage, nil
}

// relationshipAsOf is the newest confirmed relationship state at the episode's state version; it falls
// back to the current projection when no history row covers the version (a synthetic fixture).
func relationshipAsOf(ctx context.Context, q claimstore.DB, accountID string, version *int) (string, error) {
	var rel *string
	var err error
	if version != nil {
		err = q.QueryRowContext(ctx, `SELECT state -> 'relationship_state' ->> 'value' FROM state_history
 WHERE account_id = $1::uuid AND version <= $2 ORDER BY version DESC LIMIT 1`, accountID, *version).Scan(&rel)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("learning: relationship history of account %s: %w", accountID, err)
		}
	}
	if rel == nil {
		err = q.QueryRowContext(ctx, `SELECT state -> 'relationship_state' ->> 'value'
 FROM account_state WHERE account_id = $1::uuid`, accountID).Scan(&rel)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("learning: relationship state of account %s: %w", accountID, err)
		}
	}
	if rel == nil {
		return "", nil
	}
	return *rel, nil
}

// WithRelationship returns s with the relationship scope the caller already computed (the send-time
// re-evaluation reads the same account state at the replay clock, so no second query is needed).
func (s Situation) WithRelationship(rel string) Situation {
	s.Relationship = rel
	return s
}

// Signature is the situation_signature a learned knowledge carries and its narrowing applicability
// conditions. The scope is the episode's state, never transition.status alone: one human edit plus one
// reply is one observation, so the knowledge applies only where the transition status, its endpoints, the
// confirmed relationship state and the opportunity stage all match (HAR-97 B9; knowledge.ScopeTooBroad).
// The signature is never empty, which knowledge.Validate refuses.
func (s Situation) Signature() (sig, applicability []knowledge.Condition) {
	switch {
	case s.TransitionStatus != "":
		sig = []knowledge.Condition{{Field: "transition.status", Op: "eq", Value: s.TransitionStatus}}
		if s.TransitionTo != "" {
			applicability = append(applicability, knowledge.Condition{Field: "transition.to_state", Op: "eq", Value: s.TransitionTo})
		}
		if s.TransitionFrom != "" {
			applicability = append(applicability, knowledge.Condition{Field: "transition.from_state", Op: "eq", Value: s.TransitionFrom})
		}
		if s.Relationship != "" {
			applicability = append(applicability, knowledge.Condition{Field: "relationship_state", Op: "eq", Value: s.Relationship})
		}
	case s.Relationship != "":
		sig = []knowledge.Condition{{Field: "relationship_state", Op: "eq", Value: s.Relationship}}
	default:
		sig = []knowledge.Condition{{Field: "relationship_state", Op: "is_unknown"}}
	}
	if s.Stage != "" {
		applicability = append(applicability, knowledge.Condition{Field: "stage", Op: "eq", Value: s.Stage})
	}
	return sig, applicability
}
