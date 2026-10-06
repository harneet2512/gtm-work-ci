package bucket1

import (
	"testing"
	"time"
)

func TestB1GoodEpisodeIsUnknownUntilTheInferenceBoundaryIsJudged(t *testing.T) {
	r := GradeB1(goodEpisode(), nil)
	for _, n := range []string{"source_preserved", "speaker_correct", "dates_correct", "fact_inference_separated", "critical_facts_present", "no_double_count"} {
		wantVerdict(t, byName(t, r, n), Pass)
	}
	wantVerdict(t, byName(t, r, "contradictions_preserved"), NotApplicable)
	wantVerdict(t, byName(t, r, "inference_boundary"), Unknown)
	if r.Verdict != Unknown {
		t.Fatalf("with the model judgment missing the gate is %s, never a pass", r.Verdict)
	}
	if r.Gate != "B1" || r.Calibrated || len(r.EvidenceRefs) == 0 {
		t.Fatalf("result shape: %+v", r)
	}
}

func TestB1AQuoteThatIsNotInTheSourceFails(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].Quote = "legal review is approved"
	a := byName(t, GradeB1(ep, nil), "source_preserved")
	wantVerdict(t, a, Fail)
	if len(a.Refs) == 0 || a.Refs[0].ClaimID != "c1" {
		t.Fatalf("the failure cites the claim: %+v", a.Refs)
	}
}

func TestB1WhitespaceDifferencesInTheQuoteAreNotCorruption(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].Quote = "DPA   is  signed" // the source says "DPA is signed"
	wantVerdict(t, byName(t, GradeB1(ep, nil), "source_preserved"), Pass)
}

func TestB1WrongSpeakerAndWrongDateFail(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].SpeakerID = "p-int"
	ep.Claims[1].OccurredAt = t0.Add(24 * time.Hour)
	r := GradeB1(ep, nil)
	wantVerdict(t, byName(t, r, "speaker_correct"), Fail)
	wantVerdict(t, byName(t, r, "dates_correct"), Fail)
}

func TestB1AnInferenceDressedAsAFactFails(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[2].Kind = "fact" // "at risk" has no quote: it is concluded, not stated
	wantVerdict(t, byName(t, GradeB1(ep, nil), "fact_inference_separated"), Fail)
}

func TestB1AnOmittedCriticalFactFails(t *testing.T) {
	ep := goodEpisode()
	ep.CriticalFacts = append(ep.CriticalFacts, "budget approved by the CFO")
	a := byName(t, GradeB1(ep, nil), "critical_facts_present")
	wantVerdict(t, a, Fail)
}

func TestB1NoGoldListMeansCriticalFactsAreNotMeasured(t *testing.T) {
	ep := goodEpisode()
	ep.CriticalFacts = nil
	wantVerdict(t, byName(t, GradeB1(ep, nil), "critical_facts_present"), Unknown)
}

func TestB1ASilentlyResolvedContradictionFails(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].Field = "stage" // a scalar field holds one value at a time (list fields hold several)
	ep.Claims = append(ep.Claims, Claim{ID: "c4", ActivityID: "a1", AccountID: "acc-1", Kind: "fact", Field: "stage", Value: "DPA signed",
		Quote: "the DPA is signed", SpeakerID: "p-ext", OccurredAt: t0, Status: "active"})
	wantVerdict(t, byName(t, GradeB1(ep, nil), "contradictions_preserved"), Fail)
	ep.Claims[3].ConflictsWith = []string{"c1"}
	ep.Claims[0].ConflictsWith = []string{"c4"}
	ep.Claims[0].Status, ep.Claims[3].Status = "conflicted", "conflicted"
	wantVerdict(t, byName(t, GradeB1(ep, nil), "contradictions_preserved"), Pass)
}

func TestB1ADuplicateDeliveryIsCountedOnce(t *testing.T) {
	ep := goodEpisode()
	dup := ep.Activities[0]
	dup.ID = "a1-again"
	ep.Activities = append(ep.Activities, dup)
	wantVerdict(t, byName(t, GradeB1(ep, nil), "no_double_count"), Fail)
}

func TestB1AModelJudgmentSettlesTheInferenceBoundaryOnlyWithEvidence(t *testing.T) {
	ep := goodEpisode()
	j := map[string]Judgment{"B1": {Gate: "B1", Model: "qwen/qwen3.8-flash", Assertions: []Assertion{
		{Name: "inference_boundary", Verdict: Pass, Why: "the risk reading is labelled an inference", Refs: []Ref{{ActivityID: "a1", ClaimID: "c3"}}}}}}
	r := GradeB1(ep, j)
	wantVerdict(t, byName(t, r, "inference_boundary"), Pass)
	if r.Verdict != Pass {
		t.Fatalf("all measured and passing = %s (%s)", r.Verdict, r.Why)
	}
	// Rule R1: a model pass that cites nothing, or cites an activity that does not exist, is unknown.
	j["B1"].Assertions[0].Refs = nil
	wantVerdict(t, byName(t, GradeB1(ep, j), "inference_boundary"), Unknown)
	j["B1"].Assertions[0].Refs = []Ref{{ActivityID: "ghost"}}
	wantVerdict(t, byName(t, GradeB1(ep, j), "inference_boundary"), Unknown)
}

func TestB1AnEpisodeWithNoActivitiesIsNotMeasured(t *testing.T) {
	ep := goodEpisode()
	ep.Activities, ep.Claims = nil, nil
	if r := GradeB1(ep, nil); r.Verdict != Unknown {
		t.Fatalf("verdict = %s", r.Verdict)
	}
}
