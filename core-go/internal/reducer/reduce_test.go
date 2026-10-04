package reducer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestEmptyAccountIsAllUnknownAndSchemaValid(t *testing.T) {
	st, warns := Reduce(input(day(1), nil, nil))
	mustValidate(t, st)
	if len(warns) != 0 {
		t.Fatalf("warnings = %v", warns)
	}
	for _, name := range FieldNames() {
		f := st.Fields.Field(name)
		if f.Known || f.WinningClaimID != nil || len(f.EvidenceRefs) != 0 {
			t.Errorf("%s should be unknown with no evidence: %+v", name, f)
		}
	}
	if str(st.Fields.Stage) != "unknown" || len(items(st.Fields.Blockers)) != 0 {
		t.Fatalf("stage=%v blockers=%v", st.Fields.Stage.Value, st.Fields.Blockers.Value)
	}
	if st.Version != 3 || !st.AsOf.Equal(day(1)) || st.LastActivityID != nil || st.AccountName != "Acme Corp" {
		t.Fatalf("envelope = %+v", st)
	}
	if !reflect.DeepEqual(st.CoverageGaps, []string{"economic_buyer"}) {
		t.Fatalf("an account with no economic buyer has that gap, got %v", st.CoverageGaps)
	}
}

func TestScalarFieldPointsAtWinningClaimWithEvidence(t *testing.T) {
	crm := claim(claims.FieldStage, `"Commercial review"`, claims.CRMExplicit, 1, day(0))
	ai := claim(claims.FieldStage, `"Negotiation"`, claims.FirstPartyAI, 0.9, day(-2))
	owner := claim(claims.FieldOwner, `"`+dana+`"`, claims.CRMExplicit, 1, day(0))
	st, _ := Reduce(input(day(5), []claims.Claim{ai, crm, owner}, nil))
	mustValidate(t, st)

	f := st.Fields.Stage
	if !f.Known || f.Value != "Commercial review" || *f.WinningClaimID != crm.ID || *f.Standing != "crm_explicit" || *f.Confidence != 1 || !f.AsOf.Equal(day(0)) {
		t.Fatalf("stage = %+v", f)
	}
	if len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0].ActivityID != crm.SourceActivityID || f.EvidenceRefs[0].ClaimID != crm.ID {
		t.Fatalf("evidence = %+v", f.EvidenceRefs)
	}
	if !reflect.DeepEqual(f.CompetingClaimIDs, []string{ai.ID}) {
		t.Fatalf("the outranked claim must stay visible: %v", f.CompetingClaimIDs)
	}
	if len(f.Conflicts) != 0 {
		t.Fatalf("an older AI claim is not a conflict: %+v", f.Conflicts)
	}
	if st.Fields.Owner.Value != dana {
		t.Fatalf("owner = %v", st.Fields.Owner.Value)
	}
}

func TestAIEvidenceCarriesQuoteAndSpeaker(t *testing.T) {
	c := claim(claims.FieldHealth, `"at_risk"`, claims.FirstPartyAI, 0.7, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{c}, nil))
	ev := st.Fields.Health.EvidenceRefs[0]
	if ev.Quote != "verbatim quote" || ev.SpeakerPersonID != priya || !ev.OccurredAt.Equal(day(0)) {
		t.Fatalf("evidence = %+v", ev)
	}
}

func TestNewerLowerStandingContradictionIsRecordedOnTheFieldAndInCompeting(t *testing.T) {
	crm := claim(claims.FieldNextMilestone, `"Platform team integration review"`, claims.CRMExplicit, 1, day(0))
	ai := claim(claims.FieldNextMilestone, `"Review once the residency docs are read"`, claims.FirstPartyAI, 0.8, day(14))
	st, _ := Reduce(input(day(20), []claims.Claim{crm, ai}, nil))
	mustValidate(t, st)

	f := st.Fields.NextMilestone
	if f.Value != "Platform team integration review" || *f.WinningClaimID != crm.ID {
		t.Fatalf("the CRM winner must stand: %+v", f)
	}
	if len(f.Conflicts) != 1 || f.Conflicts[0].ClaimID != ai.ID || f.Conflicts[0].Standing != "first_party_ai" || f.Conflicts[0].Reason == "" {
		t.Fatalf("conflicts = %+v", f.Conflicts)
	}
	for _, c := range f.Conflicts { // contract: every conflicting claim also appears in competing_claim_ids
		found := false
		for _, id := range f.CompetingClaimIDs {
			found = found || id == c.ClaimID
		}
		if !found {
			t.Fatalf("conflict %s missing from competing_claim_ids %v", c.ClaimID, f.CompetingClaimIDs)
		}
	}
}

