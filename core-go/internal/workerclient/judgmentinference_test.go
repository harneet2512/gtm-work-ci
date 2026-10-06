package workerclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestInferJudgmentPostsTheContractRequestAndKeepsTheAnswerRaw(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/judgment-inference" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"inferred_semantic_delta":{"statement":"s"},"evidence":{"evidence_refs":[]},"model":"m"}`))
	})
	resp, err := c.InferJudgment(context.Background(), InferJudgmentRequest{
		DecisionEpisodeID: "e", StrategySet: json.RawMessage(`{}`), EvalBundles: []json.RawMessage{json.RawMessage(`{}`)},
		HumanStrategyDecision: json.RawMessage(`{}`), RunToken: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if got["decision_episode_id"] != "e" || got["run_token"] != "t" {
		t.Fatalf("request = %v", got)
	}
	if resp.Model != "m" || string(resp.InferredSemanticDelta) != `{"statement":"s"}` {
		t.Fatalf("response = %+v", resp)
	}
}

func TestInferJudgmentRejectsAnAnswerWithoutADelta(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"model":"m"}`)) })
	if _, err := c.InferJudgment(context.Background(), InferJudgmentRequest{}); err == nil {
		t.Fatal("an answer with no delta must be refused")
	}
}
