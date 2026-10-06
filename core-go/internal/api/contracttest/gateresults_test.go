package contracttest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestGateResultsConformToTheContract walks GET /episodes/{id}/gate-results over HTTP: empty before anything is
// stored (not measured is not an error), then one stored result per bucket (B, D and S gates share the store), the
// ?gate= filter, and the refusals. Every response is checked against core.yaml and gate_result.v1.json.
func TestGateResultsConformToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	op := "/episodes/{episode_id}/gate-results"
	base := "/episodes/" + seed.EpisodeID + "/gate-results"

	r := s.get(base, op)
	var empty struct {
		Items []json.RawMessage `json:"items"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &empty) != nil || len(empty.Items) != 0 || jsonField(t, r, "episode_id") != seed.EpisodeID {
		t.Fatalf("empty gate results: %d %s", r.status, clip(r.body))
	}

	mk := func(gate, question string) bucket2.Result {
		return bucket2.Result{Gate: gate, JudgedType: "DecisionEpisode", JudgedID: seed.EpisodeID, SpanID: "human_interaction:" + seed.EpisodeID,
			Verdict: bucket2.Pass, Question: question, Observed: "observed", Why: "why", Improves: "improves", Grader: bucket2.Deterministic,
			EvidenceRefs: []string{"candidate:" + seed.Candidates[0]}}
	}
	in := []bucket2.Result{mk("B2", "Did gtm_ai link the event to the right people?"), mk("D4", ""), mk("S1", "Can the grader be trusted?")}
	if err := bucket2.Save(context.Background(), env.DB, seed.EpisodeID, in); err != nil {
		t.Fatal(err)
	}
	r = s.get(base, op)
	var all struct {
		Items []struct {
			Gate       string `json:"gate"`
			Verdict    string `json:"verdict"`
			Calibrated bool   `json:"calibrated"`
		} `json:"items"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &all) != nil || len(all.Items) != 3 {
		t.Fatalf("gate results: %d %s", r.status, clip(r.body))
	}
	for i, g := range []string{"B2", "D4", "S1"} {
		if all.Items[i].Gate != g || all.Items[i].Verdict != "pass" || all.Items[i].Calibrated {
			t.Fatalf("item %d = %+v, want %s pass not calibrated", i, all.Items[i], g)
		}
	}
	r = s.get(base+"?gate=D4", op)
	if r.status != 200 || json.Unmarshal(r.body, &all) != nil || len(all.Items) != 1 || all.Items[0].Gate != "D4" {
		t.Fatalf("filtered gate results: %d %s", r.status, clip(r.body))
	}
	if r := s.get(base+"?gate=Z9", op); r.status != 400 {
		t.Fatalf("a malformed gate: %d %s", r.status, clip(r.body))
	}
	if r := s.get("/episodes/"+strategytest.NewID()+"/gate-results", op); r.status != 404 {
		t.Fatalf("an unknown episode: %d %s", r.status, clip(r.body))
	}
}
