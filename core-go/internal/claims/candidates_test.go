package claims

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

const emailBody = "Hi Dana - Sam Okafor leads information security here and will own the evaluation. Please work directly with Sam on this; I'm stepping back from the day-to-day.\nElena"

func emailActivity() ActivityInput {
	return ActivityInput{
		ID: actIDOne, AccountID: acctID, OpportunityID: oppID, Type: "EmailReceived", SourceSystem: "email",
		OccurredAt: time.Date(2026, 9, 21, 9, 30, 0, 0, time.UTC), Body: emailBody,
		Participants: []Participant{
			{RawIdentity: "elena.vasquez@northstar.health", Role: "from", DisplayName: "Elena Vasquez", PersonID: "elena-id"},
			{RawIdentity: "dana@vendor.example", Role: "to", DisplayName: "Dana Kim", PersonID: danaID},
			{RawIdentity: "sam.okafor@northstar.health", Role: "cc", DisplayName: "Sam Okafor", PersonID: "sam-id"},
			{RawIdentity: "ghost@unlinked.example", Role: "cc", DisplayName: "Unlinked"},
		},
	}
}

func cand(field FieldPath, value, quote string, conf float64) Candidate {
	return Candidate{FieldPath: field, Value: json.RawMessage(value), Confidence: conf, EvidenceQuote: quote}
}

func convert(t *testing.T, cs ...Candidate) Conversion {
	t.Helper()
	act := emailActivity()
	return FromCandidates(act, cs, "deepseek-v4-flash", "extract-v1", NewIdentityIndex(act.Participants, nil).Resolve)
}

func TestFromCandidatesBuildsFirstPartyAIClaimWithEvidenceAndSpeaker(t *testing.T) {
	c := cand(FieldChampionStatus, `"delegated"`, "I'm stepping back from the day-to-day", 0.9)
	c.SpeakerIdentity = "elena.vasquez@northstar.health"
	conv := convert(t, c)
	if len(conv.Dropped) != 0 || len(conv.Claims) != 1 {
		t.Fatalf("%+v", conv)
	}
	got := conv.Claims[0]
	if got.Standing != FirstPartyAI || got.Extractor != "llm:deepseek-v4-flash@extract-v1" || got.SpeakerPersonID != "elena-id" ||
		got.EvidenceQuote != "I'm stepping back from the day-to-day" || got.Confidence != 0.9 || got.SourceActivityID != actIDOne ||
		got.AccountID != acctID || got.OpportunityID != oppID || !got.OccurredAt.Equal(emailActivity().OccurredAt) || got.Status != StatusActive || str(got) != "delegated" {
		t.Fatalf("claim = %+v", got)
	}
}

func TestFromCandidatesEnrichmentActivitiesGetThirdPartyStanding(t *testing.T) {
	act := emailActivity()
	act.SourceSystem = "enrichment"
	conv := FromCandidates(act, []Candidate{cand(FieldProductUseCase, `"x"`, "Sam Okafor leads", 0.9)}, "m", "v", NewIdentityIndex(act.Participants, nil).Resolve)
	if len(conv.Claims) != 1 || conv.Claims[0].Standing != ThirdParty {
		t.Fatalf("%+v", conv)
	}
}

