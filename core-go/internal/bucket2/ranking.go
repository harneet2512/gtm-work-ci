package bucket2

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Ranking returns the stored DecisionRanking of an episode: the order, the tier inputs and the reason for every adjacent
// pair (decision_rankings, migration 0036). It is what D3 judges, so the inspector can show the rationale itself. An episode
// with no stored ranking is ErrNotFound: nothing is invented for it.
func (r *Reader) Ranking(ctx context.Context, id string) ([]byte, error) {
	if !episodeID.MatchString(id) {
		return nil, ErrInvalid
	}
	var setID, preferred, tiers, reasons string
	var order []byte
	var abstained bool
	var model sql.NullString
	err := r.db.QueryRowContext(ctx, `
SELECT strategy_set_id::text, preferred_candidate_id::text, to_json(order_ids::text[])::text, tier_inputs::text, pairwise_reasons::text, abstained, model
FROM decision_rankings WHERE decision_episode_id = $1::uuid`, id).Scan(&setID, &preferred, &order, &tiers, &reasons, &abstained, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bucket2: ranking of %s: %w", id, err)
	}
	if reasons == "null" {
		reasons = "[]"
	}
	doc := map[string]any{"episode_id": id, "strategy_set_id": setID, "order": json.RawMessage(order), "preferred_candidate_id": preferred,
		"tier_inputs": json.RawMessage(tiers), "reasons": json.RawMessage(reasons), "abstained": abstained, "model": nil}
	if model.Valid {
		doc["model"] = model.String
	}
	return json.Marshal(doc)
}
