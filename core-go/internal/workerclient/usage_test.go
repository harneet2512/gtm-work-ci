package workerclient

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

// recordingSink collects what the client reports.
type recordingSink struct {
	mu   sync.Mutex
	recs []UsageRecord
}

func (s *recordingSink) RecordUsage(r UsageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, r)
}

func (s *recordingSink) all() []UsageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UsageRecord(nil), s.recs...)
}

var usageBody = map[string]any{"model_calls": 3, "input_tokens": 12000, "output_tokens": 1500, "cached_input_tokens": 6000,
	"reasoning_tokens": 400, "tool_calls": 3, "retries": 1, "model_ms": 13000, "cost_usd": 0.008, "models": []string{"deepseek-v4-flash"}, "usage_source": "live"}

func workerFor(t *testing.T, withUsage any) *Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		switch r.URL.Path {
		case "/v1/strategies":
			body = map[string]any{"candidates": []any{candidateJSON()}, "model": "m", "tool_calls": 3, "cited_access_ids": []int{}}
		case "/v1/judge":
			body = map[string]any{"items": []any{map[string]any{"eval_type": "grounding", "relevance_reason": "x", "verdict": "not_relevant", "result": nil}}, "model": "m"}
		case "/v1/revise":
			body = map[string]any{"candidate": candidateJSON(), "model": "m"}
		case "/v1/human-delta":
			body = map[string]any{"semantic_labels": []string{"smaller_ask"}, "candidate_criterion": nil, "model": "m"}
		}
		if withUsage != nil {
			body["usage"] = withUsage
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}

// callAll makes one call of every run-scoped worker operation, in the order of stages.
func callAll(t *testing.T, c *Client, ctx context.Context) {
	t.Helper()
	if _, err := c.Strategies(ctx, StrategiesRequest{RunID: "r"}); err != nil {
		t.Fatalf("strategies: %v", err)
	}
	if _, err := c.Judge(ctx, JudgeRequest{RunID: "r"}); err != nil {
		t.Fatalf("judge: %v", err)
	}
	if _, err := c.Revise(ctx, ReviseRequest{RunID: "r"}); err != nil {
		t.Fatalf("revise: %v", err)
	}
	if _, err := c.LabelDelta(ctx, LabelDeltaRequest{RunID: "r"}); err != nil {
		t.Fatalf("human-delta: %v", err)
	}
}

func TestEveryRunScopedCallReportsItsUsageToTheSinkWithItsStage(t *testing.T) {
	sink := &recordingSink{}
	callAll(t, workerFor(t, usageBody), WithUsageSink(context.Background(), sink))

	got := sink.all()
	var stages []string
	for _, r := range got {
		stages = append(stages, r.Stage)
		u := r.Usage
		if u.ModelCalls != 3 || u.InputTokens != 12000 || u.OutputTokens != 1500 || u.CachedInputTokens == nil || *u.CachedInputTokens != 6000 ||
			u.ReasoningTokens == nil || *u.ReasoningTokens != 400 || u.UsageSource != "live" ||
			u.ToolCalls != 3 || u.Retries != 1 || u.ModelMs != 13000 || u.CostUSD == nil || *u.CostUSD != 0.008 || len(u.Models) != 1 {
			t.Errorf("%s usage = %+v", r.Stage, u)
		}
		if r.WallMs < 0 {
			t.Errorf("%s wall time = %d", r.Stage, r.WallMs)
		}
	}
	want := []string{"strategies", "judge", "revise", "human_delta"}
	if len(stages) != len(want) {
		t.Fatalf("stages = %v, want %v", stages, want)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatalf("stages = %v, want %v", stages, want)
		}
	}
}

