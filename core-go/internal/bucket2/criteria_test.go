package bucket2

import "testing"

func TestFromDimensionsKeepsEveryCriterion(t *testing.T) {
	ev := []string{"candidate:c"}
	r := FromDimensions(j(
		Dimension{"grounding", "pass", "every claim is sourced", ev},
		Dimension{"cta_strength", "warn", "the ask is early", ev},
		Dimension{"recipients", "pass", "right people", nil}, // R1: no evidence, so unknown
	))
	if len(r.Criteria) != 3 {
		t.Fatalf("criteria = %+v", r.Criteria)
	}
	want := []Criterion{
		{ID: "grounding", Label: "Grounding", Result: "pass", Why: "every claim is sourced", EvidenceRefs: ev},
		{ID: "cta_strength", Label: "Cta strength", Result: "warn", Why: "the ask is early", EvidenceRefs: ev},
		{ID: "recipients", Label: "Recipients", Result: "unknown", Why: "no evidence was cited, so this is unknown rather than pass (was: right people)", EvidenceRefs: []string{}},
	}
	for i, w := range want {
		g := r.Criteria[i]
		if g.ID != w.ID || g.Label != w.Label || g.Result != w.Result || g.Why != w.Why || len(g.EvidenceRefs) != len(w.EvidenceRefs) {
			t.Errorf("criterion %d = %+v, want %+v", i, g, w)
		}
	}
	if r.Verdict != Warn {
		t.Fatalf("folded verdict = %s", r.Verdict)
	}
}

func TestFromDimensionsWithNoDimensionsHasNoCriteria(t *testing.T) {
	if r := FromDimensions(j()); len(r.Criteria) != 0 {
		t.Fatalf("invented criteria: %+v", r.Criteria)
	}
}

func TestCriterionResultMustBeKnown(t *testing.T) {
	r := Result{Gate: "D2", JudgedType: "StrategyCandidate", JudgedID: "c", SpanID: "candidates:s", Verdict: Pass, Observed: "o", Why: "w",
		EvidenceRefs: []string{"x"}, Grader: Deterministic, Criteria: []Criterion{{ID: "a", Label: "A", Result: "great", Why: "w"}}}.Finalize()
	if err := r.Validate(); err == nil {
		t.Fatal("a criterion result outside pass/warn/fail/unknown/not_applicable must be refused")
	}
}