func TestFromCandidatesDropsInvalidCandidatesWithReasons(t *testing.T) {
	tests := []struct {
		name string
		c    Candidate
	}{
		{"quote is not a verbatim substring", cand(FieldHealth, `"at_risk"`, "I am considering stepping back", 0.9)},
		{"quote differs in case", cand(FieldHealth, `"at_risk"`, "SAM OKAFOR LEADS information security", 0.9)},
		{"empty quote", cand(FieldHealth, `"at_risk"`, "", 0.9)},
		{"quote over 2000 characters", cand(FieldHealth, `"at_risk"`, strings.Repeat("a", 2001), 0.9)},
		{"confidence above 1", cand(FieldHealth, `"at_risk"`, "Sam Okafor leads", 1.2)},
		{"confidence below 0", cand(FieldHealth, `"at_risk"`, "Sam Okafor leads", -0.1)},
		{"confidence NaN", cand(FieldHealth, `"at_risk"`, "Sam Okafor leads", math.NaN())},
		{"unknown field path", cand("not_a_field", `"x"`, "Sam Okafor leads", 0.9)},
		{"amount is not extractable from text (ADR-0016)", cand("amount", `120000`, "Sam Okafor leads", 0.9)},
		{"empty string value", cand(FieldHealth, `"   "`, "Sam Okafor leads", 0.9)},
		{"missing value", cand(FieldHealth, ``, "Sam Okafor leads", 0.9)},
		{"invalid json value", cand(FieldHealth, `{bad`, "Sam Okafor leads", 0.9)},
		{"invalid role", Candidate{FieldPath: FieldStakeholderRole, Value: json.RawMessage(`"x"`), Role: "wizard", Confidence: 0.9, EvidenceQuote: "Sam Okafor leads", SubjectIdentity: "sam.okafor@northstar.health"}},
		{"list item with no text", cand(FieldBlockers, `"open:"`, "Sam Okafor leads", 0.9)},
		{"stakeholder role without a resolvable subject", cand(FieldStakeholderRole, `"security"`, "Sam Okafor leads", 0.9)},
		{"champion that cannot be resolved", cand(FieldChampion, `"Nobody Atall"`, "Sam Okafor leads", 0.9)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conv := convert(t, tt.c)
			if len(conv.Claims) != 0 || len(conv.Dropped) != 1 || conv.Dropped[0].Reason == "" || conv.Dropped[0].Index != 0 {
				t.Fatalf("%+v", conv)
			}
		})
	}
}

func TestFromCandidatesResolvesPersonFieldsThroughParticipants(t *testing.T) {
	champ := cand(FieldChampion, `"Elena Vasquez"`, "Elena", 0.9)
	eb := cand(FieldEconomicBuyer, `"unknown"`, "Elena", 0.8)
	role := Candidate{FieldPath: FieldStakeholderRole, Value: json.RawMessage(`"security"`), Role: "security", Confidence: 0.9, EvidenceQuote: "Sam Okafor leads information security", SubjectIdentity: "sam.okafor@northstar.health"}
	member := Candidate{FieldPath: FieldBuyingGroupMember, Value: json.RawMessage(`"Sam Okafor"`), Role: "technical_evaluator", Confidence: 0.9, EvidenceQuote: "Sam Okafor leads"}
	conv := convert(t, champ, eb, role, member)
	if len(conv.Dropped) != 0 || len(conv.Claims) != 4 {
		t.Fatalf("%+v", conv)
	}
	if c := conv.Claims[0]; str(c) != "elena-id" || c.SubjectPersonID != "elena-id" {
		t.Errorf("champion = %+v", c)
	}
	if c := conv.Claims[1]; !IsUnknown(c.Value) || c.SubjectPersonID != "" {
		t.Errorf("economic buyer unknown = %+v", c)
	}
	if c := conv.Claims[2]; str(c) != "security" || c.SubjectPersonID != "sam-id" {
		t.Errorf("role = %+v", c)
	}
	m := conv.Claims[3]
	if m.SubjectPersonID != "sam-id" || ParseMember(m.Value).Role != "technical_evaluator" {
		t.Errorf("member = %+v", m)
	}
}

func TestFromCandidatesDelegationAcceptsObjectAndSubjectPlusStringForms(t *testing.T) {
	obj := cand(FieldDelegation, `{"from":"elena.vasquez@northstar.health","to":"Sam Okafor","scope":"evaluation, including the BAA"}`, "will own the evaluation", 0.9)
	str2 := cand(FieldDelegation, `"sam.okafor@northstar.health"`, "will own the evaluation", 0.9)
	str2.SubjectIdentity = "Elena Vasquez"
	conv := convert(t, obj, str2)
	if len(conv.Dropped) != 0 || len(conv.Claims) != 2 {
		t.Fatalf("%+v", conv)
	}
	for _, c := range conv.Claims {
		d, err := ParseDelegation(c.Value)
		if err != nil || d.FromPersonID != "elena-id" || d.ToPersonID != "sam-id" || c.SubjectPersonID != "elena-id" {
			t.Fatalf("claim %+v: %+v %v", c, d, err)
		}
	}
	if ParseDelegationMust(conv.Claims[0]).Scope != "evaluation, including the BAA" {
		t.Error("scope lost")
	}
	bad := convert(t, cand(FieldDelegation, `{"from":"elena.vasquez@northstar.health","to":"Stranger"}`, "will own the evaluation", 0.9))
	if len(bad.Claims) != 0 || len(bad.Dropped) != 1 {
		t.Fatalf("an unresolvable delegate must drop the claim: %+v", bad)
	}
}

