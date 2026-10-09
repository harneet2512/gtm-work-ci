package workerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Per-call time budgets. Each exceeds the worker's own deadline for the call, so the worker answers or fails with a 502 first. The
// demo's worker runs with the deadlines of demorun.demoDeadlines (strategies 360 s, judge 450 s, draft/revise 360 s, one model call
// 300 s: a thinking model takes 53 to 80 s a call and more under load); the defaults in worker-py settings.py are lower. The
// orchestrator sizes the run token's lifetime from these (orchestrator.Config.validate), and a token lives at most runtoken.MaxTTL
// (one hour): 3 generations (with the counterfactual) + 4 judge rounds + 1 revision = 3*390 + 4*480 + 390 = 3480 s.
const (
	StrategiesTimeout = 390 * time.Second
	JudgeTimeout      = 480 * time.Second
	ReviseTimeout     = 390 * time.Second
)

// Recipient is agent_run_output.recipients[].
type Recipient struct {
	PersonID string `json:"person_id"`
	Role     string `json:"role"`
	Why      string `json:"why,omitempty"`
}

// EvidenceRef is common.v1.json#/$defs/evidenceRef.
type EvidenceRef struct {
	ActivityID      string     `json:"activity_id"`
	ClaimID         string     `json:"claim_id,omitempty"`
	Quote           string     `json:"quote,omitempty"`
	SpeakerPersonID string     `json:"speaker_person_id,omitempty"`
	OccurredAt      *time.Time `json:"occurred_at,omitempty"`
}

// Artifact is agent_run_output.finished_artifact.
type Artifact struct {
	Channel     string   `json:"channel"`
	Subject     *string  `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments,omitempty"`
}

// FiveQuestions are the structured answers every visible recommendation carries (HAR-128).
type FiveQuestions struct {
	WhatChanged           string `json:"what_changed"`
	WhyStateChanged       string `json:"why_state_changed"`
	WhatRemainsUnknown    string `json:"what_remains_unknown"`
	PriorKnowledgeApplies string `json:"prior_knowledge_applies"`
	WhyNextAction         string `json:"why_next_action"`
}

// Candidate is strategy_candidate.v1.json. The worker leaves DraftIndex and EvalBundleRef unset.
type Candidate struct {
	CandidateID        string        `json:"candidate_id"`
	StrategyType       string        `json:"strategy_type"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Ranking            int           `json:"ranking"`
	PreferredByAgent   bool          `json:"preferred_by_agent"`
	Rationale          string        `json:"rationale"`
	StateRefs          []string      `json:"state_refs"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs"`
	KnowledgeRefs      []string      `json:"knowledge_refs"`
	ActionType         string        `json:"action_type"`
	ActionClass        string        `json:"action_class"`
	FiveQuestions      FiveQuestions `json:"five_questions"`
	To                 []Recipient   `json:"to"`
	CC                 []Recipient   `json:"cc"`
	Subject            *string       `json:"subject"`
	FullActionArtifact Artifact      `json:"full_action_artifact"`
	Preview            string        `json:"preview"`
	DraftIndex         *int          `json:"draft_index,omitempty"`
	EvalBundleRef      *string       `json:"eval_bundle_ref,omitempty"`
}

// TriggerContext is the worker's trigger_context (the same object /v1/draft takes).
type TriggerContext struct {
	TriggerActivityIDs []string `json:"trigger_activity_ids"`
	SignalTypes        []string `json:"signal_types"`
	ReasonCodes        []string `json:"reason_codes"`
	RepPersonID        *string  `json:"rep_person_id,omitempty"`
}

// GeneratorFeedback is one explained human correction queued for the account's next strategies call
// (worker.yaml generator_feedback, HAR-119): the eval whose earlier failure a send-time edit repaired,
// plus the instruction the planner should apply proactively on this episode's candidates. It is
// correction context, not evidence — the planner never cites it.
type GeneratorFeedback struct {
	EvalResultID string `json:"eval_result_id"`
	EvalType     string `json:"eval_type"`
	Instruction  string `json:"instruction"`
}

// StrategiesRequest is the body of POST /v1/strategies.
type StrategiesRequest struct {
	RunID             string              `json:"run_id"`
	AccountID         string              `json:"account_id"`
	DecisionEpisodeID string              `json:"decision_episode_id"`
	Workflow          string              `json:"workflow"`
	TriggerContext    TriggerContext      `json:"trigger_context"`
	AccountChangeID   *string             `json:"account_change_id,omitempty"`
	StateHeader       string              `json:"state_header"`
	RunToken          string              `json:"run_token"`
	CandidateCount    int                 `json:"candidate_count"`
	MaxToolCalls      int                 `json:"max_tool_calls,omitempty"`
	DecisionGuidance  json.RawMessage     `json:"decision_guidance,omitempty"`
	StateTransition   json.RawMessage     `json:"state_transition,omitempty"`
	GeneratorFeedback []GeneratorFeedback `json:"generator_feedback,omitempty"`
}

