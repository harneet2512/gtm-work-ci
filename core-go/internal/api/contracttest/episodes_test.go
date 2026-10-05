package contracttest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestEpisodeReadsConformToTheContract walks GET /episodes/{id}, /trace and /knowledge-mutations over HTTP against a
// seeded episode, before and after the human decides: every response is checked against core.yaml and the episode
// schemas, and every refusal against the error envelope.
func TestEpisodeReadsConformToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	seedResult(t, seed.RunID, 1, "champion_continuity", "warn", false)
	seedResult(t, seed.RunID, 1, "trajectory", "pass", false)
	ep, trace, muts, metrics := "/episodes/{episode_id}", "/episodes/{episode_id}/trace", "/episodes/{episode_id}/knowledge-mutations", "/episodes/{episode_id}/metrics"
	base := "/episodes/" + seed.EpisodeID

	r := s.get(base, ep)
	var summary struct {
		FinalStatus string `json:"final_status"`
		Run         struct{ Phase string }
	}
	if r.status != 200 || json.Unmarshal(r.body, &summary) != nil || summary.FinalStatus != "awaiting_choice" || summary.Run.Phase != "published" {
		t.Fatalf("episode: %d %s", r.status, clip(r.body))
	}
	r = s.get(base+"/trace", trace)
	var tr struct {
		Spans []struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"spans"`
		Unassigned []string `json:"unassigned_eval_result_ids"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &tr) != nil || len(tr.Spans) != 17 || len(tr.Unassigned) != 1 {
		t.Fatalf("trace: %d %s", r.status, clip(r.body))
	}
	if r := s.get(base+"/knowledge-mutations", muts); r.status != 200 || jsonField(t, r, "episode_id") != seed.EpisodeID {
		t.Fatalf("empty mutations: %d %s", r.status, clip(r.body))
	}

	r = s.get(base+"/metrics", metrics)
	var mt struct {
		Classification string `json:"classification"`
		Measured       bool   `json:"measured"`
		ModelCalls     int    `json:"model_calls"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &mt) != nil || mt.Classification != "metric" || mt.Measured {
		t.Fatalf("unmeasured metrics: %d %s", r.status, clip(r.body))
	}
	if _, err := env.DB.Exec(`INSERT INTO run_model_usage (agent_run_id, run_step, stage, models, model_calls, input_tokens, output_tokens, cached_input_tokens,
 reasoning_tokens, tool_calls, retries, cost_usd, model_ms, wall_ms, usage_source) VALUES ($1::uuid, 'draft', 'strategies', '{deepseek-v4-flash}', 3, 12000, 1500, 6000, 400, 3, 1, 0.008, 13000, 14000, 'live')`, seed.RunID); err != nil {
		t.Fatal(err)
	}
	r = s.get(base+"/metrics", metrics)
	if r.status != 200 || json.Unmarshal(r.body, &mt) != nil || !mt.Measured || mt.ModelCalls != 3 {
		t.Fatalf("measured metrics: %d %s", r.status, clip(r.body))
	}

	choose := map[string]any{"selected_candidate_id": seed.Candidates[1], "surface": "slack", "actor_label": "alex"}
	if r := s.post("/runs/"+seed.RunID+"/strategy-decision", "/runs/{run_id}/strategy-decision", choose); r.status != 201 {
		t.Fatalf("choose: %d %s", r.status, clip(r.body))
	}
	send := map[string]any{"decision": "send", "surface": "slack", "actor_label": "alex"}
	if r := s.post("/runs/"+seed.RunID+"/send", "/runs/{run_id}/send", send); r.status != 200 {
		t.Fatalf("send: %d %s", r.status, clip(r.body))
	}
	strategytest.SeedInference(t, env.DB, seed)
	tx, err := env.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := knowledgestore.RecordEvidence(context.Background(), tx, seed.KnowledgeID,
		knowledge.Evidence{Kind: knowledge.EvidenceDecisionEpisode, RefID: seed.EpisodeID, At: time.Now()}, lifecycleRules(t)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	r = s.get(base, ep)
	if r.status != 200 || json.Unmarshal(r.body, &summary) != nil || summary.FinalStatus != "send_recorded" {
		t.Fatalf("decided episode: %d %s", r.status, clip(r.body))
	}
	r = s.get(base+"/knowledge-mutations", muts)
	var list struct {
		Items []struct{ Operation string } `json:"items"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &list) != nil || len(list.Items) != 1 || list.Items[0].Operation != "SUPPORT" {
		t.Fatalf("mutations: %d %s", r.status, clip(r.body))
	}
	if r := s.get(base+"/trace", trace); r.status != 200 {
		t.Fatalf("decided trace: %d %s", r.status, clip(r.body))
	}

	for _, tmpl := range []string{ep, trace, muts, metrics} {
		path := "/episodes/" + missingID + tmpl[len("/episodes/{episode_id}"):]
		s.expectError(s.get(path, tmpl), 404, "not_found")
		unauth := "/episodes/" + seed.EpisodeID + tmpl[len("/episodes/{episode_id}"):]
		if r := s.do("GET", unauth, tmpl, "", nil); r.status != 401 {
			t.Fatalf("%s without a token: %d", tmpl, r.status)
		}
		if r := s.do("POST", unauth, "", apiToken, nil); r.status != 405 {
			t.Fatalf("POST %s: %d", tmpl, r.status)
		}
	}
	s.expectError(s.get("/episodes/not-a-uuid", ep), 404, "not_found")
}