func TestUnknownWinnerMakesFieldUnknownButKeepsTheClaimVisible(t *testing.T) {
	c := claim(claims.FieldEconomicBuyer, `"unknown"`, claims.FirstPartyAI, 0.9, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{c}, nil))
	mustValidate(t, st)
	f := st.Fields.EconomicBuyer
	if f.Known || f.Value != "unknown" || f.WinningClaimID != nil || len(f.EvidenceRefs) != 0 {
		t.Fatalf("%+v", f)
	}
	if !reflect.DeepEqual(f.CompetingClaimIDs, []string{c.ID}) {
		t.Fatalf("competing = %v", f.CompetingClaimIDs)
	}
}

func TestExplicitNullIsKnownAbsent(t *testing.T) {
	c := claim(claims.FieldNextMeeting, `null`, claims.CRMExplicit, 1, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{c}, nil))
	mustValidate(t, st)
	f := st.Fields.NextMeeting
	if !f.Known || f.Value != nil || *f.WinningClaimID != c.ID {
		t.Fatalf("null next_meeting must be known-absent with a winner: %+v", f)
	}
	raw, _ := json.Marshal(f)
	if !json.Valid(raw) || string(raw[:14]) != `{"value":null,` {
		t.Fatalf("value must serialize as JSON null: %s", raw)
	}
}

func TestObjectValuedFieldDecodesToStructure(t *testing.T) {
	c := claim(claims.FieldNextMeeting, `{"event_id":"e1","start":"2026-09-30T10:00:00Z"}`, claims.CRMExplicit, 1, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{c}, nil))
	mustValidate(t, st)
	m, ok := st.Fields.NextMeeting.Value.(map[string]any)
	if !ok || m["event_id"] != "e1" {
		t.Fatalf("value = %#v", st.Fields.NextMeeting.Value)
	}
}

func TestLowConfidenceClaimIsSuggestedNotWinning(t *testing.T) {
	weak := claim(claims.FieldRelationshipRisk, `"high"`, claims.FirstPartyAI, 0.5, day(0))
	st, _ := Reduce(input(day(1), []claims.Claim{weak}, nil))
	mustValidate(t, st)
	f := st.Fields.RelationshipRisk
	if f.Known || !reflect.DeepEqual(f.SuggestedClaimIDs, []string{weak.ID}) {
		t.Fatalf("%+v", f)
	}
}

