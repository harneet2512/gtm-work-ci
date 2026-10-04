package claims

import (
	"fmt"
	"testing"
)

// Conflict surfacing (ADR-0008) and the standing order (ADR-0009) in adjudication.

func TestNewerLowerStandingContradictionIsSurfacedAsConflict(t *testing.T) {
	crm := mk("crm", FieldNextMilestone, `"Platform team integration review"`, CRMExplicit, 1, at(0))
	ai := mk("ai", FieldNextMilestone, `"Review session once residency docs are read"`, FirstPartyAI, 0.8, at(14))
	a := Adjudicate([]Claim{crm, ai}, at(20))

	s := scalar(t, a, FieldNextMilestone)
	if s.Winner == nil || s.Winner.ID != "crm" {
		t.Fatalf("standing must win, got %+v", s.Winner)
	}
	if len(s.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v", s.Conflicts)
	}
	c := s.Conflicts[0]
	if c.Contradicting.ID != "ai" || c.Winner.ID != "crm" || c.Contradicting.Standing != FirstPartyAI || c.Winner.Standing != CRMExplicit ||
		c.Reason == "" || len(c.Reason) > 500 || c.Contradicting.EvidenceQuote == "" {
		t.Fatalf("conflict = %+v", c)
	}
	if len(a.Conflicts) != 1 || a.Conflicts[0].Field != FieldNextMilestone {
		t.Fatalf("conflicts = %+v", a.Conflicts)
	}
	if a.Updates["ai"].Status != StatusOutranked {
		t.Fatalf("the contradicting claim is retained as outranked, got %+v", a.Updates["ai"])
	}
}

func TestNoConflictWhenAgreeingOlderUnknownNullWeakOrWinnerIsAI(t *testing.T) {
	crm := mk("crm", FieldStage, `"Technical evaluation"`, CRMExplicit, 1, at(0))
	tests := []struct {
		name  string
		other Claim
		win   Claim
	}{
		{"newer AI agrees", mk("x", FieldStage, `"technical  evaluation"`, FirstPartyAI, 0.9, at(5)), crm},
		{"newer AI says unknown", mk("x", FieldStage, `"unknown"`, FirstPartyAI, 0.9, at(5)), crm},
		{"newer AI says null", mk("x", FieldStage, `null`, FirstPartyAI, 0.9, at(5)), crm},
		{"newer AI is below the confidence floor", mk("x", FieldStage, `"Negotiation"`, FirstPartyAI, 0.4, at(5)), crm},
		{"older AI disagrees", mk("x", FieldStage, `"Negotiation"`, FirstPartyAI, 0.9, at(-5)), crm},
		{"winner is itself AI", mk("x", FieldStage, `"Negotiation"`, FirstPartyAI, 0.9, at(-5)), mk("w", FieldStage, `"Discovery"`, FirstPartyAI, 0.9, at(0))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Adjudicate([]Claim{tt.win, tt.other}, at(20))
			if len(a.Conflicts) != 0 || len(scalar(t, a, FieldStage).Conflicts) != 0 {
				t.Fatalf("unexpected conflict: %+v", a.Conflicts)
			}
		})
	}
}

func TestHumanApprovedWinnerConflictsWithNewerCRM(t *testing.T) {
	human := mk("h", FieldStage, `"Negotiation"`, HumanApproved, 1, at(0))
	crm := mk("crm", FieldStage, `"Discovery"`, CRMExplicit, 1, at(2))
	a := Adjudicate([]Claim{human, crm}, at(5))
	if w := scalar(t, a, FieldStage).Winner; w.ID != "h" {
		t.Fatalf("winner = %s, want the human-approved claim", w.ID)
	}
	if len(a.Conflicts) != 1 || a.Conflicts[0].Contradicting.ID != "crm" {
		t.Fatalf("conflicts = %+v", a.Conflicts)
	}
}

