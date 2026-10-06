package contracttest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// HAR-149: the inspector reads per-criterion results, the control effect, the evaluator version, the metrics and the
// lineage from the same route. A measurement that was not recorded stays null (never 0 or 1), a model D8 fail is a record
// and not a block, and a changed verdict on a re-save is NOT read as a recompute: lineage is only what a caller states.
func TestGateResultsCarryCriteriaEffectAndLineage(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	op := "/episodes/{episode_id}/gate-results"
	path := "/episodes/" + seed.EpisodeID + "/gate-results?gate=D8"
	ref := []string{"candidate:" + seed.Candidates[0]}
	zero := int64(0)
	first := bucket2.FromDimensions(bucket2.Judged{Gate: "D8", SubGate: "model", Type: "FinalArtifact", ID: seed.Candidates[0],
		SpanID: "recomputed_action:" + seed.EpisodeID, Model: "m", PromptVersion: "final_artifact:v1", ModelCalls: &zero,
		Dimensions: []bucket2.Dimension{{Name: "recipients", Verdict: "fail", Why: "wrong person", EvidenceRefs: ref}, {Name: "grounding", Verdict: "pass", Why: "sourced", EvidenceRefs: ref}}})
	blocker := bucket2.Result{Gate: "D8", SubGate: "recipients", JudgedType: "FinalArtifact", JudgedID: seed.Candidates[0], SpanID: "recomputed_action:" + seed.EpisodeID,
		Verdict: bucket2.Fail, Observed: "a recipient is not on the account", Why: "wrong person", EvidenceRefs: ref, Grader: bucket2.Deterministic}
	if err := bucket2.Save(context.Background(), env.DB, seed.EpisodeID, []bucket2.Result{first, blocker}); err != nil {
		t.Fatal(err)
	}
	type item struct {
		SubGate       string `json:"sub_gate"`
		Verdict       string `json:"verdict"`
		ControlEffect string `json:"control_effect"`
		Evaluator     string `json:"evaluator_version"`
		Criteria      []struct {
			ID     string `json:"id"`
			Result string `json:"result"`
		} `json:"criteria"`
		Lineage    map[string]string `json:"lineage"`
		LatencyMs  *int64            `json:"latency_ms"`
		ModelCalls *int64            `json:"model_calls"`
		Tokens     *int64            `json:"tokens"`
		CostUSD    *float64          `json:"cost_usd"`
	}
	read := func() map[string]item {
		r := s.get(path, op)
		var doc struct {
			Items []item `json:"items"`
		}
		if r.status != 200 || json.Unmarshal(r.body, &doc) != nil || len(doc.Items) != 2 {
			t.Fatalf("read: %d %s", r.status, clip(r.body))
		}
		out := map[string]item{}
		for _, i := range doc.Items {
			out[i.SubGate] = i
		}
		return out
	}
	got := read()
	m := got["model"]
	if m.Verdict != "fail" || m.ControlEffect != "RECORD ONLY" || m.Evaluator != "final_artifact:v1" || len(m.Criteria) != 2 || m.Criteria[0].ID != "recipients" {
		t.Fatalf("a model D8 fail is stored and shown, never a block: %+v", m)
	}
	if m.ModelCalls == nil || *m.ModelCalls != 0 || m.LatencyMs != nil || m.Tokens != nil || m.CostUSD != nil || len(m.Lineage) != 0 {
		t.Fatalf("a replayed answer is 0 calls and nothing else: %+v", m)
	}
	if b := got["recipients"]; b.ControlEffect != "BLOCK" || b.ModelCalls != nil {
		t.Fatalf("a deterministic recipients fail is the block, with no model calls recorded: %+v", b)
	}
	// The same gate saying something else on a re-save is not a recompute: no lineage is invented.
	pass := bucket2.FromDimensions(bucket2.Judged{Gate: "D8", SubGate: "model", Type: "FinalArtifact", ID: seed.Candidates[0],
		SpanID: "recomputed_action:" + seed.EpisodeID, Model: "m", PromptVersion: "final_artifact:v1",
		Dimensions: []bucket2.Dimension{{Name: "recipients", Verdict: "pass", Why: "right person", EvidenceRefs: ref}}})
	if err := bucket2.Save(context.Background(), env.DB, seed.EpisodeID, []bucket2.Result{pass}); err != nil {
		t.Fatal(err)
	}
	if again := read()["model"]; again.Verdict != "pass" || len(again.Lineage) != 0 {
		t.Fatalf("a changed verdict is not a recompute: %+v", again)
	}
	// A lineage the caller states (from a real recompute record) is stored and served.
	stated := pass
	stated.Lineage = bucket2.Lineage{RecomputeOf: "0e7a1000-0000-4000-8000-0000000000aa", PreviousVerdict: "fail"}
	if err := bucket2.Save(context.Background(), env.DB, seed.EpisodeID, []bucket2.Result{stated}); err != nil {
		t.Fatal(err)
	}
	if l := read()["model"].Lineage; l["recompute_of"] != "0e7a1000-0000-4000-8000-0000000000aa" || l["previous_verdict"] != "fail" {
		t.Fatalf("a stated lineage: %+v", l)
	}
}
