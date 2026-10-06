package strategystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

type stubInferrer struct {
	resp  workerclient.InferJudgmentResponse
	err   error
	calls int
	last  workerclient.InferJudgmentRequest
}

func (s *stubInferrer) InferJudgment(_ context.Context, req workerclient.InferJudgmentRequest) (workerclient.InferJudgmentResponse, error) {
	s.calls++
	s.last = req
	return s.resp, s.err
}

func inferenceAnswer(activity string) workerclient.InferJudgmentResponse {
	return workerclient.InferJudgmentResponse{
		InferredSemanticDelta: json.RawMessage(`{"statement":"The person kept the champion in the loop.","semantic_labels":["kept_champion_involved"],` +
			`"edit_class":["strategy"],"signal_strength":"moderate","explicit_instructions":[],"unknown":false}`),
		Evidence: json.RawMessage(`{"candidate_differences":["chosen loops in the champion"],"evidence_refs":[{"activity_id":"` + activity + `"}],` +
			`"eval_differences":[],"knowledge_refs":[],"no_applicable_knowledge":true}`),
		Model: "stub-infer-v1"}
}

func TestSendProducesTheRealInferenceFromTheWorker(t *testing.T) {
	stub := &stubInferrer{}
	f := newFixtureWith(t, strategystore.WithInferrer(stub))
	stub.resp = inferenceAnswer(f.seed.ActivityID)
	editAndSend(t, f)
	if stub.calls != 1 {
		t.Fatalf("the worker was called %d times, want 1", stub.calls)
	}
	doc, err := f.svc.Inference(f.ctx, f.seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	valid(t, "judgment_inference", doc)
	d := decode(t, doc)
	delta := d["inferred_semantic_delta"].(map[string]any)
	if delta["signal_strength"] != "moderate" || delta["edit_class"].([]any)[0] != "strategy" {
		t.Fatalf("typed fields lost: %v", delta)
	}
	if d["model"] != "stub-infer-v1" || d["human_verdict"] != "pending" {
		t.Fatalf("model/verdict = %v / %v", d["model"], d["human_verdict"])
	}
	if stub.last.DecisionEpisodeID != f.seed.EpisodeID || len(stub.last.EvalBundles) != 3 {
		t.Fatalf("request = %+v", stub.last)
	}
}

func TestInferenceIsIdempotent(t *testing.T) {
	stub := &stubInferrer{}
	f := newFixtureWith(t, strategystore.WithInferrer(stub))
	stub.resp = inferenceAnswer(f.seed.ActivityID)
	editAndSend(t, f)
	if err := f.svc.InferJudgment(f.ctx, f.seed.RunID); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 {
		t.Fatalf("a second call must not ask the worker again (calls = %d)", stub.calls)
	}
}

func TestWorkerFailureLeavesNoInventedInferenceAndDoesNotFailTheSend(t *testing.T) {
	stub := &stubInferrer{err: errors.New("worker down")}
	f := newFixtureWith(t, strategystore.WithInferrer(stub))
	editAndSend(t, f)
	if _, err := f.svc.Inference(f.ctx, f.seed.EpisodeID); !errors.Is(err, strategystore.ErrNotReady) {
		t.Fatalf("want not ready, got %v", err)
	}
}

func TestNoInferrerMeansNoInference(t *testing.T) {
	f := newFixture(t)
	editAndSend(t, f)
	if err := f.svc.InferJudgment(f.ctx, f.seed.RunID); err == nil {
		t.Fatal("without an inferrer InferJudgment must say so")
	}
	if _, err := f.svc.Inference(f.ctx, f.seed.EpisodeID); !errors.Is(err, strategystore.ErrNotReady) {
		t.Fatalf("want not ready, got %v", err)
	}
}

func TestAnswerWithoutStatementIsRefused(t *testing.T) {
	stub := &stubInferrer{resp: workerclient.InferJudgmentResponse{
		InferredSemanticDelta: json.RawMessage(`{"statement":""}`), Evidence: json.RawMessage(`{}`), Model: "m"}}
	f := newFixtureWith(t, strategystore.WithInferrer(stub))
	editAndSend(t, f)
	if _, err := f.svc.Inference(f.ctx, f.seed.EpisodeID); !errors.Is(err, strategystore.ErrNotReady) {
		t.Fatalf("an unusable answer must not become an inference: %v", err)
	}
}
