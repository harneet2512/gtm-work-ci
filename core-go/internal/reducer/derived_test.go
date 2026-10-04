package reducer

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestLastCustomerInteractionIsDerivedFromInboundCustomerActivities(t *testing.T) {
	email := inboundEmail(80, priya, day(2))
	call := Activity{ID: id(81), Type: "TranscriptReady", OccurredAt: day(4), Participants: []Participant{
		{PersonID: dana, Role: "speaker", RawIdentity: "call:c1:speaker_01"}, {PersonID: tom, Role: "speaker", RawIdentity: "call:c1:speaker_02"}}}
	outbound := Activity{ID: id(82), Type: "EmailSent", OccurredAt: day(6), Participants: []Participant{
		{PersonID: dana, Role: "from", RawIdentity: "dana@ghostvendor.com"}, {PersonID: priya, Role: "to", RawIdentity: "priya.shah@acme.com"}}}
	slack := Activity{ID: id(83), Type: "SlackMessage", OccurredAt: day(7), Participants: []Participant{{PersonID: dana, Role: "actor", RawIdentity: "slack:U1"}}}
	accepted := Activity{ID: id(84), Type: "MeetingAccepted", OccurredAt: day(8), Participants: []Participant{{PersonID: priya, Role: "actor", RawIdentity: "priya.shah@acme.com"}}}
	st, _ := Reduce(input(day(10), nil, []Activity{outbound, slack, accepted, email, call}))
	mustValidate(t, st)

	f := st.Fields.LastCustomerInteraction
	if !f.Known || !f.Derived || f.WinningClaimID != nil || f.Standing != nil || f.Value != day(4).Format("2006-01-02T15:04:05Z") {
		t.Fatalf("%+v", f)
	}
	if len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0].ActivityID != call.ID || f.EvidenceRefs[0].ClaimID != "" || !f.AsOf.Equal(day(4)) {
		t.Fatalf("evidence = %+v", f.EvidenceRefs)
	}
}

func TestCustomerIsDecidedByPersonKindOrByForeignEmailDomain(t *testing.T) {
	byDomain := Activity{ID: id(90), Type: "EmailReceived", OccurredAt: day(2), Participants: []Participant{{Role: "from", RawIdentity: "someone@prospect.io"}}}
	internal := Activity{ID: id(91), Type: "EmailReceived", OccurredAt: day(5), Participants: []Participant{{Role: "from", RawIdentity: "rachel@ghostvendor.com"}}}
	unresolvedCall := Activity{ID: id(92), Type: "TranscriptReady", OccurredAt: day(6), Participants: []Participant{{Role: "speaker", RawIdentity: "call:c1:speaker_02"}}}
	st, _ := Reduce(input(day(10), nil, []Activity{byDomain, internal, unresolvedCall}))
	f := st.Fields.LastCustomerInteraction
	if !f.Known || f.EvidenceRefs[0].ActivityID != byDomain.ID {
		t.Fatalf("an unresolved sender on a foreign domain is a customer, our own domain and unlinked call speakers are not: %+v", f)
	}
}

func TestCompletedMeetingWithCustomerAttendeeCounts(t *testing.T) {
	m := Activity{ID: id(95), Type: "MeetingCompleted", OccurredAt: day(3), Participants: []Participant{{PersonID: priya, Role: "attendee"}, {PersonID: dana, Role: "attendee"}}}
	internalOnly := Activity{ID: id(96), Type: "MeetingCompleted", OccurredAt: day(5), Participants: []Participant{{PersonID: dana, Role: "attendee"}}}
	st, _ := Reduce(input(day(10), nil, []Activity{m, internalOnly}))
	if f := st.Fields.LastCustomerInteraction; !f.Known || f.EvidenceRefs[0].ActivityID != m.ID {
		t.Fatalf("%+v", f)
	}
}

func TestLastCustomerInteractionUnknownWithoutCustomerActivity(t *testing.T) {
	st, _ := Reduce(input(day(3), nil, []Activity{{ID: id(97), Type: "EmailSent", OccurredAt: day(1)}}))
	mustValidate(t, st)
	f := st.Fields.LastCustomerInteraction
	if f.Known || f.Value != "unknown" || !f.Derived || f.WinningClaimID != nil {
		t.Fatalf("%+v", f)
	}
}

func TestLastMeaningfulChangeIgnoresReaffirmations(t *testing.T) {
	stage := claim(claims.FieldStage, `"Technical evaluation"`, claims.CRMExplicit, 1, day(0))
	first := claim(claims.FieldBlockers, `"Security sign-off"`, claims.FirstPartyAI, 0.9, day(3))
	again := claim(claims.FieldBlockers, `"Security sign-off"`, claims.FirstPartyAI, 0.9, day(9)) // reaffirms, changes nothing
	acts := []Activity{
		{ID: stage.SourceActivityID, Type: "OpportunityStageChanged", OccurredAt: stage.OccurredAt},
		{ID: first.SourceActivityID, Type: "EmailReceived", OccurredAt: first.OccurredAt},
		{ID: again.SourceActivityID, Type: "EmailReceived", OccurredAt: again.OccurredAt},
	}
	st, _ := Reduce(input(day(12), []claims.Claim{stage, first, again}, acts))
	mustValidate(t, st)
	f := st.Fields.LastMeaningfulChange
	if !f.Known || !f.Derived || f.WinningClaimID != nil || len(f.EvidenceRefs) != 1 || f.EvidenceRefs[0].ActivityID != first.SourceActivityID {
		t.Fatalf("the change is when the blocker first appeared, not its reaffirmation: %+v", f)
	}
	if s := str(f); s == "" || s == "unknown" {
		t.Fatalf("value = %q", s)
	}
}

func TestLastMeaningfulChangeUnknownWhenNothingConcreteIsKnown(t *testing.T) {
	null := claim(claims.FieldNextMeeting, `null`, claims.CRMExplicit, 1, day(0))
	st, _ := Reduce(input(day(2), []claims.Claim{null}, nil))
	mustValidate(t, st)
	if f := st.Fields.LastMeaningfulChange; f.Known || f.Value != "unknown" || !f.Derived {
		t.Fatalf("an absence is not a change: %+v", f)
	}
}

func TestLastMeaningfulChangeNamesAllFieldsEstablishedAtTheSameInstant(t *testing.T) {
	a := claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(0))
	b := a
	b.ID, b.FieldPath, b.Value = id(2000), claims.FieldMotion, []byte(`"expansion"`)
	st, _ := Reduce(input(day(2), []claims.Claim{a, b}, []Activity{{ID: a.SourceActivityID, Type: "OpportunityStageChanged", OccurredAt: day(0)}}))
	if s := str(st.Fields.LastMeaningfulChange); s != "motion, stage updated (OpportunityStageChanged, 2026-09-01)" {
		t.Fatalf("value = %q", s)
	}
}
