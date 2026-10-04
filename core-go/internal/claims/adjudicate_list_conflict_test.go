package claims

import "testing"

func TestListItemStatusConflictsAreSurfacedLikeScalarOnes(t *testing.T) {
	// A rep-approved "resolved" outranks a newer AI "open" for the same blocker; the disagreement is surfaced.
	human := mk("h", FieldBlockers, `"resolved: EU budget line"`, HumanApproved, 1, at(0))
	ai := mk("ai", FieldBlockers, `{"text":"EU budget line","status":"open"}`, FirstPartyAI, 0.9, at(5))
	agree := mk("same", FieldBlockers, `"resolved: eu budget line."`, FirstPartyAI, 0.9, at(6))
	other := mk("o", FieldBlockers, `"Unrelated blocker"`, FirstPartyAI, 0.9, at(7))
	a := Adjudicate([]Claim{human, ai, agree, other}, at(10))

	items := a.Items(FieldBlockers)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	var budget Slot
	for _, s := range items {
		if s.Key == ItemKey("EU budget line") {
			budget = s
		}
	}
	if budget.Winner.ID != "h" || len(budget.Conflicts) != 1 || budget.Conflicts[0].Contradicting.ID != "ai" {
		t.Fatalf("winner=%s conflicts=%+v: only the disagreeing status conflicts, an agreeing newer claim does not", budget.Winner.ID, budget.Conflicts)
	}
	if len(a.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", a.Conflicts)
	}
}

func TestPerPersonSlotsNeverRaiseConflicts(t *testing.T) {
	crm := mk("crm", FieldBuyingGroupMember, `{"title":"Head of Security"}`, CRMExplicit, 1, at(0))
	crm.SubjectPersonID = "marco"
	ai := mk("ai", FieldBuyingGroupMember, `{"title":"CISO"}`, FirstPartyAI, 0.9, at(5))
	ai.SubjectPersonID = "marco"
	if got := Adjudicate([]Claim{crm, ai}, at(10)).Conflicts; len(got) != 0 {
		t.Fatalf("conflicts = %+v", got)
	}
}
