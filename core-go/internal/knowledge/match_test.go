package knowledge

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// keepChampion is the HAR-97 L4 example lesson with neutral data: preserve an active champion when a new
// stakeholder enters an expansion motion, unless ownership was delegated or the champion left.
func keepChampion() Knowledge {
	return k(
		[]Condition{cond("motion", OpEq, "expansion"), cond("champion_status", OpEq, "active"),
			cond("diff.new_stakeholder_entered", OpExists)},
		exc("Champion explicitly delegated ownership", cond("champion_status", OpEq, "delegated")),
		exc("Champion departed", cond("buying_group.champion", OpIn, list("departed", "inactive"))),
	)
}

func expansionState(championStatus string, memberStatus string) Situation {
	s := situation(map[string]Value{"motion": known("expansion"), "champion_status": known(championStatus)})
	s.BuyingGroup = []Member{{PersonID: "p1", Roles: []string{"champion"}, Status: memberStatus}}
	s.Signals = []Signal{signal("new_stakeholder_entered", 2*24*time.Hour)}
	return s
}

func TestSignatureHoldsAndNoExceptionApplies(t *testing.T) {
	r := mustMatch(t, keepChampion(), expansionState("active", "active"))
	if r.Label != LabelApplies || !r.Entry.Applies {
		t.Fatalf("got %s applies=%v", r.Label, r.Entry.Applies)
	}
	if len(r.Entry.MatchedConditions) != 3 || len(r.Entry.UnmatchedConditions) != 0 || len(r.Entry.ExceptionsChecked) != 2 {
		t.Fatalf("entry = %+v", r.Entry)
	}
	for _, x := range r.Entry.ExceptionsChecked {
		if x.Triggered || x.EvidenceRefs != nil {
			t.Fatalf("exception should not fire: %+v", x)
		}
	}
}

// HAR-97 §16 "Why does that learning apply to this account now?": the entry cites the state and signals.
func TestEntryCarriesTheCurrentEvidenceOfMatchedConditions(t *testing.T) {
	s := expansionState("active", "active")
	s.Fields["motion"] = Value{Known: true, Scalar: "expansion", EvidenceRefs: []EvidenceRef{{ActivityID: "a1", ClaimID: "c1"}, {ActivityID: "a1", ClaimID: "c1"}}}
	r := mustMatch(t, keepChampion(), s)
	want := []EvidenceRef{{ActivityID: "a1", ClaimID: "c1"}, {ActivityID: `act-"active"`}, {ActivityID: "act-new_stakeholder_entered"}}
	if !reflect.DeepEqual(r.Entry.CurrentEvidenceRefs, want) {
		t.Fatalf("current evidence = %v", r.Entry.CurrentEvidenceRefs)
	}
}

// current_evidence_refs is set only on APPLIES entries (re-review LOW): DOES_NOT_APPLY and
// EXCEPTION_TRIGGERED entries must not cite evidence for a conclusion the matcher did not draw.
func TestCurrentEvidenceOnlyOnApplyingEntries(t *testing.T) {
	cases := map[string]Situation{
		LabelDoesNotApply:       expansionState("delegated", "active"),
		LabelExceptionTriggered: expansionState("active", "departed"),
	}
	for want, s := range cases {
		r := mustMatch(t, keepChampion(), s)
		if r.Label != want || r.Entry.CurrentEvidenceRefs != nil {
			t.Fatalf("%s: label %s, current evidence %v", want, r.Label, r.Entry.CurrentEvidenceRefs)
		}
	}
	if r := mustMatch(t, keepChampion(), expansionState("active", "active")); len(r.Entry.CurrentEvidenceRefs) == 0 {
		t.Fatal("an APPLIES entry must cite its current evidence")
	}
}

func TestFailingSignatureNeverChecksExceptions(t *testing.T) {
	// Champion delegated: the signature (champion active) fails, so the delegation exception is not reached.
	r := mustMatch(t, keepChampion(), expansionState("delegated", "active"))
	if r.Label != LabelDoesNotApply || r.Entry.Applies {
		t.Fatalf("got %s", r.Label)
	}
	if len(r.Entry.ExceptionsChecked) != 0 {
		t.Fatalf("exceptions were checked: %+v", r.Entry.ExceptionsChecked)
	}
	if !reflect.DeepEqual(r.Entry.UnmatchedConditions, []string{`champion_status eq "active"`}) {
		t.Fatalf("unmatched = %v", r.Entry.UnmatchedConditions)
	}
}

func TestExceptionAfterSignatureOverridesTheDefault(t *testing.T) {
	r := mustMatch(t, keepChampion(), expansionState("active", "departed"))
	if r.Label != LabelExceptionTriggered || r.Entry.Applies {
		t.Fatalf("got %s applies=%v", r.Label, r.Entry.Applies)
	}
	fired := r.Entry.ExceptionsChecked[1]
	if fired.Exception != "Champion departed" || !fired.Triggered {
		t.Fatalf("exception check = %+v", fired)
	}
	if r.Entry.ExceptionsChecked[0].Triggered {
		t.Fatal("delegation exception should not fire")
	}
}

