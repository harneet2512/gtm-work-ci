package transitioneval

import (
	"math"
	"testing"
)

func status(s string) *string { return &s }

// step builds a result: gold status g (None = no transition), predicted p, both targeting EXPANSION when not None.
func step(g, p string) StepResult {
	r := StepResult{Scenario: g + "-" + p, Slice: "x", Pred: Prediction{Status: p, RelationshipStateAfter: "REORG"}, Gold: Label{RelationshipStateAfter: "REORG"}}
	if g != None {
		r.Gold.Status, r.Gold.ToState = status(g), status("EXPANSION")
	}
	if p != None {
		r.Pred.ToState = "EXPANSION"
	}
	return r
}

func approx(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestStatusAccuracyAndDetectionCounts(t *testing.T) {
	results := []StepResult{
		step("CONFIRMED", "CONFIRMED"), step("CANDIDATE", "CANDIDATE"), step("CANDIDATE", "CONFIRMED"),
		step(None, "CANDIDATE"), step("REJECTED", None), step(None, None),
	}
	m := Compute(results, func(StepResult) bool { return true })
	if m.Steps != 6 || m.StatusAccuracy.Num != 3 || m.StatusAccuracy.Den != 6 {
		t.Fatalf("accuracy = %+v over %d steps", m.StatusAccuracy, m.Steps)
	}
	// detection: gold transitions are CONFIRMED, CANDIDATE, CANDIDATE, REJECTED; predicted are CONFIRMED, CANDIDATE, CONFIRMED, CANDIDATE
	if m.Detection.TP != 3 || m.Detection.FP != 1 || m.Detection.FN != 1 {
		t.Errorf("detection = %+v, want tp 3 fp 1 fn 1", m.Detection)
	}
	approx(t, "detection precision", m.Detection.Precision, 0.75)
	approx(t, "detection recall", m.Detection.Recall, 0.75)
	if c := m.PerStatus["CONFIRMED"]; c.TP != 1 || c.FP != 1 || c.FN != 0 {
		t.Errorf("CONFIRMED = %+v, want tp 1 fp 1 fn 0", c)
	}
	if c := m.PerStatus["CANDIDATE"]; c.TP != 1 || c.FP != 1 || c.FN != 1 {
		t.Errorf("CANDIDATE = %+v, want tp 1 fp 1 fn 1", c)
	}
}

func TestPrematurePromotionAndUncertaintyMarking(t *testing.T) {
	results := []StepResult{step("CANDIDATE", "CONFIRMED"), step("UNRESOLVED", "CANDIDATE"), step("CANDIDATE", None), step("CONFIRMED", "CONFIRMED")}
	m := Compute(results, func(StepResult) bool { return true })
	if m.PrematurePromotion.Num != 1 || m.PrematurePromotion.Den != 2 {
		t.Errorf("premature = %+v, want 1 of the 2 CONFIRMED predictions", m.PrematurePromotion)
	}
	if m.UncertaintyMarking.Num != 1 || m.UncertaintyMarking.Den != 3 {
		t.Errorf("uncertainty = %+v, want 1 of the 3 gold-uncertain steps marked CANDIDATE or UNRESOLVED", m.UncertaintyMarking)
	}
}

func TestFactLevelScoringSkipsUnlabelledAndUnresolvedSteps(t *testing.T) {
	scored := step("CANDIDATE", "CANDIDATE")
	scored.Gold.SupportingRequired, scored.Gold.Contradicting = []string{"a", "b"}, []string{}
	scored.Pred.SupportingRequired, scored.Pred.Contradicting = []string{"a", "c"}, []string{"x"}
	unlabelled := step("CANDIDATE", "CANDIDATE") // no supporting_required label: not scored
	unlabelled.Pred.SupportingRequired = []string{"z"}
	unresolved := step("UNRESOLVED", "UNRESOLVED") // the spec does not say which facts it keeps
	unresolved.Gold.SupportingRequired = []string{"q"}
	unresolved.Pred.SupportingRequired = []string{"r"}
	m := Compute([]StepResult{scored, unlabelled, unresolved}, func(StepResult) bool { return true })
	if f := m.SupportingFacts; f.TP != 1 || f.FP != 1 || f.FN != 1 {
		t.Errorf("supporting = %+v, want tp 1 fp 1 fn 1 from the one labelled step", f)
	}
	if f := m.ContradictingFacts; f.TP != 0 || f.FP != 1 || f.FN != 0 {
		t.Errorf("contradicting = %+v, want the unexpected x counted once", f)
	}
}

func TestAFalseTransitionOnAGoldNoneStepCountsItsFactsAsFalsePositives(t *testing.T) {
	r := step(None, "CANDIDATE")
	r.Pred.SupportingRequired = []string{"a"}
	m := Compute([]StepResult{r}, func(StepResult) bool { return true })
	if m.SupportingFacts.FP != 1 || len(m.Disagreements) != 1 || m.Disagreements[0].Why == "" {
		t.Errorf("supporting = %+v, disagreements = %+v", m.SupportingFacts, m.Disagreements)
	}
}

func TestStaleClosureAndRelationshipStateAreScored(t *testing.T) {
	closed := step("UNRESOLVED", "UNRESOLVED")
	closed.Gold.Closed, closed.Pred.Closed = true, false
	wrongState := step("CONFIRMED", "CONFIRMED")
	wrongState.Pred.RelationshipStateAfter = "unknown"
	m := Compute([]StepResult{closed, wrongState}, func(StepResult) bool { return true })
	if m.StaleClosure.Num != 0 || m.StaleClosure.Den != 1 {
		t.Errorf("stale closure = %+v, want 0 of 1", m.StaleClosure)
	}
	if m.RelationshipState.Num != 1 || m.RelationshipState.Den != 2 {
		t.Errorf("relationship state = %+v, want 1 of 2", m.RelationshipState)
	}
	if len(m.Disagreements) != 2 {
		t.Errorf("disagreements = %d, want both steps listed", len(m.Disagreements))
	}
}

func TestEmptyDenominatorsAreNilNotZero(t *testing.T) {
	m := Compute(nil, func(StepResult) bool { return true })
	if m.StatusAccuracy.Value != nil || m.Detection.Precision != nil || m.PrematurePromotion.Value != nil {
		t.Errorf("an empty set must report n/a, not 0: %+v", m)
	}
}

func TestKeepSelectsSteps(t *testing.T) {
	a, b := step("CANDIDATE", "CANDIDATE"), step("CANDIDATE", None)
	b.Slice = "other"
	m := Compute([]StepResult{a, b}, func(r StepResult) bool { return r.Slice == "x" })
	if m.Steps != 1 || m.Scenarios != 1 || m.StatusAccuracy.Num != 1 {
		t.Errorf("got %+v, want only the first step", m)
	}
}
