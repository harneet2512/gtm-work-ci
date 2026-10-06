package contracttest

import (
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// HAR-149: the stored DecisionRanking is readable, so the inspector can show why one candidate ranked above another.
func TestEpisodeRankingIsServedFromTheStoredRow(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	op := "/episodes/{episode_id}/ranking"
	path := "/episodes/" + seed.EpisodeID + "/ranking"

	if r := s.get(path, op); r.status != 404 {
		t.Fatalf("no stored ranking must be 404, got %d %s", r.status, clip(r.body))
	}

	a, b, c := seed.Candidates[0], seed.Candidates[1], seed.Candidates[2]
	tiers := `[{"candidate_id":"` + a + `","blocking":false,"restricted":false,"worker_rank":1},{"candidate_id":"` + b + `","blocking":false,"restricted":false,"worker_rank":2},{"candidate_id":"` + c + `","blocking":true,"restricted":false,"worker_rank":3}]`
	reasons := `[{"ranked_higher_id":"` + a + `","ranked_lower_id":"` + b + `","reason":"fits the state better","evidence_refs":["activity:` + seed.ActivityID + `"],"knowledge_refs":[]},` +
		`{"ranked_higher_id":"` + b + `","ranked_lower_id":"` + c + `","reason":"the third is blocked","evidence_refs":[],"knowledge_refs":[]}]`
	if _, err := env.DB.Exec(`INSERT INTO decision_rankings (strategy_set_id, decision_episode_id, agent_run_id, order_ids, preferred_candidate_id, tier_inputs, pairwise_reasons, abstained, model)
VALUES ($1::uuid, $2::uuid, $3::uuid, ARRAY[$4, $5, $6]::uuid[], $4::uuid, $7::jsonb, $8::jsonb, false, 'm')`,
		seed.SetID, seed.EpisodeID, seed.RunID, a, b, c, tiers, reasons); err != nil {
		t.Fatal(err)
	}
	r := s.get(path, op)
	var doc struct {
		Order     []string `json:"order"`
		Preferred string   `json:"preferred_candidate_id"`
		Reasons   []struct {
			Higher string `json:"ranked_higher_id"`
			Reason string `json:"reason"`
		} `json:"reasons"`
		Model string `json:"model"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &doc) != nil {
		t.Fatalf("ranking: %d %s", r.status, clip(r.body))
	}
	if len(doc.Order) != 3 || doc.Order[0] != a || doc.Preferred != a || len(doc.Reasons) != 2 || doc.Reasons[0].Reason != "fits the state better" || doc.Model != "m" {
		t.Fatalf("ranking = %+v", doc)
	}
	if r := s.get("/episodes/not-a-uuid/ranking", op); r.status != 400 && r.status != 404 {
		t.Fatalf("a malformed episode id: %d", r.status)
	}
}
