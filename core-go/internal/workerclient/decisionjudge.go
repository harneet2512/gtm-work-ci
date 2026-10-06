package workerclient

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// DecisionJudgeTimeout bounds POST /v1/decision-judge (one model call per request).
const DecisionJudgeTimeout = 90 * time.Second

// DecisionJudgeRequest is the body of POST /v1/decision-judge (contracts/openapi/worker.yaml). Kind is one of
// intent_fit, set_quality, candidate_quality, ranking, final_artifact or rank_rationale.
type DecisionJudgeRequest struct {
	Kind        string          `json:"kind"`
	SubjectID   string          `json:"subject_id"`
	Payload     json.RawMessage `json:"payload"`
	EvidenceIDs []string        `json:"evidence_ids"`
	Trial       int             `json:"trial,omitempty"`
}

// DimensionVerdict is one dimension of a judge's answer.
type DimensionVerdict struct {
	Dimension    string   `json:"dimension"`
	Verdict      string   `json:"verdict"`
	Why          string   `json:"why"`
	EvidenceRefs []string `json:"evidence_refs"`
}

// PairReason is one adjacent-pair reason of a rank rationale.
type PairReason struct {
	RankedHigherID string   `json:"ranked_higher_id"`
	RankedLowerID  string   `json:"ranked_lower_id"`
	Reason         string   `json:"reason"`
	EvidenceRefs   []string `json:"evidence_refs"`
	KnowledgeRefs  []string `json:"knowledge_refs"`
}

// DecisionJudgeResponse is the 200 body of POST /v1/decision-judge.
type DecisionJudgeResponse struct {
	Kind                string             `json:"kind"`
	SubjectID           string             `json:"subject_id"`
	Dimensions          []DimensionVerdict `json:"dimensions"`
	Summary             string             `json:"summary"`
	RightReaction       *string            `json:"right_reaction"`
	QuietMoveConsidered *bool              `json:"quiet_move_considered"`
	PairwiseReasons     []PairReason       `json:"pairwise_reasons"`
	Model               string             `json:"model"`
	PromptVersion       string             `json:"prompt_version"`
}

// DecisionJudge calls POST /v1/decision-judge.
func (c *Client) DecisionJudge(ctx context.Context, req DecisionJudgeRequest) (DecisionJudgeResponse, error) {
	var out DecisionJudgeResponse
	if err := c.call(ctx, "/v1/decision-judge", DecisionJudgeTimeout, req, &out); err != nil {
		return DecisionJudgeResponse{}, err
	}
	if out.Model == "" {
		return DecisionJudgeResponse{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no model"}
	}
	return out, nil
}
