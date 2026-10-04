package workerclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func candidateJSON() map[string]any {
	return map[string]any{"candidate_id": "0ca00000-0000-4000-8000-0000000000a1", "strategy_type": "send_package", "title": "t", "description": "d",
		"ranking": 1, "preferred_by_agent": true, "rationale": "r", "state_refs": []string{}, "evidence_refs": []any{map[string]any{"activity_id": "0ac70000-0000-4000-8000-000000000101"}},
		"knowledge_refs": []string{}, "action_type": "wait", "action_class": "WAIT",
		"five_questions": map[string]string{"what_changed": "a", "why_state_changed": "b", "what_remains_unknown": "c", "prior_knowledge_applies": "d", "why_next_action": "e"},
		"to":             []any{}, "cc": []any{}, "subject": nil, "full_action_artifact": map[string]any{"channel": "none", "subject": nil, "body": ""}, "preview": "p"}
}

func TestStrategiesPostsTheContractRequestAndDecodesTheCandidates(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/strategies" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{candidateJSON()}, "model": "m", "tool_calls": 2, "cited_access_ids": []int{1}})
	})
	resp, err := c.Strategies(context.Background(), StrategiesRequest{RunID: "r", AccountID: "a", DecisionEpisodeID: "e", Workflow: "post_interaction_followup",
		StateHeader: "h", RunToken: "tok", CandidateCount: 3, StateTransition: json.RawMessage(`{"status":"CANDIDATE"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got["candidate_count"] != float64(3) || got["run_token"] != "tok" || got["state_transition"].(map[string]any)["status"] != "CANDIDATE" {
		t.Fatalf("request = %v", got)
	}
	if _, has := got["account_change_id"]; has {
		t.Fatal("an unset optional field must be absent, not null")
	}
	c0 := resp.Candidates[0]
	if c0.Subject != nil || c0.ActionClass != "WAIT" || c0.FiveQuestions.WhyNextAction != "e" || c0.DraftIndex != nil {
		t.Fatalf("candidate = %+v", c0)
	}
	out, _ := json.Marshal(c0)
	var back map[string]json.RawMessage
	_ = json.Unmarshal(out, &back)
	if string(back["subject"]) != "null" {
		t.Fatalf("subject must serialise as null (the contract requires the key), got %s", back["subject"])
	}
	if _, has := back["draft_index"]; has {
		t.Fatal("an unassigned draft_index must be absent")
	}
}

func TestJudgeAndReviseDecodeAndRefuseEmptyAnswers(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/judge":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"eval_type": "grounding", "relevance_reason": "x", "verdict": "not_relevant", "result": nil}}, "model": "m"})
		case "/v1/revise":
			_ = json.NewEncoder(w).Encode(map[string]any{"candidate": candidateJSON(), "model": "m"})
		}
	})
	j, err := c.Judge(context.Background(), JudgeRequest{RunID: "r", RunToken: "t"})
	if err != nil || len(j.Items) != 1 || j.Items[0].Verdict != "not_relevant" || string(j.Items[0].Result) != "null" {
		t.Fatalf("judge = %+v, %v", j, err)
	}
	r, err := c.Revise(context.Background(), ReviseRequest{RunID: "r", RunToken: "t"})
	if err != nil || r.Candidate.CandidateID == "" {
		t.Fatalf("revise = %+v, %v", r, err)
	}
	empty := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"items":[],"model":"m"}`) })
	if _, err := empty.Judge(context.Background(), JudgeRequest{}); err == nil {
		t.Fatal("a judge answer without items must be an error")
	}
}

func TestWorkerErrorsAreClassifiedForTheBreakerAndTheRetryPolicy(t *testing.T) {
	cases := []struct {
		name                           string
		status                         int
		body                           string
		retryable, provider, permanent bool
	}{
		{"provider refused", 424, `{"error":{"code":"provider_unavailable_nonretryable","message":"credits"}}`, false, true, false},
		{"worker overloaded", 503, `{"error":{"code":"x","message":"busy"}}`, true, false, false},
		{"rate limited", 429, `{"error":{"code":"rate_limited","message":"slow"}}`, true, false, false},
		{"invalid request", 422, `{"error":{"code":"invalid_request","message":"bad"}}`, false, false, true},
		{"unusable strategies", 502, `{"error":{"code":"invalid_strategies","message":"dupes"}}`, true, false, false},
	}
	for _, tc := range cases {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		})
		_, err := c.Strategies(context.Background(), StrategiesRequest{})
		var we *Error
		if !errors.As(err, &we) {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if we.Retryable != tc.retryable || claims.IsProviderUnavailable(err) != tc.provider || claims.IsPermanent(err) != tc.permanent {
			t.Errorf("%s: retryable %v provider %v permanent %v", tc.name, we.Retryable, claims.IsProviderUnavailable(err), claims.IsPermanent(err))
		}
	}
}

func TestACallerDeadlineEndsTheCallAsATransientError(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Judge(ctx, JudgeRequest{})
	var we *Error
	if !errors.As(err, &we) || !we.Retryable {
		t.Fatalf("err = %v, want a retryable worker error", err)
	}
}
