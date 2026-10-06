package contracttest

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// post sends a JSON body as the operator, after checking the body itself against the spec.
func (s *stack) post(path, template string, body any) reply {
	s.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.spec.Request("POST", template, raw); err != nil {
		s.t.Fatalf("the test's own request is not a valid %s body: %v", template, err)
	}
	return s.do("POST", path, template, apiToken, raw)
}

func (s *stack) expectError(r reply, status int, code string) {
	s.t.Helper()
	if r.status != status || errorCode(s.t, r) != code {
		s.t.Fatalf("got %d %s, want %d %s", r.status, clip(r.body), status, code)
	}
}

func jsonField(t *testing.T, r reply, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	return m[key]
}

// TestStrategyEndpointsConformToTheContract walks the HAR-129 demo path over HTTP against seeded rows:
// strategies, choose, edit, send, the judgment inference and the human's verdict, each response checked
// against core.yaml and the schemas it references.
func TestStrategyEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	run, episode := "/runs/"+seed.RunID, "/episodes/"+seed.EpisodeID
	cand := seed.Candidates

	if r := s.get("/accounts/"+seed.AccountID+"/business-intelligence/latest", "/accounts/{account_id}/business-intelligence/latest"); r.status != 200 || jsonField(t, r, "id") != seed.BIID {
		t.Fatalf("business intelligence: %d %s", r.status, clip(r.body))
	}
	s.expectError(s.get("/accounts/"+missingID+"/business-intelligence/latest", "/accounts/{account_id}/business-intelligence/latest"), 404, "not_found")

	if r := s.get(run+"/strategies", "/runs/{run_id}/strategies"); r.status != 200 {
		t.Fatalf("strategies: %d %s", r.status, clip(r.body))
	}
	s.expectError(s.get("/runs/"+missingID+"/strategies", "/runs/{run_id}/strategies"), 404, "not_found")
	s.expectError(s.get("/runs/"+s.world.RunB+"/strategies", "/runs/{run_id}/strategies"), 404, "strategies_not_ready")
	if r := s.do("GET", run+"/strategies", "/runs/{run_id}/strategies", "", nil); r.status != 401 {
		t.Fatalf("strategies without a token: %d", r.status)
	}

	decisionPath := "/runs/{run_id}/strategy-decision"
	s.expectError(s.get(run+"/strategy-decision", decisionPath), 404, "no_decision")
	choose := map[string]any{"selected_candidate_id": cand[1], "surface": "slack", "actor_label": "alex"}
	if r := s.post(run+"/strategy-decision", decisionPath, choose); r.status != 201 || jsonField(t, r, "send_decision") != "pending" {
		t.Fatalf("choose: %d %s", r.status, clip(r.body))
	}
	if r := s.post(run+"/strategy-decision", decisionPath, choose); r.status != 200 {
		t.Fatalf("choose again: %d %s", r.status, clip(r.body))
	}
	s.expectError(s.post(run+"/strategy-decision", decisionPath, map[string]any{"selected_candidate_id": cand[2], "surface": "slack", "actor_label": "alex"}), 409, "selection_locked")
	s.expectError(s.post(run+"/strategy-decision", decisionPath, map[string]any{"selected_candidate_id": missingID, "surface": "slack", "actor_label": "alex"}), 422, "unknown_candidate")
	if r := s.do("POST", run+"/strategy-decision", decisionPath, apiToken, []byte(`{"selected_candidate_id":"x","bogus":1}`)); r.status != 400 {
		t.Fatalf("unknown field: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", run+"/strategy-decision", decisionPath, "wrong", []byte(`{}`)); r.status != 401 {
		t.Fatalf("wrong token: %d", r.status)
	}
	edit := map[string]any{"selected_candidate_id": cand[1], "surface": "slack", "actor_label": "alex",
		"final_artifact": map[string]any{"channel": "email", "subject": "Edited", "body": "Edited body"}}
	if r := s.post(run+"/strategy-decision", decisionPath, edit); r.status != 200 || jsonField(t, r, "final_artifact") == nil {
		t.Fatalf("edit: %d %s", r.status, clip(r.body))
	}
	if r := s.get(run+"/strategy-decision", decisionPath); r.status != 200 || fmt.Sprint(jsonField(t, r, "send_decision")) != "pending" {
		t.Fatalf("read decision: %d %s", r.status, clip(r.body))
	}

	s.expectError(s.get(episode+"/judgment-inference", "/episodes/{episode_id}/judgment-inference"), 404, "inference_not_ready")
	sendPath := "/runs/{run_id}/send"
	send := map[string]any{"decision": "send", "surface": "slack", "actor_label": "alex"}
	if r := s.post(run+"/send", sendPath, send); r.status != 200 || jsonField(t, r, "send_decision") != "send" {
		t.Fatalf("send: %d %s", r.status, clip(r.body))
	}
	if r := s.post(run+"/send", sendPath, send); r.status != 409 || jsonField(t, r, "send_decision") != "send" {
		t.Fatalf("second send must be 409 with the stored record: %d %s", r.status, clip(r.body))
	}
	if r := s.post(run+"/strategy-decision", decisionPath, edit); r.status != 409 || jsonField(t, r, "send_decision") != "send" {
		t.Fatalf("an edit after the send must be 409 with the stored record: %d %s", r.status, clip(r.body))
	}

	strategytest.SeedInference(t, env.DB, seed) // the worker's job after the send
	inferencePath, verdictPath := "/episodes/{episode_id}/judgment-inference", "/episodes/{episode_id}/judgment-verdict"
	if r := s.get(episode+"/judgment-inference", inferencePath); r.status != 200 || jsonField(t, r, "human_verdict") != "pending" {
		t.Fatalf("inference: %d %s", r.status, clip(r.body))
	}
	s.expectError(s.get("/episodes/"+missingID+"/judgment-inference", inferencePath), 404, "not_found")
	note := map[string]any{"note": "context", "surface": "slack", "actor_label": "alex"}
	if r := s.post(episode+"/judgment-verdict", verdictPath, note); r.status != 200 || jsonField(t, r, "human_verdict") != "pending" {
		t.Fatalf("note: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", episode+"/judgment-verdict", verdictPath, apiToken, []byte(`{"verdict":"corrected","surface":"slack","actor_label":"a"}`)); r.status != 422 {
		t.Fatalf("a correction without a statement: %d %s", r.status, clip(r.body))
	}
	fix := map[string]any{"verdict": "corrected", "corrected_statement": "Because the reviewer is new.", "surface": "slack", "actor_label": "alex"}
	if r := s.post(episode+"/judgment-verdict", verdictPath, fix); r.status != 200 || jsonField(t, r, "human_verdict") != "corrected" {
		t.Fatalf("correction: %d %s", r.status, clip(r.body))
	}
	// no_learning after a correction is refused: the correction already seeded a candidate (HAR-129 Cliff).
	declined := map[string]any{"verdict": "no_learning", "surface": "slack", "actor_label": "alex"}
	s.expectError(s.post(episode+"/judgment-verdict", verdictPath, declined), 422, "verdict_locked")
	s.expectError(s.post("/episodes/"+missingID+"/judgment-verdict", verdictPath, fix), 404, "not_found")
	if r := s.do("POST", episode+"/judgment-verdict", verdictPath, apiToken, []byte(`not json`)); r.status != 400 {
		t.Fatalf("bad json: %d", r.status)
	}
}
