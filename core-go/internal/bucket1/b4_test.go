package bucket1

import "testing"

func TestB4GoodEpisodePasses(t *testing.T) {
	r := GradeB4(goodEpisode())
	if r.Verdict != Pass || r.JudgedObject.Type != "StateDiff" {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
}

func TestB4DiffAndGraphDiffMustAgree(t *testing.T) {
	ep := goodEpisode()
	ep.GraphDiffFields = []string{"blockers", "stage"}
	a := byName(t, GradeB4(ep), "diff_matches_graph_diff")
	wantVerdict(t, a, Fail)
	ep.GraphDiffKnown = false
	wantVerdict(t, byName(t, GradeB4(ep), "diff_matches_graph_diff"), Unknown)
}

func TestB4AChangeWithoutEvidenceFails(t *testing.T) {
	ep := goodEpisode()
	ep.StateDiff[0].Refs = nil
	wantVerdict(t, byName(t, GradeB4(ep), "diff_traceable_to_evidence"), Fail)
	ep = goodEpisode()
	ep.StateDiff[0].Refs = []Ref{{ActivityID: "ghost"}}
	wantVerdict(t, byName(t, GradeB4(ep), "diff_traceable_to_evidence"), Fail)
}

func TestB4ALocalityViolationFails(t *testing.T) {
	ep := goodEpisode()
	ep.StateDiff = append(ep.StateDiff, FieldChange{Field: "owner", Before: "Sam", After: "Kim", Material: true, Refs: []Ref{{ActivityID: "a1"}}})
	ep.GraphDiffFields = append(ep.GraphDiffFields, "owner")
	wantVerdict(t, byName(t, GradeB4(ep), "mutation_locality"), Fail)
}

func TestB4MaterialityFollowsTheStatediffRule(t *testing.T) {
	ep := goodEpisode()
	ep.StateDiff[0].Material = false // blockers are material
	wantVerdict(t, byName(t, GradeB4(ep), "material_vs_nonmaterial"), Fail)
	ep = goodEpisode()
	ep.StateDiff = append(ep.StateDiff, FieldChange{Field: "summary", Material: true, Refs: []Ref{{ActivityID: "a1"}}})
	wantVerdict(t, byName(t, GradeB4(ep), "material_vs_nonmaterial"), Fail)
}

func TestB4AnUnsupportedTransitionFails(t *testing.T) {
	ep := goodEpisode()
	ep.Transition = &Transition{ID: "tr1", Status: "confirmed"}
	wantVerdict(t, byName(t, GradeB4(ep), "no_unsupported_transition"), Fail)
	ep.Transition.Support = []Ref{{ActivityID: "a1", ClaimID: "c1"}}
	wantVerdict(t, byName(t, GradeB4(ep), "no_unsupported_transition"), Pass)
	ep.Transition = &Transition{ID: "tr1", Status: "weird"}
	wantVerdict(t, byName(t, GradeB4(ep), "no_unsupported_transition"), Fail)
	ep.Transition = &Transition{ID: "tr1", Status: "unresolved"}
	wantVerdict(t, byName(t, GradeB4(ep), "no_unsupported_transition"), Pass)
}

func TestB4UnresolvedMustStayUnresolved(t *testing.T) {
	ep := goodEpisode()
	ep.Unresolved = []string{"economic_buyer"}
	ep.StateAfter = map[string]string{"economic_buyer": "Dana"}
	wantVerdict(t, byName(t, GradeB4(ep), "unresolved_kept"), Fail)
	ep.StateAfter["economic_buyer"] = "UNRESOLVED"
	wantVerdict(t, byName(t, GradeB4(ep), "unresolved_kept"), Pass)
}

func TestB4NothingRecordedIsNotMeasured(t *testing.T) {
	ep := goodEpisode()
	ep.StateDiff, ep.GraphDiffKnown = nil, false
	if r := GradeB4(ep); r.Verdict != Unknown {
		t.Fatal(r.Verdict)
	}
}

func TestB4GraphDiffByPresenceWhenOnlyTheGraphWasProjected(t *testing.T) {
	ep := goodEpisode()
	ep.GraphDiffKnown, ep.GraphProjected, ep.GraphChanges = false, true, 3
	wantVerdict(t, byName(t, GradeB4(ep), "diff_matches_graph_diff"), Pass)
	ep.GraphChanges = 0
	wantVerdict(t, byName(t, GradeB4(ep), "diff_matches_graph_diff"), Fail)
	ep.StateDiff = nil
	wantVerdict(t, byName(t, GradeB4(ep), "diff_matches_graph_diff"), NotApplicable)
}

func TestB4DerivedFieldsNeedNoClaimOfTheEvent(t *testing.T) {
	ep := goodEpisode()
	ep.StateDiff = append(ep.StateDiff, FieldChange{Field: "relationship_state", Material: true, Refs: []Ref{{ActivityID: "a1"}}})
	wantVerdict(t, byName(t, GradeB4(ep), "mutation_locality"), Pass)
}