func TestListFieldFoldsItemWiseWithStatusAndEvidence(t *testing.T) {
	open := claim(claims.FieldBlockers, `"open: EU budget line needs finance approval"`, claims.FirstPartyAI, 0.9, day(0))
	resolved := claim(claims.FieldBlockers, `"resolved: EU budget line needs finance approval"`, claims.FirstPartyAI, 0.9, day(7))
	sec := claim(claims.FieldBlockers, `{"text":"Security sign-off by the Head of Security","status":"open"}`, claims.FirstPartyAI, 0.9, day(9))
	st, _ := Reduce(input(day(10), []claims.Claim{sec, open, resolved}, nil))
	mustValidate(t, st)

	got := ids(items(st.Fields.Blockers))
	want := []string{"EU budget line needs finance approval=resolved", "Security sign-off by the Head of Security=open"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	f := st.Fields.Blockers
	if !f.Known || f.WinningClaimID != nil || f.Standing != nil {
		t.Fatalf("a list field has no single winner: %+v", f)
	}
	if it := items(f)[0]; it.ClaimID != resolved.ID || len(it.EvidenceRefs) != 1 {
		t.Fatalf("item = %+v", it)
	}
	if len(f.EvidenceRefs) != 2 || !f.AsOf.Equal(day(9)) {
		t.Fatalf("field evidence = %+v as_of=%v", f.EvidenceRefs, f.AsOf)
	}
	if !reflect.DeepEqual(f.CompetingClaimIDs, []string{open.ID}) {
		t.Fatalf("the superseded status claim is retained: %v", f.CompetingClaimIDs)
	}
}

func TestEmptyListIsUnknown(t *testing.T) {
	st, _ := Reduce(input(day(1), nil, nil))
	v, ok := st.Fields.Objections.Value.([]Item)
	if !ok || v == nil || len(v) != 0 || st.Fields.Objections.Known {
		t.Fatalf("%+v", st.Fields.Objections)
	}
}

func TestOpenCommitmentPastDueBecomesOverdue(t *testing.T) {
	due := day(3).Format("2006-01-02T15:04:05Z")
	c := claim(claims.FieldCommitment, `{"text":"Send SOC2 report","status":"open","due_at":"`+due+`"}`, claims.FirstPartyAI, 0.9, day(0))
	fulfilled := claim(claims.FieldCommitment, `{"text":"Send proposal","status":"fulfilled","due_at":"`+due+`"}`, claims.FirstPartyAI, 0.9, day(0))
	act := Activity{ID: id(50), Type: "EmailSent", OccurredAt: day(10)}
	st, _ := Reduce(input(day(11), []claims.Claim{c, fulfilled}, []Activity{act}))
	mustValidate(t, st)
	got := ids(items(st.Fields.CurrentCommitments))
	if !reflect.DeepEqual(got, []string{"Send proposal=fulfilled", "Send SOC2 report=overdue"}) { // same first-seen instant: ordered by key
		t.Fatalf("items = %v", got)
	}
}

func TestUnparseableListClaimIsSkippedWithAWarning(t *testing.T) {
	bad := claim(claims.FieldBlockers, `42`, claims.FirstPartyAI, 0.9, day(0))
	good := claim(claims.FieldBlockers, `"Real blocker"`, claims.FirstPartyAI, 0.9, day(1))
	st, warns := Reduce(input(day(2), []claims.Claim{bad, good}, nil))
	mustValidate(t, st)
	if len(items(st.Fields.Blockers)) != 1 || len(warns) != 1 {
		t.Fatalf("items=%v warnings=%v", items(st.Fields.Blockers), warns)
	}
}

func TestEnvelopeAsOfLastActivityAndOpportunity(t *testing.T) {
	// ADR-0016: the account's opportunity_id is its primary deal, derived from deal evidence (activities or
	// claims that name the deal), not a loader hint.
	acts := []Activity{
		{ID: id(60), Type: "EmailSent", OccurredAt: day(2), OpportunityID: opp},
		{ID: id(61), Type: "EmailReceived", OccurredAt: day(5), OpportunityID: opp},
		{ID: id(62), Type: "SlackMessage", OccurredAt: day(3)},
	}
	st, _ := Reduce(input(day(9), nil, acts))
	if !st.AsOf.Equal(day(5)) || st.LastActivityID == nil || *st.LastActivityID != id(61) || st.OpportunityID == nil || *st.OpportunityID != opp {
		t.Fatalf("%+v", st)
	}
	untied := []Activity{{ID: id(60), Type: "EmailSent", OccurredAt: day(2)}}
	if st, _ := Reduce(input(day(9), nil, untied)); st.OpportunityID != nil || len(st.Opportunities) != 0 {
		t.Fatal("an account with no deal evidence must serialize a null primary and no opportunities")
	}
}

func TestReduceIsDeterministicAndIgnoresInputOrder(t *testing.T) {
	var cs []claims.Claim
	for i := 0; i < 12; i++ {
		f := []claims.FieldPath{claims.FieldStage, claims.FieldBlockers, claims.FieldHealth}[i%3]
		v := fmt.Sprintf(`"v%d"`, i%4)
		cs = append(cs, claim(f, v, []claims.Standing{claims.CRMExplicit, claims.FirstPartyAI}[i%2], 0.8, day(i%5)))
	}
	acts := []Activity{inboundEmail(70, priya, day(1)), inboundEmail(71, marco, day(2))}
	first, _ := Reduce(input(day(20), cs, acts))
	rev := append([]claims.Claim(nil), cs...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	second, _ := Reduce(input(day(20), rev, []Activity{acts[1], acts[0]}))
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("state depends on input order:\n%s\n%s", a, b)
	}
}
