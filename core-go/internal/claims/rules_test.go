package claims

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type mapDirectory map[string]string

func (m mapDirectory) PersonIDByEmail(_ context.Context, email string) (string, bool, error) {
	id, ok := m[email]
	return id, ok, nil
}

type failingDirectory struct{}

func (failingDirectory) PersonIDByEmail(context.Context, string) (string, bool, error) {
	return "", false, errors.New("directory down")
}

const (
	oppID    = "0c0f0000-0000-4000-8000-000000000004"
	danaID   = "0b0e0000-0000-4000-8000-000000000001"
	marcoID  = "0b0e0000-0000-4000-8000-000000000018"
	acctID   = "0a0c0000-0000-4000-8000-000000000001"
	actIDOne = "0ac70000-0000-4000-8000-000000000101"
)

var testDir = mapDirectory{"dana@vendor.example": danaID, "priya.shah@acme.com": "priya-id"}

func crmActivity(actType, payload string, parts ...Participant) ActivityInput {
	return ActivityInput{ID: actIDOne, AccountID: acctID, OpportunityID: oppID, Type: actType, SourceSystem: "crm",
		OccurredAt: time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC), Payload: json.RawMessage(payload), Participants: parts}
}

func claimFor(t *testing.T, res RuleResult, f FieldPath) Claim {
	t.Helper()
	for _, c := range res.Claims {
		if c.FieldPath == f {
			return c
		}
	}
	t.Fatalf("no %s claim in %+v (skipped %+v)", f, res.Claims, res.Skipped)
	return Claim{}
}

func str(c Claim) string {
	s, _ := StringValue(c.Value)
	return s
}

func TestCRMOpportunityCreatedYieldsStageMotionOwner(t *testing.T) {
	act := crmActivity("OpportunityStageChanged", `{"kind":"crm_change","object_type":"Opportunity","record_id":"opp:AC-4-EXP","created":true,
		"fields":{"Name":{"new":"Acme - EU rollout"},"Type":{"new":"Expansion"},"StageName":{"new":"Discovery"},"Amount":{"new":156000},
		"CloseDate":{"new":"2026-10-30"},"OwnerEmail":{"new":"dana@vendor.example"},"AccountId":{"new":"account:AC-4"}}}`)
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
	if err != nil {
		t.Fatal(err)
	}
	stage, motion, owner := claimFor(t, res, FieldStage), claimFor(t, res, FieldMotion), claimFor(t, res, FieldOwner)
	if str(stage) != "Discovery" || str(motion) != "expansion" || str(owner) != danaID {
		t.Fatalf("stage=%s motion=%s owner=%s", str(stage), str(motion), str(owner))
	}
	for _, c := range res.Claims {
		if c.Standing != CRMExplicit || c.Confidence != 1 || c.Extractor != RuleCRM || c.SourceActivityID != actIDOne ||
			c.AccountID != acctID || c.OpportunityID != oppID || !c.OccurredAt.Equal(act.OccurredAt) || c.Status != StatusActive {
			t.Errorf("bad claim envelope: %+v", c)
		}
	}
	if amount := claimFor(t, res, FieldAmount); string(amount.Value) != "156000" {
		t.Fatalf("amount = %s, want the deal's number (ADR-0016)", amount.Value)
	}
	if len(res.Claims) != 4 {
		t.Fatalf("claims = %d, want stage+motion+owner+amount only (CloseDate and Name fold into nothing)", len(res.Claims))
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason == "" {
		t.Fatalf("CloseDate must be recognised and reported as folding into nothing, skipped = %+v", res.Skipped)
	}
}

func TestCRMOpportunityAmountMustBeANumber(t *testing.T) {
	for _, raw := range []string{`"a lot"`, `null`} {
		res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(),
			crmActivity("CRMFieldChanged", `{"object_type":"Opportunity","fields":{"Amount":{"new":`+raw+`}}}`))
		if err != nil || len(res.Claims) != 0 {
			t.Fatalf("%s: claims=%d err=%v, want none", raw, len(res.Claims), err)
		}
	}
}

