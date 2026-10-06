package workerclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// InferJudgmentTimeout bounds POST /v1/judgment-inference. The call runs after the send transaction has
// committed, so a slow model never holds a lock; a failure leaves the episode without an inference (not ready).
const InferJudgmentTimeout = 90 * time.Second

// InferJudgmentRequest is the body of POST /v1/judgment-inference (contracts/openapi/worker.yaml). The three
// documents are the stored strategy_set, eval_bundle and human_strategy_decision JSON, passed through.
type InferJudgmentRequest struct {
	DecisionEpisodeID     string            `json:"decision_episode_id"`
	StrategySet           json.RawMessage   `json:"strategy_set"`
	EvalBundles           []json.RawMessage `json:"eval_bundles"`
	HumanStrategyDecision json.RawMessage   `json:"human_strategy_decision"`
	RunToken              string            `json:"run_token"`
	HumanNote             *string           `json:"human_note,omitempty"`
}

// InferJudgmentResponse is the 200 body: the inferred_semantic_delta and evidence objects stay raw JSON so
// core stores exactly what the contract describes.
type InferJudgmentResponse struct {
	InferredSemanticDelta json.RawMessage `json:"inferred_semantic_delta"`
	Evidence              json.RawMessage `json:"evidence"`
	Model                 string          `json:"model"`
}

// InferJudgment calls POST /v1/judgment-inference (HAR-97 D5).
func (c *Client) InferJudgment(ctx context.Context, req InferJudgmentRequest) (InferJudgmentResponse, error) {
	var out InferJudgmentResponse
	if err := c.call(ctx, "/v1/judgment-inference", InferJudgmentTimeout, req, &out); err != nil {
		return InferJudgmentResponse{}, err
	}
	if out.Model == "" || len(out.InferredSemanticDelta) == 0 || len(out.Evidence) == 0 {
		return InferJudgmentResponse{}, &Error{Status: http.StatusOK, Code: "bad_response",
			Message: "worker returned no model, inferred_semantic_delta or evidence"}
	}
	return out, nil
}
