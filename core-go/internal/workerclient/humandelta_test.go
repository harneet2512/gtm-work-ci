package workerclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestLabelDeltaPostsTheContractRequestAndDecodesTheAnswer(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/human-delta" || r.Method != http.MethodPost {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"semantic_labels": []string{"reduced_pressure", "deferred_to_buyer_timing"},
			"candidate_criterion": map[string]any{
				"statement":           "The human drops the CTA when the buyer asks for time.",
				"suggested_eval_type": "cta_calibration"},
			"model": "stub-v1"})
	})
	resp, err := c.LabelDelta(context.Background(), LabelDeltaRequest{
		RunID: "r", DecisionEpisodeID: "e", AccountID: "a",
		SelectedCandidate: json.RawMessage(`{"candidate_id":"c"}`),
		FinalAction: DeltaFinalAction{
			To:       []Recipient{{PersonID: "p", Role: "to", Why: "champion"}},
			Artifact: Artifact{Channel: "email", Body: "b"}},
		LiteralChanges: json.RawMessage(`[{"kind":"subject_changed"}]`),
		Unexplained:    true,
		ExplainingEvals: []ExplainingEval{
			{EvalResultID: "er", EvalType: "recipient_exists", Reason: "predicted"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["run_id"] != "r" || got["decision_episode_id"] != "e" || got["account_id"] != "a" {
		t.Fatalf("request ids = %v", got)
	}
	if got["unexplained"] != true || got["selected_candidate"].(map[string]any)["candidate_id"] != "c" {
		t.Fatalf("request = %v", got)
	}
	fa := got["final_action"].(map[string]any)
	if fa["to"].([]any)[0].(map[string]any)["person_id"] != "p" || fa["artifact"].(map[string]any)["channel"] != "email" {
		t.Fatalf("final_action = %v", fa)
	}
	if len(got["explaining_evals"].([]any)) != 1 {
		t.Fatalf("explaining_evals = %v", got["explaining_evals"])
	}
	if _, has := got["run_token"]; has {
		t.Fatal("human-delta is self-contained: no run_token may be sent")
	}
	if len(resp.SemanticLabels) != 2 || resp.SemanticLabels[0] != "reduced_pressure" {
		t.Fatalf("semantic_labels = %v", resp.SemanticLabels)
	}
	if resp.CandidateCriterion == nil || resp.CandidateCriterion.SuggestedEvalType != "cta_calibration" {
		t.Fatalf("candidate_criterion = %+v", resp.CandidateCriterion)
	}
	if resp.Model != "stub-v1" {
		t.Fatalf("model = %q", resp.Model)
	}
}

func TestLabelDeltaRefusesAnIncompleteAnswer(t *testing.T) {
	for name, body := range map[string]string{
		"no model":   `{"semantic_labels":[],"candidate_criterion":null}`,
		"no labels":  `{"model":"m","candidate_criterion":null}`,
		"bad status": `{"error":{"code":"invalid_request","message":"bad delta"}}`,
	} {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if name == "bad status" {
				w.WriteHeader(http.StatusUnprocessableEntity)
			}
			_, _ = io.WriteString(w, body)
		})
		_, err := c.LabelDelta(context.Background(), LabelDeltaRequest{})
		var we *Error
		if !errors.As(err, &we) {
			t.Fatalf("%s: %v, want a *Error", name, err)
		}
	}
}