func TestTriggeredExceptionCitesTheEvidenceOfItsConditions(t *testing.T) {
	kn := k([]Condition{cond("motion", OpEq, "expansion")},
		exc("security blocker open", cond("diff.security_blocker_appeared", OpExists), cond("relationship_risk", OpEq, "high")))
	s := situation(map[string]Value{
		"motion": known("expansion"), "relationship_risk": known("high"),
		"blockers": items(Item{Text: "Security review", Status: "open"}),
	})
	s.Signals = []Signal{signal("security_blocker_appeared", 30*24*time.Hour)}
	r := mustMatch(t, kn, s)
	got := r.Entry.ExceptionsChecked[0].EvidenceRefs
	want := []EvidenceRef{{ActivityID: "act-security_blocker_appeared"}, {ActivityID: `act-"high"`}}
	if r.Label != LabelExceptionTriggered || !reflect.DeepEqual(got, want) {
		t.Fatalf("label %s refs %v", r.Label, got)
	}
}

func TestApplicabilityConditionsNarrowTheSignature(t *testing.T) {
	kn := keepChampion()
	kn.ApplicabilityConditions = []Condition{cond("transition.status", OpEq, "CANDIDATE")}
	s := expansionState("active", "active")
	if r := mustMatch(t, kn, s); r.Label != LabelDoesNotApply || r.Entry.UnmatchedConditions[0] != `transition.status eq "CANDIDATE"` {
		t.Fatalf("no open transition: got %s %v", r.Label, r.Entry.UnmatchedConditions)
	}
	s.Transition = &Transition{FromState: "REORG", ToState: "EXPANSION", Status: "CANDIDATE"}
	if r := mustMatch(t, kn, s); r.Label != LabelApplies || len(r.Entry.MatchedConditions) != 4 {
		t.Fatalf("candidate transition: got %s %v", r.Label, r.Entry.MatchedConditions)
	}
}

// The HAR-97 §7/§8 lesson is scoped to REORG -> EXPANSION while CANDIDATE: it must stop applying once confirmed.
func TestTransitionScopedLessonFollowsTransitionStatus(t *testing.T) {
	kn := k([]Condition{cond("relationship_state", OpEq, "REORG"), cond("transition.to_state", OpEq, "EXPANSION"),
		cond("transition.status", OpEq, "CANDIDATE")},
		exc("owner already stabilized", cond("buying_group.champion", OpEq, "active")))
	candidate := with(situation(map[string]Value{}), func(s *Situation) {
		s.RelationshipState = "REORG"
		s.Transition = &Transition{FromState: "REORG", ToState: "EXPANSION", Status: "CANDIDATE"}
	})
	if r := mustMatch(t, kn, candidate); r.Label != LabelApplies {
		t.Fatalf("candidate: %s", r.Label)
	}
	confirmed := with(candidate, func(s *Situation) { s.RelationshipState, s.Transition = "EXPANSION", nil })
	if r := mustMatch(t, kn, confirmed); r.Label != LabelDoesNotApply || len(r.Entry.UnmatchedConditions) != 3 {
		t.Fatalf("confirmed: %s %v", r.Label, r.Entry.UnmatchedConditions)
	}
	stabilized := with(candidate, func(s *Situation) {
		s.BuyingGroup = []Member{{PersonID: "p2", Roles: []string{"champion"}, Status: "active"}}
	})
	if r := mustMatch(t, kn, stabilized); r.Label != LabelExceptionTriggered {
		t.Fatalf("stabilized owner: %s", r.Label)
	}
}

func TestMatchAllEvaluatesEveryKnowledgeObject(t *testing.T) {
	other := k([]Condition{cond("motion", OpEq, "renewal")})
	other.ID = "00000000-0000-4000-8000-000000000021"
	rs, err := MatchAll([]Knowledge{keepChampion(), other}, expansionState("active", "active"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Label != LabelApplies || rs[1].Label != LabelDoesNotApply || rs[1].Entry.KnowledgeID != other.ID {
		t.Fatalf("results = %+v", rs)
	}
}

func TestMatchAllStopsAtInvalidKnowledge(t *testing.T) {
	bad := k([]Condition{cond("mood", OpEq, "x")})
	if _, err := MatchAll([]Knowledge{keepChampion(), bad}, expansionState("active", "active")); !errors.Is(err, ErrInvalidCondition) {
		t.Fatalf("err = %v", err)
	}
}

func TestMatchDoesNotMutateItsInputs(t *testing.T) {
	kn := keepChampion()
	kn.ApplicabilityConditions = make([]Condition, 0, 8) // spare capacity must not be written through
	s := expansionState("active", "active")
	before := len(s.Signals)
	_ = mustMatch(t, kn, s)
	if len(kn.ApplicabilityConditions) != 0 || cap(kn.SituationSignature) != 3 || len(s.Signals) != before {
		t.Fatal("inputs changed")
	}
}
