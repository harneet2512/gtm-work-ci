package demosmoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// replayInferrer stands in for the model worker in the real-core smoke (no worker, no network). It is wired
// into the real strategystore path, so the smoke now exercises the production flow: Send, then InferJudgment,
// then the stored row Slack Message 3 renders. It states only what the request itself shows (the two
// candidates, the edits, their evidence) and never claims a label, so the smoke cannot pass on a made-up
// interpretation. The unit tests of the worker route cover the model side.
type replayInferrer struct{}

type inferCandidate struct {
	ID           string `json:"candidate_id"`
	Title        string `json:"title"`
	StrategyType string `json:"strategy_type"`
	Evidence     []struct {
		ActivityID string `json:"activity_id"`
	} `json:"evidence_refs"`
}

// InferJudgment implements strategystore.JudgmentInferrer.
func (replayInferrer) InferJudgment(_ context.Context, req workerclient.InferJudgmentRequest) (workerclient.InferJudgmentResponse, error) {
	var set struct {
		Candidates []inferCandidate `json:"candidates"`
	}
	var dec struct {
		Selected  string            `json:"selected_candidate_id"`
		Preferred string            `json:"original_agent_preference"`
		Edits     []json.RawMessage `json:"edits"`
	}
	if err := errors.Join(json.Unmarshal(req.StrategySet, &set), json.Unmarshal(req.HumanStrategyDecision, &dec)); err != nil {
		return workerclient.InferJudgmentResponse{}, fmt.Errorf("replay inferrer: %w", err)
	}
	byID := map[string]inferCandidate{}
	for _, c := range set.Candidates {
		byID[c.ID] = c
	}
	chosen, pref := byID[dec.Selected], byID[dec.Preferred]
	if len(chosen.Evidence) == 0 {
		return workerclient.InferJudgmentResponse{}, errors.New("replay inferrer: the chosen candidate cites no evidence")
	}
	statement := fmt.Sprintf("The person chose %q (%s) and gtm_ai preferred %q (%s).", chosen.Title, chosen.StrategyType, pref.Title, pref.StrategyType)
	signal, class := "weak", []string{}
	if len(dec.Edits) > 0 {
		signal, class = "moderate", []string{"unknown"} // an edit it cannot interpret: unknown, never a confident class
	}
	delta, _ := json.Marshal(map[string]any{"statement": statement, "semantic_labels": []string{}, "edit_class": class,
		"signal_strength": signal, "explicit_instructions": []any{}, "unknown": true})
	evidence, _ := json.Marshal(map[string]any{"candidate_differences": []string{"strategy " + chosen.StrategyType + " versus " + pref.StrategyType},
		"evidence_refs": []map[string]string{{"activity_id": chosen.Evidence[0].ActivityID}}, "eval_differences": []any{},
		"knowledge_refs": []string{}, "no_applicable_knowledge": true})
	return workerclient.InferJudgmentResponse{InferredSemanticDelta: delta, Evidence: evidence, Model: "smoke-replay"}, nil
}