func TestCRMOpportunityFieldChanges(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		field   FieldPath
		want    string
	}{
		{"stage change", `{"object_type":"Opportunity","fields":{"StageName":{"old":"Discovery","new":"Commercial review"}}}`, FieldStage, "Commercial review"},
		{"next step", `{"object_type":"Opportunity","fields":{"NextStep":{"old":null,"new":"Platform team integration review"}}}`, FieldNextMilestone, "Platform team integration review"},
		{"renewal motion", `{"object_type":"Opportunity","fields":{"Type":{"new":"Renewal"}}}`, FieldMotion, "renewal"},
		{"new business motion", `{"object_type":"Opportunity","fields":{"Type":{"new":"New Business"}}}`, FieldMotion, "new_business"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), crmActivity("CRMFieldChanged", tt.payload))
			if err != nil {
				t.Fatal(err)
			}
			if got := str(claimFor(t, res, tt.field)); got != tt.want {
				t.Fatalf("value = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCRMOpportunityOnlyCloseDateYieldsNoClaim(t *testing.T) {
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(),
		crmActivity("CRMFieldChanged", `{"object_type":"Opportunity","fields":{"CloseDate":{"old":"2026-11-30","new":"2026-10-23"}}}`))
	if err != nil || len(res.Claims) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("claims=%d skipped=%+v err=%v", len(res.Claims), res.Skipped, err)
	}
}