// StrategiesResponse is the 200 body of POST /v1/strategies.
type StrategiesResponse struct {
	Candidates    []Candidate `json:"candidates"`
	Model         string      `json:"model"`
	ToolCalls     int         `json:"tool_calls"`
	CitedAccessID []int       `json:"cited_access_ids"`
}

// EvalSuite tells the judge which suite core selected (contracts/transitions/routing.v1.json).
type EvalSuite struct {
	Name              string   `json:"name"`
	ExcludedEvalTypes []string `json:"excluded_eval_types"`
}

// JudgeRequest is the body of POST /v1/judge.
type JudgeRequest struct {
	RunID            string            `json:"run_id"`
	AccountID        string            `json:"account_id"`
	DraftIndex       int               `json:"draft_index"`
	Candidate        Candidate         `json:"candidate"`
	RunToken         string            `json:"run_token"`
	TriggerContext   *TriggerContext   `json:"trigger_context,omitempty"`
	EvalSuite        *EvalSuite        `json:"eval_suite,omitempty"`
	OfferedKnowledge []json.RawMessage `json:"offered_knowledge,omitempty"`
}

// BundleItem is one EvalBundle item (eval_bundle.v1.json#/$defs/item); Result stays raw JSON (an EvalResult).
type BundleItem struct {
	EvalType        string          `json:"eval_type"`
	RelevanceReason string          `json:"relevance_reason"`
	Verdict         string          `json:"verdict"`
	Result          json.RawMessage `json:"result"`
}

// JudgeResponse is the 200 body of POST /v1/judge.
type JudgeResponse struct {
	Items []BundleItem `json:"items"`
	Model string       `json:"model"`
}

// Feedback is one eval finding the revision planner acts on.
type Feedback struct {
	EvalResultID string `json:"eval_result_id"`
	Instruction  string `json:"instruction"`
}

// ReviseRequest is the body of POST /v1/revise.
type ReviseRequest struct {
	RunID      string     `json:"run_id"`
	AccountID  string     `json:"account_id"`
	DraftIndex int        `json:"draft_index"`
	Candidate  Candidate  `json:"candidate"`
	Feedback   []Feedback `json:"feedback"`
	RunToken   string     `json:"run_token"`
}

// ReviseResponse is the 200 body of POST /v1/revise.
type ReviseResponse struct {
	Candidate Candidate `json:"candidate"`
	Model     string    `json:"model"`
}

// Strategies calls POST /v1/strategies.
func (c *Client) Strategies(ctx context.Context, req StrategiesRequest) (StrategiesResponse, error) {
	var out StrategiesResponse
	if err := c.call(ctx, "/v1/strategies", StrategiesTimeout, req, &out); err != nil {
		return StrategiesResponse{}, err
	}
	if len(out.Candidates) == 0 || out.Model == "" {
		return StrategiesResponse{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no candidates or no model"}
	}
	return out, nil
}

// Judge calls POST /v1/judge.
func (c *Client) Judge(ctx context.Context, req JudgeRequest) (JudgeResponse, error) {
	var out JudgeResponse
	if err := c.call(ctx, "/v1/judge", JudgeTimeout, req, &out); err != nil {
		return JudgeResponse{}, err
	}
	if len(out.Items) == 0 {
		return JudgeResponse{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no eval items"}
	}
	return out, nil
}

// Revise calls POST /v1/revise.
func (c *Client) Revise(ctx context.Context, req ReviseRequest) (ReviseResponse, error) {
	var out ReviseResponse
	if err := c.call(ctx, "/v1/revise", ReviseTimeout, req, &out); err != nil {
		return ReviseResponse{}, err
	}
	if out.Candidate.CandidateID == "" || out.Model == "" {
		return ReviseResponse{}, &Error{Status: http.StatusOK, Code: "bad_response", Message: "worker returned no revised candidate or no model"}
	}
	return out, nil
}

// call POSTs body as JSON and decodes a 200 response into out. The time budget is the context's (the shared
// client's own timeout is Extract's), so a caller's shorter deadline still wins.
func (c *Client) call(ctx context.Context, path string, budget time.Duration, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("workerclient: encode %s request: %w", path, err)
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	started := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("workerclient: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.long.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(ctxErr, context.DeadlineExceeded) {
			return ctxErr
		}
		return &Error{Message: "worker unreachable or timed out: " + redactURLError(err), Retryable: true}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return &Error{Message: "read worker response: " + err.Error(), Retryable: true}
	}
	if len(data) > MaxResponseBytes {
		return &Error{Status: resp.StatusCode, Message: "worker response exceeds the size limit"}
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Status: resp.StatusCode, Code: "bad_response", Message: "worker response is not valid JSON"}
	}
	reportUsage(ctx, path, data, time.Since(started).Milliseconds())
	return nil
}
