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
	// Measured is what the worker reported for this call (not part of the worker's body). Every field is nil when the
	// worker reported nothing: not recorded is never read as zero.
	Measured Measured `json:"-"`
}

// Measured is the real cost of one worker call, from the usage the worker reported.
type Measured struct {
	// ModelCalls is the live model calls made: 0 when the answer was replayed from a recording.
	ModelCalls *int64
	// Tokens, LatencyMs (the model time) and CostUSD are set only for a live call that reported them.
	Tokens    *int64
	LatencyMs *int64
	CostUSD   *float64
}

// MeasuredFrom folds the usage records of one call. No record is "not recorded"; a replayed call made no model call and
// spent nothing, so it carries 0 calls and nothing else; a live call carries what the worker reported.
func MeasuredFrom(recs []UsageRecord) Measured {
	var m Measured
	if len(recs) == 0 {
		return m
	}
	var calls, tokens, ms int64
	var cost float64
	live, costKnown := false, true
	for _, r := range recs {
		if r.Usage.UsageSource != UsageLive {
			continue
		}
		live = true
		calls += int64(r.Usage.ModelCalls)
		tokens += r.Usage.InputTokens + r.Usage.OutputTokens
		ms += r.Usage.ModelMs
		if r.Usage.CostUSD == nil {
			costKnown = false
		} else {
			cost += *r.Usage.CostUSD
		}
	}
	if !live {
		zero := int64(0)
		m.ModelCalls = &zero
		return m
	}
	m.ModelCalls, m.Tokens, m.LatencyMs = &calls, &tokens, &ms
	if costKnown {
		m.CostUSD = &cost
	}
	return m
}

// UsageCapture is a usage sink that keeps one call's records and passes them on to the sink it wraps (the run's own).
type UsageCapture struct {
	Outer UsageSink
	recs  []UsageRecord
}

// RecordUsage keeps the record and forwards it.
func (c *UsageCapture) RecordUsage(r UsageRecord) {
	c.recs = append(c.recs, r)
	if c.Outer != nil {
		c.Outer.RecordUsage(r)
	}
}

// Records are the records captured so far.
func (c *UsageCapture) Records() []UsageRecord { return c.recs }

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
