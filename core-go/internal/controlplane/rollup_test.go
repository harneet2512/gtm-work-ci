package controlplane

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalarea"
)

func TestAreaCountsAlwaysListsFourAreasInOrderAndSumsToTheTotal(t *testing.T) {
	cur := []result{
		res("1", "provenance_coverage", "pass", false),        // E1 intelligence
		res("2", "champion_continuity", "fail", true),         // E8 decision_learning
		res("3", "knowledge_applicability", "abstain", false), // E6 decision_learning
		res("4", "cta_calibration", "warn", false),            // E12 decision_learning (ACT)
	}
	areas := areaCounts(cur, nil, false)
	want := []struct {
		area     string
		measured bool
		total    int
	}{{"intelligence", true, 1}, {"decision_learning", true, 3}, {"cliff_experience", false, 0}, {"system", false, 0}}
	if len(areas) != 4 {
		t.Fatalf("areas = %d", len(areas))
	}
	sum := 0
	for i, w := range want {
		a := areas[i]
		if a.Area != w.area || a.Measured != w.measured || a.Counts.Total != w.total || a.Order != i+1 || a.Label == "" {
			t.Errorf("area %d = %+v, want %+v", i, a, w)
		}
		if a.Delta != nil {
			t.Errorf("area %s: no previous run, delta must be null, got %+v", a.Area, a.Delta)
		}
		sum += a.Counts.Total
	}
	if sum != tally(cur).Total {
		t.Fatalf("area totals %d != run total %d", sum, tally(cur).Total)
	}
	if areas[1].Counts.Fail != 1 || areas[1].Counts.BlockingFail != 1 || areas[1].Counts.Unknown != 1 {
		t.Errorf("decision_learning counts = %+v", areas[1].Counts)
	}
}

func TestAreaCountsDeltaIsAgainstThePreviousRunEvenForAnEmptyArea(t *testing.T) {
	cur := []result{res("1", "provenance_coverage", "pass", false)}
	prev := []result{res("p1", "provenance_coverage", "fail", false), res("p2", "trajectory", "warn", false)}
	areas := areaCounts(cur, prev, true)
	if d := areas[0].Delta; d == nil || d.Pass != 1 || d.Fail != -1 || d.Total != 0 {
		t.Errorf("intelligence delta = %+v", d)
	}
	if d := areas[3].Delta; d == nil || d.Warn != -1 || d.Total != -1 {
		t.Errorf("system delta = %+v (the previous run's warn is gone)", d)
	}
	if d := areas[1].Delta; d == nil || *d != (Counts{}) {
		t.Errorf("an area empty in both runs has a zero delta, got %+v", d)
	}
	if areas[3].Measured {
		t.Error("an area with no result in this run is not measured, whatever the previous run held")
	}
}

func TestFamilySummaryGroupsByFamilyAndEvalTypeAndKeepsVanishedFamilies(t *testing.T) {
	cur := []result{
		res("1", "champion_continuity", "pass", false), res("2", "champion_continuity", "fail", false), res("3", "stakeholder_coverage", "pass", false),
		res("4", "cta_calibration", "warn", false),
	}
	prev := []result{res("p1", "champion_continuity", "fail", false), res("p2", "trajectory", "fail", true)}
	areas := familySummary(cur, prev, true)

	dl := areas[1]
	if dl.Area != "decision_learning" || len(dl.Families) != 2 || dl.Families[0].FamilyID != "E8" || dl.Families[1].FamilyID != "E12" {
		t.Fatalf("decision_learning = %+v", dl)
	}
	e8 := dl.Families[0]
	if e8.Name != "Decision construction" || e8.Counts.Total != 3 || e8.Delta == nil || e8.Delta.Total != 2 {
		t.Errorf("E8 = %+v", e8)
	}
	if len(e8.EvalTypes) != 2 || e8.EvalTypes[0].EvalType != "champion_continuity" || e8.EvalTypes[1].EvalType != "stakeholder_coverage" {
		t.Fatalf("eval types are alphabetical: %+v", e8.EvalTypes)
	}
	cc := e8.EvalTypes[0]
	if cc.Counts.Pass != 1 || cc.Counts.Fail != 1 || len(cc.ResultIDs) != 2 || cc.Delta.Fail != 0 || cc.Delta.Pass != 1 {
		t.Errorf("champion_continuity = %+v", cc)
	}
	if sc := e8.EvalTypes[1]; sc.Delta == nil || sc.Delta.Pass != 1 || len(sc.ResultIDs) != 1 {
		t.Errorf("a type new in this run has a delta equal to its counts: %+v", sc)
	}

	sys := areas[3]
	if sys.Measured || len(sys.Families) != 1 || sys.Families[0].FamilyID != "E18" {
		t.Fatalf("system = %+v: E18 only exists in the previous run and must show as a vanished family", sys)
	}
	v := sys.Families[0]
	if v.Counts.Total != 0 || v.Delta == nil || v.Delta.Fail != -1 || v.Delta.BlockingFail != -1 {
		t.Errorf("vanished family = %+v", v)
	}
	if v.EvalTypes[0].EvalType != "trajectory" || len(v.EvalTypes[0].ResultIDs) != 0 {
		t.Errorf("a vanished type has no result ids in this run: %+v", v.EvalTypes[0])
	}
}

func TestFamilySummaryWithoutAPreviousRunHasNullDeltasEverywhere(t *testing.T) {
	areas := familySummary([]result{res("1", "provenance_coverage", "pass", false)}, nil, false)
	for _, a := range areas {
		if a.Delta != nil {
			t.Errorf("area %s delta %+v", a.Area, a.Delta)
		}
		for _, f := range a.Families {
			if f.Delta != nil {
				t.Errorf("family %s delta %+v", f.FamilyID, f.Delta)
			}
			for _, et := range f.EvalTypes {
				if et.Delta != nil {
					t.Errorf("eval type %s delta %+v", et.EvalType, et.Delta)
				}
			}
		}
	}
}

func TestFamiliesFollowTheRegistryNumberingNotTheAlphabet(t *testing.T) {
	cur := []result{res("1", "evidence_sufficiency", "pass", false), res("2", "knowledge_applicability", "pass", false), res("3", "champion_continuity", "pass", false)}
	var got []string
	for _, f := range familySummary(cur, nil, false)[1].Families {
		got = append(got, f.FamilyID)
	}
	if len(got) != 3 || got[0] != "E6" || got[1] != "E8" || got[2] != "E9" {
		t.Fatalf("families = %v, want E6 E8 E9 (E9 before E10 by number)", got)
	}
}

func TestEvalTypesOutsideTheMappingCountInTheRunButInNoArea(t *testing.T) {
	cur := []result{res("1", "provenance_coverage", "pass", false), res("2", "brand_new_eval", "fail", false)}
	if tally(cur).Total != 2 {
		t.Fatal("the run total keeps every row")
	}
	sum := 0
	for _, a := range areaCounts(cur, nil, false) {
		sum += a.Counts.Total
	}
	if sum != 1 {
		t.Fatalf("an unmapped eval type must not be guessed into an area: area total %d", sum)
	}
}

func TestEveryRegistryTypeIsMappedSoNothingFallsOutOfTheAreas(t *testing.T) {
	for evalType := range evalarea.EvalTypes() {
		if _, ok := evalarea.AreaOf(evalType); !ok {
			t.Errorf("%s has no area", evalType)
		}
	}
}
