package bucket1run

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

func episode() bucket1.Episode {
	return bucket1.Episode{ID: "ep",
		Activities:  []bucket1.Activity{{ID: "a1", Text: "the DPA is signed"}},
		Claims:      []bucket1.Claim{{ID: "c1", ActivityID: "a1", Field: "stage", Value: "Legal", Quote: "the DPA is signed", Kind: "fact"}},
		PriorClaims: []bucket1.Claim{{ID: "pc1", Field: "stage", Value: "Discovery"}},
		Beliefs:     []bucket1.Belief{{Kind: "summary", Statement: "legal is the gate"}},
		Precedents:  []bucket1.Precedent{{ID: "pr1"}},
	}
}

func TestRefStringsKeepOnlyNamedEvidenceOnceAndAlwaysAnArray(t *testing.T) {
	got := refStrings([]bucket1.Ref{{ActivityID: "a1"}, {ActivityID: "a1"}, {ClaimID: "c1", ActivityID: "a1"}, {StepID: "s1"}, {Quote: "only words"}, {}})
	want := []string{"activity:a1", "claim:c1", "agent_run_step:s1"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
	if got := refStrings(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty must be an empty array, got %#v", got)
	}
}

func TestJudgePayloadReadsOnlyWhatTheGateNeedsAndSaysWhenThereIsNothing(t *testing.T) {
	ep := episode()
	for gate, want := range map[string]string{"B1": "inference_boundary", "B3": "supporting_and_conflicting_links", "B5": "relevance_and_misses", "B8": "confidence_and_omissions"} {
		payload, ids, assertion, ok := judgePayload(ep, gate)
		if !ok || assertion != want || len(ids) == 0 || payload == nil {
			t.Errorf("%s: %v %v %q %v", gate, payload, ids, assertion, ok)
		}
	}
	if _, _, _, ok := judgePayload(bucket1.Episode{ID: "x"}, "B1"); ok {
		t.Error("B1 with no claims has nothing to judge")
	}
	if _, _, _, ok := judgePayload(ep, "B2"); ok {
		t.Error("B2 is deterministic and has no model call")
	}
	ep.Precedents = nil
	if _, _, _, ok := judgePayload(ep, "B5"); ok {
		t.Error("B5 with no precedents has nothing to judge")
	}
}

func TestJudgmentOfMapsCitedIdsBackToTheEpisodeAndDropsStrangers(t *testing.T) {
	resp := workerclient.DecisionJudgeResponse{Model: "m", Dimensions: []workerclient.DimensionVerdict{
		{Dimension: "inference_boundary", Verdict: "pass", Why: "ok", EvidenceRefs: []string{"c1", "a1", "made-up"}},
		{Dimension: "other", Verdict: "fail", Why: "x", EvidenceRefs: []string{"a1"}}}}
	j := judgmentOf(episode(), "B1", "inference_boundary", resp)
	if len(j.Assertions) != 1 || len(j.Assertions[0].Refs) != 2 || j.Assertions[0].Refs[0].ClaimID != "c1" || j.Model != "m" {
		t.Fatalf("%+v", j)
	}
}