func TestConfirmationByHumanClearsTheConflictAndWinsOverCRM(t *testing.T) {
	// ADR-0008: the rep confirms through the API, which writes a human_approved claim; it outranks
	// CRM and, being the newest claim, leaves nothing newer to contradict it.
	crm := mk("crm", FieldNextMilestone, `"Platform team integration review"`, CRMExplicit, 1, at(0))
	ai := mk("ai", FieldNextMilestone, `"Review once residency docs are read"`, FirstPartyAI, 0.8, at(14))
	before := Adjudicate([]Claim{crm, ai}, at(20))
	if len(before.Conflicts) != 1 {
		t.Fatalf("setup: %+v", before.Conflicts)
	}
	human := mk("human", FieldNextMilestone, `"Review once residency docs are read"`, HumanApproved, 1, at(21))
	after := Adjudicate([]Claim{crm, ai, human}, at(22))
	s := scalar(t, after, FieldNextMilestone)
	if s.Winner == nil || s.Winner.ID != "human" || len(s.Conflicts) != 0 {
		t.Fatalf("winner = %+v conflicts = %+v", s.Winner, s.Conflicts)
	}
	if after.Updates["crm"].Status != StatusOutranked || after.Updates["ai"].Status != StatusOutranked {
		t.Fatalf("updates = %+v", after.Updates)
	}
}

func TestFirstPartyRecordRanksBetweenCRMAndAI(t *testing.T) {
	// ADR-0009: human_approved > crm_explicit > first_party_record > first_party_ai > third_party.
	record := mk("rec", FieldNextMeeting, `{"event_id":"e1"}`, FirstPartyRecord, 1, at(0))
	ai := mk("ai", FieldNextMeeting, `"call on Friday"`, FirstPartyAI, 0.9, at(5))
	crm := mk("crm", FieldNextMeeting, `"CRM says Monday"`, CRMExplicit, 1, at(-5))

	a := Adjudicate([]Claim{ai, record}, at(10))
	s := scalar(t, a, FieldNextMeeting)
	if s.Winner.ID != "rec" || a.Updates["ai"].Status != StatusOutranked {
		t.Fatalf("a deterministic record outranks a newer AI claim: winner %s, updates %+v", s.Winner.ID, a.Updates)
	}
	if len(s.Conflicts) != 1 || s.Conflicts[0].Contradicting.ID != "ai" {
		t.Fatalf("the newer AI contradiction is surfaced: %+v", s.Conflicts)
	}

	a = Adjudicate([]Claim{ai, record, crm}, at(10))
	s = scalar(t, a, FieldNextMeeting)
	if s.Winner.ID != "crm" || a.Updates["rec"].Status != StatusOutranked || a.Updates["ai"].Status != StatusOutranked {
		t.Fatalf("CRM outranks the record: winner %s, updates %+v", s.Winner.ID, a.Updates)
	}
	if len(s.Conflicts) != 2 || s.Conflicts[0].Contradicting.ID != "ai" || s.Conflicts[1].Contradicting.ID != "rec" {
		t.Fatalf("both newer lower-standing claims conflict with the older CRM value, newest first: %+v", s.Conflicts)
	}
	if !(HumanApproved.Rank() > CRMExplicit.Rank() && CRMExplicit.Rank() > FirstPartyRecord.Rank() &&
		FirstPartyRecord.Rank() > FirstPartyAI.Rank() && FirstPartyAI.Rank() > ThirdParty.Rank() && ThirdParty.Rank() > Standing("bogus").Rank()) {
		t.Fatal("rank table order")
	}
}

func TestEveryNewerContradictingClaimIsListedNewestFirstIncludingThirdParty(t *testing.T) {
	crm := mk("crm", FieldNextMilestone, `"A"`, CRMExplicit, 1, at(0))
	a1 := mk("a1", FieldNextMilestone, `"B"`, FirstPartyAI, 0.9, at(2))
	a2 := mk("a2", FieldNextMilestone, `"C"`, FirstPartyAI, 0.9, at(4))
	tp := mk("tp", FieldNextMilestone, `"D"`, ThirdParty, 1, at(3))
	older := mk("older", FieldNextMilestone, `"E"`, FirstPartyAI, 0.9, at(-3))
	a := Adjudicate([]Claim{crm, a1, a2, tp, older}, at(10))
	s := scalar(t, a, FieldNextMilestone)
	var got []string
	for _, c := range s.Conflicts {
		got = append(got, c.Contradicting.ID)
	}
	if fmt.Sprint(got) != "[a2 tp a1]" {
		t.Fatalf("conflicts = %v, want [a2 tp a1] (every newer lower-standing contradiction, newest first)", got)
	}
}
