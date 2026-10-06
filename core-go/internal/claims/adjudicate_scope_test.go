package claims

import "testing"

// scoped returns a copy of c scoped to a deal ("" = the account).
func scoped(c Claim, opp string) Claim { c.OpportunityID = opp; return c }

func TestClaimsOfDifferentDealsNeverCompete(t *testing.T) {
	a := scoped(mk("a", FieldOwner, `"dana"`, CRMExplicit, 1, at(0)), "dealA")
	b := scoped(mk("b", FieldOwner, `"owen"`, CRMExplicit, 1, at(5)), "dealB") // newer, same standing
	un := mk("un", FieldOwner, `"sam"`, HumanApproved, 1, at(9))               // outranks both, names no deal
	adj := Adjudicate([]Claim{a, b, un}, at(10))

	if len(adj.Updates) != 0 {
		t.Fatalf("each claim wins its own slot, nothing is outranked: %v", adj.Updates)
	}
	if got := adj.Scopes(); len(got) != 2 || got[0] != "dealA" || got[1] != "dealB" {
		t.Fatalf("scopes = %v", got)
	}
	if s := adj.Find(FieldOwner, "", ""); s == nil || s.Winner.ID != "un" || s.Scope != "" {
		t.Fatalf("account scope = %+v, want only the unattributed claim", s)
	}
	if w := adj.View("dealA", nil).Find(FieldOwner, "", "").Winner; w.ID != "a" {
		t.Fatalf("dealA winner = %s", w.ID)
	}
	if w := adj.View("dealB", nil).Find(FieldOwner, "", "").Winner; w.ID != "b" {
		t.Fatalf("dealB winner = %s", w.ID)
	}
}

func TestSameDealStillAdjudicatesByStanding(t *testing.T) {
	crm := scoped(mk("crm", FieldStage, `"Quote"`, CRMExplicit, 1, at(0)), "dealA")
	ai := scoped(mk("ai", FieldStage, `"Negotiation"`, FirstPartyAI, 0.9, at(3)), "dealA")
	adj := Adjudicate([]Claim{crm, ai}, at(10))
	s := adj.View("dealA", nil).Find(FieldStage, "", "")
	if s.Winner.ID != "crm" || len(s.Conflicts) != 1 || s.Conflicts[0].Contradicting.ID != "ai" {
		t.Fatalf("slot = %+v", s)
	}
	if u := adj.Updates["ai"]; u.Status != StatusOutranked {
		t.Fatalf("update = %+v", u)
	}
}

func TestPersonScopedPathsIgnoreTheOpportunity(t *testing.T) {
	m1 := scoped(mk("m1", FieldBuyingGroupMember, `{"title":"CFO"}`, CRMExplicit, 1, at(0)), "dealA")
	m2 := scoped(mk("m2", FieldBuyingGroupMember, `{"title":"CEO"}`, CRMExplicit, 1, at(2)), "dealB")
	m1.SubjectPersonID, m2.SubjectPersonID = "p", "p"
	adj := Adjudicate([]Claim{m1, m2}, at(10))
	s := adj.Find(FieldBuyingGroupMember, "p", "title")
	if s == nil || s.Scope != "" || s.Winner.ID != "m2" || len(adj.Scopes()) != 0 {
		t.Fatalf("a person's title is one fact account-wide: %+v scopes=%v", s, adj.Scopes())
	}
}

func TestViewKeepsOnlyWhatTheCallerAdmits(t *testing.T) {
	m := mk("m", FieldBuyingGroupMember, `{}`, CRMExplicit, 1, at(0))
	m.SubjectPersonID = "p"
	other := mk("o", FieldBuyingGroupMember, `{}`, CRMExplicit, 1, at(0))
	other.SubjectPersonID = "q"
	stage := scoped(mk("s", FieldStage, `"Quote"`, CRMExplicit, 1, at(0)), "dealA")
	adj := Adjudicate([]Claim{m, other, stage}, at(10))
	v := adj.View("dealA", func(s Slot) bool { return s.Subject == "p" })
	if len(v.Slots) != 2 || v.Find(FieldBuyingGroupMember, "p", "member") == nil || v.Find(FieldBuyingGroupMember, "q", "member") != nil {
		t.Fatalf("view = %+v", v.Slots)
	}
	if got := adj.View("dealA", nil); len(got.Slots) != 1 {
		t.Fatalf("a nil filter keeps no person-scoped slot: %+v", got.Slots)
	}
}

func TestScopedAdjudicationIsIndependentOfInputOrder(t *testing.T) {
	cs := []Claim{
		scoped(mk("a", FieldStage, `"Quote"`, CRMExplicit, 1, at(0)), "dealA"),
		scoped(mk("b", FieldStage, `"Closed"`, CRMExplicit, 1, at(2)), "dealB"),
		mk("c", FieldStage, `"Discovery"`, FirstPartyAI, 0.9, at(1)),
	}
	want := Adjudicate(cs, at(10))
	rev := []Claim{cs[2], cs[1], cs[0]}
	got := Adjudicate(rev, at(10))
	if len(got.Slots) != len(want.Slots) {
		t.Fatalf("slots %d vs %d", len(got.Slots), len(want.Slots))
	}
	for i := range want.Slots {
		if got.Slots[i].Scope != want.Slots[i].Scope || got.Slots[i].Winner.ID != want.Slots[i].Winner.ID {
			t.Fatalf("slot %d differs: %+v vs %+v", i, got.Slots[i], want.Slots[i])
		}
	}
}

func TestAmountIsAKnownDealScopedPath(t *testing.T) {
	if !FieldAmount.Valid() || (Claim{FieldPath: FieldAmount}).PersonFact() || FieldAmount.IsList() {
		t.Fatal("amount is a valid scalar deal-scoped path")
	}
}

func TestAMemberRoleBelongsToTheDealNotToThePerson(t *testing.T) {
	// "champion on deal A" must not follow the person into deal B: only presence and title are person facts.
	role := scoped(mk("r", FieldBuyingGroupMember, `{"role":"champion"}`, FirstPartyAI, 0.9, at(0)), "dealA")
	title := scoped(mk("t", FieldBuyingGroupMember, `{"title":"CFO"}`, CRMExplicit, 1, at(0)), "dealA")
	bare := scoped(mk("b", FieldBuyingGroupMember, `{}`, CRMExplicit, 1, at(0)), "dealA")
	for _, c := range []*Claim{&role, &title, &bare} {
		c.SubjectPersonID = "p"
	}
	if role.PersonFact() || !title.PersonFact() || !bare.PersonFact() {
		t.Fatalf("role=%v title=%v bare=%v", role.PersonFact(), title.PersonFact(), bare.PersonFact())
	}
	adj := Adjudicate([]Claim{role, title, bare}, at(10))
	b := adj.View("dealB", func(Slot) bool { return true })
	if b.Find(FieldBuyingGroupMember, "p", "member:champion") != nil || b.Find(FieldBuyingGroupMember, "p", "title") == nil {
		t.Fatalf("deal B view = %+v", b.Slots)
	}
	if adj.View("dealA", nil).Find(FieldBuyingGroupMember, "p", "member:champion") == nil {
		t.Fatal("the role stays on its own deal")
	}
}
