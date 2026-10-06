package bucket1load

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// loadBeliefs reads the episode's business-intelligence update: its summary and one belief per claim, each with
// the evidence refs the update cites.
func loadBeliefs(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	var summary string
	var claims []byte
	err := db.QueryRowContext(ctx, `SELECT b.summary, b.claims FROM decision_episodes e
 JOIN business_intelligence_updates b ON b.id = e.business_intelligence_update_id WHERE e.id = $1::uuid`, run.episodeID).Scan(&summary, &claims)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("bucket1load: read the business-intelligence update of episode %s: %w", run.episodeID, err)
	}
	var in []struct {
		Statement string          `json:"statement"`
		Dimension string          `json:"dimension"`
		Field     *string         `json:"state_diff_field"`
		Evidence  json.RawMessage `json:"evidence_refs"`
	}
	if err := json.Unmarshal(claims, &in); err != nil {
		return fmt.Errorf("bucket1load: decode update claims: %w", err)
	}
	for _, c := range in {
		kind := "summary"
		if c.Field != nil {
			if k, ok := beliefKinds[*c.Field]; ok {
				kind = k
			}
		}
		ep.Beliefs = append(ep.Beliefs, bucket1.Belief{Kind: kind, Statement: c.Statement, Refs: jsonRefs(c.Evidence)})
	}
	_ = summary
	return nil
}

var beliefKinds = map[string]string{
	"blockers": "blocker", "current_commitments": "commitment", "champion": "stakeholder", "economic_buyer": "stakeholder",
	"next_milestone": "next_decision", "next_meeting": "next_decision", "buying_group": "stakeholder", "relationship_state": "relationship",
}

// loadKnowledge reads the build_context step's real knowledge_attribution record, the knowledge it retrieved as of
// the replay clock and the situation the matcher saw.
func loadKnowledge(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	var stepID string
	var detail []byte
	err := db.QueryRowContext(ctx, `SELECT id::text, detail FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'build_context'`, run.id).Scan(&stepID, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("bucket1load: read the build_context step of run %s: %w", run.id, err)
	}
	att, err := bucket1.ParseAttribution(stepID, detail)
	if errors.Is(err, bucket1.ErrNoAttribution) {
		return nil // the run recorded none: B7 reads not measured and says why
	}
	if err != nil {
		return err
	}
	ep.Attribution = att
	for _, id := range att.Retrieved {
		k, err := knowledgestore.Get(ctx, db, id)
		if err != nil {
			return fmt.Errorf("bucket1load: read retrieved knowledge %s: %w", id, err)
		}
		ep.Knowledge = append(ep.Knowledge, k)
	}
	s, found, err := signalstore.SituationAt(ctx, db, run.accountID, att.AsOf, coalesce.WorldAsOf)
	if err != nil {
		return fmt.Errorf("bucket1load: situation of account %s: %w", run.accountID, err)
	}
	if found {
		ep.Situation = &s
	}
	return nil
}