func TestCRMSkipsUnresolvableOwnerUnknownTypeAndEmptyValues(t *testing.T) {
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), crmActivity("CRMFieldChanged",
		`{"object_type":"Opportunity","fields":{"OwnerEmail":{"new":"stranger@vendor.example"},"Type":{"new":"Upsell"},"StageName":{"new":"  "},"NextStep":{"new":null}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Claims) != 0 {
		t.Fatalf("claims = %+v", res.Claims)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %+v, want the unresolved owner and the unmapped type reported", res.Skipped)
	}
}

func TestCRMDirectoryFailureIsAnError(t *testing.T) {
	_, err := (RuleExtractor{Dir: failingDirectory{}}).Extract(context.Background(),
		crmActivity("CRMFieldChanged", `{"object_type":"Opportunity","fields":{"OwnerEmail":{"new":"dana@vendor.example"}}}`))
	if err == nil {
		t.Fatal("a directory failure must surface, not silently drop the owner")
	}
}

func TestCRMContactCreatedYieldsMemberWithTitleAndEconomicBuyerRole(t *testing.T) {
	act := crmActivity("ContactAdded", `{"object_type":"Contact","record_id":"contact:907","created":true,
		"fields":{"FirstName":{"new":"Owen"},"Title":{"new":"Chief Financial Officer"},"Role__c":{"new":"Economic buyer"}}}`,
		Participant{RawIdentity: "crm:contact:907", Role: "mentioned", PersonID: "owen-id"})
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
	if err != nil {
		t.Fatal(err)
	}
	member := claimFor(t, res, FieldBuyingGroupMember)
	if member.SubjectPersonID != "owen-id" || ParseMember(member.Value).Title != "Chief Financial Officer" {
		t.Fatalf("member = %+v", member)
	}
	eb := claimFor(t, res, FieldEconomicBuyer)
	if str(eb) != "owen-id" || eb.SubjectPersonID != "owen-id" {
		t.Fatalf("economic buyer = %+v", eb)
	}
}

func TestCRMContactRoleMapping(t *testing.T) {
	tests := []struct {
		role  string
		field FieldPath
		want  string
	}{
		{"Security approver", FieldStakeholderRole, "security"},
		{"Technical approver", FieldStakeholderRole, "technical_evaluator"},
		{"Evaluation owner", FieldStakeholderRole, "technical_evaluator"},
		{"Champion", FieldChampion, "marco-id"},
		{"economic buyer", FieldEconomicBuyer, "marco-id"},
		{"Legal", FieldStakeholderRole, "legal"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			act := crmActivity("StakeholderRoleChanged", `{"object_type":"Contact","record_id":"contact:842","created":false,"fields":{"Role__c":{"old":null,"new":"`+tt.role+`"}}}`,
				Participant{RawIdentity: "crm:contact:842", Role: "mentioned", PersonID: "marco-id"})
			res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
			if err != nil {
				t.Fatal(err)
			}
			c := claimFor(t, res, tt.field)
			if str(c) != tt.want || c.SubjectPersonID != "marco-id" {
				t.Fatalf("claim = %+v", c)
			}
		})
	}
}

func TestCRMContactSkipsUnresolvedPersonAndUnmappedRole(t *testing.T) {
	unresolved := crmActivity("StakeholderRoleChanged", `{"object_type":"Contact","fields":{"Role__c":{"new":"Security approver"}}}`,
		Participant{RawIdentity: "crm:contact:842", Role: "mentioned"})
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), unresolved)
	if err != nil || len(res.Claims) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("unresolved contact: claims=%d skipped=%+v err=%v", len(res.Claims), res.Skipped, err)
	}
	odd := crmActivity("StakeholderRoleChanged", `{"object_type":"Contact","fields":{"Role__c":{"new":"Vibes officer"}}}`,
		Participant{RawIdentity: "crm:contact:842", Role: "mentioned", PersonID: "marco-id"})
	res, err = (RuleExtractor{Dir: testDir}).Extract(context.Background(), odd)
	if err != nil || len(res.Claims) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("unmapped role: claims=%d skipped=%+v err=%v", len(res.Claims), res.Skipped, err)
	}
}

func TestCRMTitleChangeOnExistingContact(t *testing.T) {
	act := crmActivity("StakeholderRoleChanged", `{"object_type":"Contact","created":false,"fields":{"Title":{"old":"Manager","new":"Head of Platform Engineering"}}}`,
		Participant{RawIdentity: "crm:contact:1", Role: "mentioned", PersonID: "ravi-id"})
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
	if err != nil {
		t.Fatal(err)
	}
	if m := claimFor(t, res, FieldBuyingGroupMember); ParseMember(m.Value).Title != "Head of Platform Engineering" || m.SubjectPersonID != "ravi-id" {
		t.Fatalf("member = %+v", m)
	}
}

func TestCRMNotesAccountsAndNonCRMPayloadsYieldNothing(t *testing.T) {
	for _, payload := range []string{
		`{"object_type":"Note","created":true,"note_body":"text"}`,
		`{"object_type":"Account","created":true,"fields":{"Name":{"new":"Acme"}}}`,
	} {
		res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), crmActivity("CRMNoteAdded", payload))
		if err != nil || len(res.Claims) != 0 || len(res.Skipped) != 0 {
			t.Fatalf("payload %s: %+v %v", payload, res, err)
		}
	}
	email := ActivityInput{ID: actIDOne, SourceSystem: "email", Type: "EmailReceived", Payload: json.RawMessage(`{"kind":"email"}`)}
	if res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), email); err != nil || len(res.Claims) != 0 {
		t.Fatalf("email: %+v %v", res, err)
	}
}

func TestMalformedStructuredPayloadIsAnError(t *testing.T) {
	for _, src := range []string{"crm", "calendar", "enrichment"} {
		act := ActivityInput{ID: actIDOne, SourceSystem: src, Type: "CRMFieldChanged", Payload: json.RawMessage(`{not json`)}
		if _, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act); err == nil {
			t.Errorf("%s: malformed payload accepted", src)
		}
	}
}

func calendarActivity(actType, payload string, when time.Time) ActivityInput {
	return ActivityInput{ID: actIDOne, AccountID: acctID, OpportunityID: oppID, Type: actType, SourceSystem: "calendar", OccurredAt: when, Payload: json.RawMessage(payload)}
}

func TestCalendarFutureMeetingBecomesNextMeetingClaimExpiringAtStart(t *testing.T) {
	when := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	for _, typ := range []string{"MeetingScheduled", "MeetingAccepted", "MeetingParticipantAdded"} {
		act := calendarActivity(typ, `{"kind":"calendar_event","event_id":"gcal:x","title":"Acme scoping","start":"2026-08-25T13:00:00Z","end":"2026-08-25T13:45:00Z","status":"scheduled"}`, when)
		res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
		if err != nil || len(res.Claims) != 1 {
			t.Fatalf("%s: %+v %v", typ, res, err)
		}
		c := res.Claims[0]
		start := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
		if c.FieldPath != FieldNextMeeting || c.Standing != FirstPartyRecord || c.Extractor != RuleCalendar || c.ExpiresAt == nil || !c.ExpiresAt.Equal(start) {
			t.Fatalf("%s: claim = %+v", typ, c)
		}
		var v struct {
			EventID string `json:"event_id"`
			Title   string `json:"title"`
			Start   string `json:"start"`
		}
		if err := json.Unmarshal(c.Value, &v); err != nil {
			t.Fatal(err)
		}
		if v.EventID != "gcal:x" || v.Title != "Acme scoping" || v.Start != "2026-08-25T13:00:00Z" {
			t.Fatalf("value = %s", c.Value)
		}
	}
}

func TestCalendarCompletedMeetingYieldsRuleNullClaim(t *testing.T) {
	end := time.Date(2026, 8, 25, 13, 45, 0, 0, time.UTC)
	act := calendarActivity("MeetingCompleted", `{"kind":"calendar_event","event_id":"gcal:x","start":"2026-08-25T13:00:00Z","end":"2026-08-25T13:45:00Z","status":"completed"}`, end)
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
	if err != nil || len(res.Claims) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	c := res.Claims[0]
	if !IsNull(c.Value) || c.FieldPath != FieldNextMeeting || c.ExpiresAt != nil || !c.OccurredAt.Equal(end) || !isRuleNull(c) {
		t.Fatalf("claim = %+v", c)
	}
}

func TestCalendarIgnoresPastMeetingsDeclinesAndOtherStatuses(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	past := `{"event_id":"e","start":"2026-08-25T13:00:00Z","end":"2026-08-25T13:45:00Z","status":"scheduled"}`
	if res, _ := (RuleExtractor{Dir: testDir}).Extract(context.Background(), calendarActivity("MeetingScheduled", past, when)); len(res.Claims) != 0 {
		t.Fatalf("a meeting that already started is not 'next': %+v", res.Claims)
	}
	future := `{"event_id":"e","start":"2026-09-25T13:00:00Z","end":"2026-09-25T13:45:00Z","status":"scheduled"}`
	if res, _ := (RuleExtractor{Dir: testDir}).Extract(context.Background(), calendarActivity("MeetingDeclined", future, when)); len(res.Claims) != 0 {
		t.Fatalf("a decline books nothing: %+v", res.Claims)
	}
	cancelled := `{"event_id":"e","start":"2026-09-25T13:00:00Z","end":"2026-09-25T13:45:00Z","status":"cancelled"}`
	if res, _ := (RuleExtractor{Dir: testDir}).Extract(context.Background(), calendarActivity("MeetingScheduled", cancelled, when)); len(res.Claims) != 0 {
		t.Fatalf("a cancelled meeting is not booked: %+v", res.Claims)
	}
	if _, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), calendarActivity("MeetingScheduled", `{"event_id":"e","start":"yesterday","status":"scheduled"}`, when)); err == nil {
		t.Fatal("an unparseable start must be an error")
	}
}

func TestEnrichmentBecomesThirdPartyTitleClaim(t *testing.T) {
	act := ActivityInput{ID: actIDOne, AccountID: acctID, SourceSystem: "enrichment", Type: "EnrichmentUpdated",
		OccurredAt: time.Date(2026, 9, 12, 6, 0, 0, 0, time.UTC),
		Payload: json.RawMessage(`{"kind":"enrichment","provider":"peoplegraph","subject":{"email":"priya.shah@acme.com","domain":null},
			"observed_at":"2026-09-12T06:00:00Z","facts":{"title":"Senior Engineering Manager","employer_domain":"globex.com"}}`)}
	res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), act)
	if err != nil || len(res.Claims) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	c := res.Claims[0]
	m := ParseMember(c.Value)
	if c.Standing != ThirdParty || c.FieldPath != FieldBuyingGroupMember || c.SubjectPersonID != "priya-id" || m.Title != "Senior Engineering Manager" || m.EmployerDomain != "globex.com" || c.Extractor != RuleEnrichment {
		t.Fatalf("claim = %+v", c)
	}
}

func TestEnrichmentWithoutPersonOrTitleYieldsNothing(t *testing.T) {
	mk := func(payload string) ActivityInput {
		return ActivityInput{ID: actIDOne, SourceSystem: "enrichment", Type: "EnrichmentUpdated", OccurredAt: base, Payload: json.RawMessage(payload)}
	}
	for name, payload := range map[string]string{
		"domain-level facts": `{"subject":{"email":null,"domain":"beta.io"},"facts":{"headcount":900}}`,
		"unknown person":     `{"subject":{"email":"nobody@acme.com"},"facts":{"title":"CEO"}}`,
		"no title":           `{"subject":{"email":"priya.shah@acme.com"},"facts":{"seniority":"director"}}`,
	} {
		res, err := (RuleExtractor{Dir: testDir}).Extract(context.Background(), mk(payload))
		if err != nil || len(res.Claims) != 0 {
			t.Errorf("%s: claims=%+v err=%v", name, res.Claims, err)
		}
	}
}