func TestTheWallTimeIsWhatTheCallTookForCore(t *testing.T) {
	sink := &recordingSink{}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"candidate": candidateJSON(), "model": "m", "usage": usageBody})
	})
	if _, err := c.Revise(WithUsageSink(context.Background(), sink), ReviseRequest{RunID: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := sink.all(); len(got) != 1 || got[0].WallMs < 50 || got[0].WallMs > 5000 {
		t.Fatalf("wall time = %+v", got)
	}
}

func TestAWorkerThatReportsNoUsageRecordsNothing(t *testing.T) {
	sink := &recordingSink{}
	callAll(t, workerFor(t, nil), WithUsageSink(context.Background(), sink))
	if got := sink.all(); len(got) != 0 {
		t.Fatalf("an older worker that omits usage must not produce zero-token rows: %+v", got)
	}
}

func TestAnInvalidUsageObjectIsIgnoredNotRecorded(t *testing.T) {
	for name, bad := range map[string]any{
		"negative tokens":         map[string]any{"model_calls": 1, "input_tokens": -5, "output_tokens": 1, "cached_input_tokens": 0, "reasoning_tokens": 0, "tool_calls": 0, "retries": 0, "model_ms": 1, "models": []string{}},
		"no usage source":         map[string]any{"model_calls": 1, "input_tokens": 5, "output_tokens": 1, "tool_calls": 0, "retries": 0, "model_ms": 1, "models": []string{}},
		"an unknown usage source": map[string]any{"model_calls": 1, "input_tokens": 5, "output_tokens": 1, "tool_calls": 0, "retries": 0, "model_ms": 1, "models": []string{}, "usage_source": "estimated"},
		"negative cached":         map[string]any{"model_calls": 1, "input_tokens": 5, "output_tokens": 1, "cached_input_tokens": -1, "tool_calls": 0, "retries": 0, "model_ms": 1, "models": []string{}, "usage_source": "live"},
		"not an object":           "lots",
		"a null usage":            nil,
	} {
		t.Run(name, func(t *testing.T) {
			sink := &recordingSink{}
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"candidate": candidateJSON(), "model": "m", "usage": bad})
			})
			if _, err := c.Revise(WithUsageSink(context.Background(), sink), ReviseRequest{RunID: "r"}); err != nil {
				t.Fatalf("a bad usage object must never fail the call: %v", err)
			}
			if got := sink.all(); len(got) != 0 {
				t.Fatalf("recorded %+v", got)
			}
		})
	}
}

func TestUnreportedCachedAndReasoningTokensStayNullAndReplayIsCarried(t *testing.T) {
	sink := &recordingSink{}
	replay := map[string]any{"model_calls": 0, "input_tokens": 0, "output_tokens": 0, "tool_calls": 0, "retries": 0, "model_ms": 0,
		"models": []string{}, "usage_source": "replay"}
	callAll(t, workerFor(t, replay), WithUsageSink(context.Background(), sink))
	got := sink.all()
	if len(got) != 4 {
		t.Fatalf("recorded %d", len(got))
	}
	for _, r := range got {
		if r.Usage.UsageSource != "replay" || r.Usage.CachedInputTokens != nil || r.Usage.ReasoningTokens != nil || r.Usage.CostUSD != nil {
			t.Errorf("%s usage = %+v", r.Stage, r.Usage)
		}
	}
}

func TestWithoutASinkTheCallsBehaveAsBefore(t *testing.T) {
	callAll(t, workerFor(t, usageBody), context.Background())
}

func TestAFailedCallReportsNoUsage(t *testing.T) {
	sink := &recordingSink{}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"provider_error","message":"x"},"usage":{}}`))
	})
	if _, err := c.Revise(WithUsageSink(context.Background(), sink), ReviseRequest{RunID: "r"}); err == nil {
		t.Fatal("expected the 502 to surface")
	}
	if got := sink.all(); len(got) != 0 {
		t.Fatalf("recorded %+v", got)
	}
}

func TestUsageSinkOfAContextIsNilWhenNoneWasSet(t *testing.T) {
	if usageSinkFrom(context.Background()) != nil {
		t.Fatal("no sink was set")
	}
	sink := &recordingSink{}
	if usageSinkFrom(WithUsageSink(context.Background(), sink)) != sink {
		t.Fatal("the sink set on the context is the one read back")
	}
}
