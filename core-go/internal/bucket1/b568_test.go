package bucket1

import (
	"testing"
	"time"
)

func goodPrecedent() Precedent {
	return Precedent{ID: "pr1", CreatedAt: t0.Add(-30 * 24 * time.Hour), SharedFeatures: []string{"transition", "stage"},
		HumanChoice: "email the champion", CustomerResponse: "replied within a day", Differences: []string{"older deal"},
		Lesson: "a quick champion email got a reply", LessonCites: "both", ClaimsSimilar: "analogous"}
}

func TestB5And6WithNoPrecedentAreUnknownForThatReason(t *testing.T) {
	ep := goodEpisode()
	for _, r := range []Result{GradeB5(ep, nil), GradeB6(ep)} {
		if r.Verdict != Unknown || r.Why == "" || len(r.EvidenceRefs) == 0 {
			t.Fatalf("%s: %+v", r.Gate, r)
		}
		if got := r.Why; got[:12] != "no precedent" {
			t.Fatalf("reason = %q", got)
		}
	}
}

func TestB5FutureLeakageFails(t *testing.T) {
	ep := goodEpisode()
	p := goodPrecedent()
	p.CreatedAt = t0 // same instant is not before the event
	ep.Precedents = []Precedent{goodPrecedent(), p}
	p.ID = "pr2"
	ep.Precedents[1] = p
	wantVerdict(t, byName(t, GradeB5(ep, nil), "no_future_leakage"), Fail)
	ep.Precedents = []Precedent{goodPrecedent()}
	r := GradeB5(ep, nil)
	wantVerdict(t, byName(t, r, "no_future_leakage"), Pass)
	wantVerdict(t, byName(t, r, "relevance_and_misses"), Unknown)
}

func TestB5TextualSimilarityIsNotRelevance(t *testing.T) {
	ep := goodEpisode()
	p := goodPrecedent()
	p.TextOnlyMatch = true
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB5(ep, nil), "situational_not_textual"), Fail)
	p.TextOnlyMatch, p.SharedFeatures = false, []string{"stage"}
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB5(ep, nil), "situational_not_textual"), Fail)
}

func TestB6GoodPrecedentPasses(t *testing.T) {
	ep := goodEpisode()
	ep.Precedents = []Precedent{goodPrecedent()}
	if r := GradeB6(ep); r.Verdict != Pass {
		t.Fatalf("%s %s", r.Verdict, r.Why)
	}
}

func TestB6HumanChoiceIsNotTruth(t *testing.T) {
	ep := goodEpisode()
	p := goodPrecedent()
	p.LessonCites = "human_choice"
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB6(ep), "human_choice_not_truth"), Fail)
}

func TestB6ALessonNeedsAnObservedResponse(t *testing.T) {
	ep := goodEpisode()
	p := goodPrecedent()
	p.CustomerResponse = ""
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB6(ep), "outcome_represented_and_separate"), Fail)
	p = goodPrecedent()
	p.LessonCites = "none"
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB6(ep), "outcome_represented_and_separate"), Fail)
}

func TestB6OverclaimedAnalogyFailsAndUnstatedDifferencesWarn(t *testing.T) {
	ep := goodEpisode()
	p := goodPrecedent()
	p.ClaimsSimilar, p.Differences = "same", nil
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB6(ep), "analogy_not_overclaimed"), Fail)
	p.ClaimsSimilar = "similar"
	ep.Precedents = []Precedent{p}
	wantVerdict(t, byName(t, GradeB6(ep), "analogy_not_overclaimed"), Warn)
}

func b8Beliefs() []Belief {
	return []Belief{
		{Kind: "blocker", Statement: "Legal review is blocked until the DPA is signed", Refs: []Ref{{ActivityID: "a1", ClaimID: "c1"}}},
		{Kind: "next_decision", Statement: "Pilot start on March 20", Refs: []Ref{{ActivityID: "a1", ClaimID: "c2"}}},
	}
}

func TestB8GoodSynthesisIsUnknownOnlyForTheModelJudgment(t *testing.T) {
	ep := goodEpisode()
	ep.Beliefs = b8Beliefs()
	r := GradeB8(ep, nil)
	for _, n := range []string{"beliefs_traceable", "blockers_commitments_stakeholders_next_decision"} {
		wantVerdict(t, byName(t, r, n), Pass)
	}
	wantVerdict(t, byName(t, r, "counter_evidence_not_omitted"), NotApplicable)
	wantVerdict(t, byName(t, r, "confidence_and_omissions"), Unknown)
}

func TestB8UntraceableBeliefFails(t *testing.T) {
	ep := goodEpisode()
	ep.Beliefs = append(b8Beliefs(), Belief{Kind: "summary", Statement: "Deal is healthy"})
	wantVerdict(t, byName(t, GradeB8(ep, nil), "beliefs_traceable"), Fail)
	ep.Beliefs = append(b8Beliefs(), Belief{Kind: "summary", Statement: "x", Refs: []Ref{{ActivityID: "ghost"}}})
	wantVerdict(t, byName(t, GradeB8(ep, nil), "beliefs_traceable"), Fail)
}

func TestB8OmittedCounterEvidenceAndMissingKindsWarn(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].Status = "conflicted"
	ep.Beliefs = []Belief{{Kind: "summary", Statement: "The blockers field is clear", Refs: []Ref{{ActivityID: "a1"}}}}
	r := GradeB8(ep, nil)
	wantVerdict(t, byName(t, r, "counter_evidence_not_omitted"), Warn)
	wantVerdict(t, byName(t, r, "blockers_commitments_stakeholders_next_decision"), Warn)
	if r.Verdict != Warn {
		t.Fatal(r.Verdict)
	}
}

func TestB8NoBeliefsIsNotMeasured(t *testing.T) {
	if GradeB8(goodEpisode(), nil).Verdict != Unknown {
		t.Fatal("not measured")
	}
}