func ParseDelegationMust(c Claim) Delegation {
	d, _ := ParseDelegation(c.Value)
	return d
}

func TestFromCandidatesCanonicalizesListItemsAndAppliesDueDate(t *testing.T) {
	due := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	withDue := cand(FieldCommitment, `"open: Send the mutual NDA"`, "Please work directly with Sam", 0.9)
	withDue.DueAt = &due
	a := cand(FieldBlockers, `"resolved: Security sign-off"`, "Sam Okafor leads", 0.9)
	b := cand(FieldBlockers, `{"text":"Security sign-off","status":"resolved"}`, "Sam Okafor leads", 0.9)
	conv := convert(t, withDue, a, b)
	if len(conv.Claims) != 3 {
		t.Fatalf("%+v", conv)
	}
	item, err := ParseListItem(conv.Claims[0].Value)
	if err != nil || item.Status != "open" || item.DueAt == nil || !item.DueAt.Equal(due) {
		t.Fatalf("due item = %+v %v", item, err)
	}
	if CanonicalValue(conv.Claims[1].Value) != CanonicalValue(conv.Claims[2].Value) {
		t.Fatalf("string and object forms must canonicalize identically (dedupe): %s vs %s", conv.Claims[1].Value, conv.Claims[2].Value)
	}
}

func TestFromCandidatesKeepsNullAndUnknownValues(t *testing.T) {
	nm := cand(FieldNextMeeting, `null`, "stepping back from the day-to-day", 0.8)
	conv := convert(t, nm)
	if len(conv.Claims) != 1 || !IsNull(conv.Claims[0].Value) {
		t.Fatalf("an explicit null (known absent) must survive: %+v", conv)
	}
}

func TestFromCandidatesDroppedIndexPointsAtTheInputPosition(t *testing.T) {
	good := cand(FieldHealth, `"at_risk"`, "Sam Okafor leads", 0.9)
	bad := cand(FieldHealth, `"x"`, "not in the text", 0.9)
	conv := convert(t, good, bad, good)
	if len(conv.Claims) != 2 || len(conv.Dropped) != 1 || conv.Dropped[0].Index != 1 {
		t.Fatalf("%+v", conv)
	}
}

func TestIdentityIndexResolvesEmailLabelDisplayNameAndRejectsAmbiguity(t *testing.T) {
	parts := []Participant{
		{RawIdentity: "call:call-1:speaker_02", Role: "speaker", PersonID: "priya-id", DisplayName: "Priya Shah"},
		{RawIdentity: "call:call-1:speaker_03", Role: "speaker", PersonID: "tom-id"},
		{RawIdentity: "priya.shah@acme.com", Role: "from", PersonID: "priya-id", DisplayName: "Priya Shah"},
		{RawIdentity: "twin1@acme.com", Role: "cc", PersonID: "t1", DisplayName: "Alex Doe"},
		{RawIdentity: "twin2@acme.com", Role: "cc", PersonID: "t2", DisplayName: "Alex Doe"},
		{RawIdentity: "ghost@x.com", Role: "cc"},
	}
	ix := NewIdentityIndex(parts, []KnownPerson{{PersonID: "marco-id", RawIdentity: "marco.ruiz@acme.com", DisplayName: "Marco Ruiz"}})
	for in, want := range map[string]string{
		"call:call-1:speaker_02": "priya-id", "CALL:call-1:SPEAKER_03": "tom-id", "speaker_03": "tom-id",
		"Priya.Shah@acme.com": "priya-id", " Priya Shah ": "priya-id", "twin1@acme.com": "t1",
		"marco.ruiz@acme.com": "marco-id", "Marco Ruiz": "marco-id",
	} {
		if got, ok := ix.Resolve(in); !ok || got != want {
			t.Errorf("Resolve(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"Alex Doe", "ghost@x.com", "", "nobody@x.com"} {
		if got, ok := ix.Resolve(in); ok {
			t.Errorf("Resolve(%q) = %q, want unresolved (ambiguous or unlinked)", in, got)
		}
	}
}
