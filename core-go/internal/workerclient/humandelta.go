package workerclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// LabelDeltaTimeout bounds POST /v1/human-delta: the call happens while the send transaction holds its
// FOR UPDATE locks (contracts/openapi/worker.yaml, HAR-139), so a hung labeler must not stall sends.
// The effective bound is min(LabelDeltaTimeout, the request ctx) — under the API's read timeout the
// request ctx aborts first — and any failure degrades to the core-written fallback.
const LabelDeltaTimeout = 60 * time.Second

// ExplainingEval is one eval result that predicted the human's change (human_delta.explaining_evals[]).
type ExplainingEval struct {
	EvalResultID string `json:"eval_result_id"`
	EvalType     string `json:"eval_type"`
	Reason       string `json:"reason"`
}

// DeltaFinalAction is what the human is about to send (the request's final_action).
type DeltaFinalAction struct {
	To       []Recipient `json:"to"`
	CC       []Recipient `json:"cc,omitempty"`
	Artifact Artifact    `json:"artifact"`
}

// DeltaCriterion is the candidate eval criterion of an unexplained delta (human_delta.candidate_criterion).
type DeltaCriterion struct {
	Statement            string  `json:"statement"`
	SuggestedEvalType    string  `json:"suggested_eval_type"`
	KnowledgeCandidateID *string `json:"knowledge_candidate_id,omitempty"`
}

// LabelDeltaRequest is the body of POST /v1/human-delta. SelectedCandidate is the stored
// strategy_candidate.v1.json document; LiteralChanges is the literal_changes array core computed.
type LabelDeltaRequest struct {
	RunID             string           `json:"run_id"`
	DecisionEpisodeID string           `json:"decision_episode_id"`
	AccountID         string           `json:"account_id"`
	SelectedCandidate json.RawMessage  `json:"selected_candidate"`
	FinalAction       DeltaFinalAction `json:"final_action"`
	LiteralChanges    json.RawMessage  `json:"literal_changes"`
	Unexplained       bool             `json:"unexplained"`
	ExplainingEvals   []ExplainingEval `json:"explaining_evals"`
}

// LabelDeltaResponse is the 200 body of POST /v1/human-delta.
type LabelDeltaResponse struct {
	SemanticLabels     []string        `json:"semantic_labels"`
	CandidateCriterion *DeltaCriterion `json:"candidate_criterion"`
	Model              string          `json:"model"`
}

// LabelDelta calls POST /v1/human-delta. The caller validates the response's semantics (vocabulary,
// criterion presence) — the client only checks the envelope.
func (c *Client) LabelDelta(ctx context.Context, req LabelDeltaRequest) (LabelDeltaResponse, error) {
	var out LabelDeltaResponse
	if err := c.call(ctx, "/v1/human-delta", LabelDeltaTimeout, req, &out); err != nil {
		return LabelDeltaResponse{}, err
	}
	if out.Model == "" || out.SemanticLabels == nil {
		return LabelDeltaResponse{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no model or no semantic_labels"}
	}
	return out, nil
}
