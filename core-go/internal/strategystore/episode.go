package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// episode is the locked decision episode of a run and its strategy set.
type episode struct {
	ID, SetID, AccountID, Status string
}

// lockEpisode row-locks the run's episode so decisions on one run are serialised: a double click, a retry
// and the web and Slack surfaces all queue here.
func (s *Service) lockEpisode(ctx context.Context, tx *sql.Tx, runID string) (episode, error) {
	var e episode
	err := tx.QueryRowContext(ctx, `
SELECT de.id::text, ss.id::text, ss.account_id::text, de.status
FROM strategy_sets ss JOIN decision_episodes de ON de.id = ss.decision_episode_id
WHERE ss.agent_run_id = $1::uuid FOR UPDATE OF de`, runID).Scan(&e.ID, &e.SetID, &e.AccountID, &e.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return e, s.runMissing(ctx, runID, CodeNotReady)
	}
	if err != nil {
		return e, fmt.Errorf("strategystore: lock episode of run %s: %w", runID, err)
	}
	return e, nil
}

// candidate is the part of a stored strategy candidate a decision needs.
type candidate struct {
	ID         string
	Preferred  bool
	DraftIndex int
	ActionType string
	Draft      Draft
}

func loadCandidates(ctx context.Context, tx *sql.Tx, setID string) (map[string]candidate, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id::text, preferred_by_agent, draft_index, action_type, to_recipients, cc_recipients, full_action_artifact
FROM strategy_candidates WHERE strategy_set_id = $1::uuid`, setID)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read candidates of set %s: %w", setID, err)
	}
	defer rows.Close()
	out := map[string]candidate{}
	for rows.Next() {
		var c candidate
		var to, cc, art []byte
		if err := rows.Scan(&c.ID, &c.Preferred, &c.DraftIndex, &c.ActionType, &to, &cc, &art); err != nil {
			return nil, fmt.Errorf("strategystore: scan candidate: %w", err)
		}
		for _, d := range []struct {
			raw []byte
			dst any
		}{{to, &c.Draft.To}, {cc, &c.Draft.CC}, {art, &c.Draft.Artifact}} {
			if err := json.Unmarshal(d.raw, d.dst); err != nil {
				return nil, fmt.Errorf("strategystore: decode candidate %s: %w", c.ID, err)
			}
		}
		out[c.ID] = c
	}
	return out, rows.Err()
}

// savedDecision is a stored HumanStrategyDecision row, locked.
type savedDecision struct {
	ID, Selected, SendDecision string
	EditCount                  int
	Final                      *Draft // nil until the human has saved edits
}

func lockDecision(ctx context.Context, tx *sql.Tx, episodeID string) (*savedDecision, error) {
	var d savedDecision
	var to, cc, art []byte
	err := tx.QueryRowContext(ctx, `
SELECT id::text, selected_candidate_id::text, send_decision, jsonb_array_length(edits), final_to, final_cc, final_artifact
FROM human_strategy_decisions WHERE decision_episode_id = $1::uuid FOR UPDATE`, episodeID).Scan(&d.ID, &d.Selected, &d.SendDecision, &d.EditCount, &to, &cc, &art)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("strategystore: lock decision of episode %s: %w", episodeID, err)
	}
	if art != nil {
		d.Final = &Draft{}
		for _, p := range []struct {
			raw []byte
			dst any
		}{{to, &d.Final.To}, {cc, &d.Final.CC}, {art, &d.Final.Artifact}} {
			if p.raw == nil {
				continue
			}
			if err := json.Unmarshal(p.raw, p.dst); err != nil {
				return nil, fmt.Errorf("strategystore: decode decision %s: %w", d.ID, err)
			}
		}
	}
	return &d, nil
}

// overlay returns base with the fields req carries replaced; nothing is mutated.
func overlay(base Draft, req DecisionRequest) Draft {
	out := Draft{To: slices.Clone(base.To), CC: slices.Clone(base.CC), Artifact: base.Artifact}
	if req.FinalTo != nil {
		out.To = slices.Clone(*req.FinalTo)
	}
	if req.FinalCC != nil {
		out.CC = slices.Clone(*req.FinalCC)
	}
	if req.FinalArtifact != nil {
		out.Artifact = *req.FinalArtifact
	}
	return out
}

// checkPeople refuses a recipient or actor that is not a person core knows: an employee, or a contact of
// the run's account. Ids from a Slack payload are untrusted.
func checkPeople(ctx context.Context, tx *sql.Tx, accountID string, d Draft, actor *string) error {
	var ids []string
	for _, r := range slices.Concat(d.To, d.CC) {
		if !slices.Contains(ids, r.PersonID) {
			ids = append(ids, r.PersonID)
		}
	}
	if len(ids) > 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM people WHERE id = ANY($1::uuid[]) AND merged_into IS NULL AND (kind = 'employee' OR account_id = $2::uuid)`,
			signalstore.UUIDArray(ids), accountID).Scan(&n); err != nil {
			return fmt.Errorf("strategystore: check recipients: %w", err)
		}
		if n != len(ids) {
			return refuse("unknown_recipient", "a recipient is not a person of this account or of the company")
		}
	}
	if actor != nil {
		var known bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE id = $1::uuid)`, *actor).Scan(&known); err != nil {
			return fmt.Errorf("strategystore: check actor: %w", err)
		}
		if !known {
			return refuse("unknown_actor", "actor_person_id is not a known person")
		}
	}
	return nil
}

func marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("strategystore: encode: %w", err)
	}
	return b, nil
}

// conflictWithRecord is a 409 that carries the stored decision.
func conflictWithRecord(ctx context.Context, tx *sql.Tx, runID, code string) error {
	doc, err := readDecision(ctx, tx, runID)
	if err != nil {
		return fmt.Errorf("strategystore: read decision of run %s: %w", runID, err)
	}
	return &ConflictError{Code: code, Decision: doc}
}
