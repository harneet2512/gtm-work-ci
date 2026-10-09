package learning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/changedim"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Situation is the part of an episode's published situation a learned candidate is scoped to: the open
// StateTransition the episode recorded, or the account's confirmed relationship state when it had none.
type Situation struct {
	TransitionStatus string   // decision_episodes.transition_status ("" = no open transition)
	TransitionTo     string   // state_transition.to_state_candidate
	TransitionFrom   string   // state_transition.from_state
	Relationship     string   // newest CONFIRMED relationship state at the episode's time; "" = unknown
	Stage            string   // the opportunity stage in the episode's state version; "" = unknown
	Motion           string   // the deal motion in the episode's state version; "" = unknown
	Health           string   // the deal health; "" = unknown
	ChampionStatus   string   // the champion's status; "" = unknown
	SignalTypes      []string // the signal types of the diff that triggered the episode (sorted); empty = none known
	Topics           []string // the change dimensions of the material changes of the diff that triggered the episode (sorted); empty = unknown
	LearningScope    string   // decision_episodes.learning_scope: the D5 reading of how reusable the lesson is
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
	s.Relationship = knownValue(rel)
	for field, dst := range map[string]*string{"stage": &s.Stage, "motion": &s.Motion, "health": &s.Health,
		"champion_status": &s.ChampionStatus} {
		v, err := fieldAsOf(ctx, q, accountID, stateVersion, field)
		if err != nil {
			return s, err
		}
		*dst = knownValue(v)
	}
	if s.SignalTypes, err = triggerSignalTypes(ctx, q, episodeID); err != nil {
		return s, err
	}
	if s.Topics, err = episodeTopics(ctx, q, episodeID); err != nil {
		return s, err
	}
	if err := q.QueryRowContext(ctx, `SELECT learning_scope FROM decision_episodes WHERE id = $1::uuid`, episodeID).
		Scan(&s.LearningScope); err != nil {
		return s, fmt.Errorf("learning: learning scope of episode %s: %w", episodeID, err)
	}
	return s, nil
}

// knownValue is "" for an unknown value: the episode did not know it, so a lesson never scopes on it (ADR-0013
// amendment 2). The reducer writes the literal "unknown" for a state field it has no claim for.
func knownValue(v string) string {
	if t := strings.TrimSpace(v); !strings.EqualFold(t, "unknown") {
		return t
	}
	return ""
}

// fieldAsOf is one scalar state field in the account state at the episode's state version ("" when no history
// row covers it: the scope then simply does not constrain the field).
func fieldAsOf(ctx context.Context, q claimstore.DB, accountID string, version *int, field string) (string, error) {
	if version == nil {
		return "", nil
	}
	var v *string
	err := q.QueryRowContext(ctx, `SELECT state -> 'fields' -> $3::text ->> 'value' FROM state_history
 WHERE account_id = $1::uuid AND version <= $2 ORDER BY version DESC LIMIT 1`, accountID, *version, field).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && v == nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("learning: %s history of account %s: %w", field, accountID, err)
	}
	return *v, nil
}

// episodeTopics is what the episode's interpretation was about: the change dimensions of its triggering diff, from
// structured fields only (ADR-0013 amendment 4). An episode without a diff has no topic.
func episodeTopics(ctx context.Context, q claimstore.DB, episodeID string) ([]string, error) {
	var diffID *string
	err := q.QueryRowContext(ctx, `SELECT state_diff_id::text FROM decision_episodes WHERE id = $1::uuid`, episodeID).Scan(&diffID)
	if err != nil || diffID == nil {
		if err != nil {
			return nil, fmt.Errorf("learning: state diff of episode %s: %w", episodeID, err)
		}
		return nil, nil
	}
	return changedim.DiffTopics(ctx, q, *diffID)
}

// triggerSignalTypes lists, sorted, the signal types of the diff that triggered the episode: what happened.
func triggerSignalTypes(ctx context.Context, q claimstore.DB, episodeID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT sg.signal_type FROM decision_episodes de
 JOIN signals sg ON sg.state_diff_id = de.state_diff_id WHERE de.id = $1::uuid ORDER BY 1`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("learning: trigger signals of episode %s: %w", episodeID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
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
// conditions. The scope is only what the source episode actually knew (ADR-0013 amendment 2): the open
// transition and its endpoints, the confirmed relationship state, the stage, motion, health and champion status,
// and the signal types that triggered it. An unknown or empty value is never a condition, so a lesson never
// claims that a case must be unknown too. Reuse is by similarity over these features (knowledge.RetrieveAll),
// not by every condition holding. The signature is never empty (knowledge.Validate refuses that): an episode
// that knew none of the features is scoped on having a customer interaction, which compares with nothing.
func (s Situation) Signature() (sig, applicability []knowledge.Condition) {
	var all []knowledge.Condition
	eq := func(field, v string) {
		if v != "" {
			all = append(all, knowledge.Condition{Field: field, Op: "eq", Value: v})
		}
	}
	eq("transition.status", s.TransitionStatus)
	eq("transition.to_state", s.TransitionTo)
	eq("transition.from_state", s.TransitionFrom)
	eq("relationship_state", s.Relationship)
	eq("stage", s.Stage)
	eq("motion", s.Motion)
	eq("health", s.Health)
	eq("champion_status", s.ChampionStatus)
	for _, t := range s.SignalTypes {
		all = append(all, knowledge.Condition{Field: "diff." + t, Op: "exists"})
	}
	for _, d := range s.Topics {
		all = append(all, knowledge.Condition{Field: "topic." + d, Op: "exists"})
	}
	if len(all) == 0 {
		all = []knowledge.Condition{{Field: "last_customer_interaction", Op: "exists"}}
	}
	return all[:1], all[1:]
}
