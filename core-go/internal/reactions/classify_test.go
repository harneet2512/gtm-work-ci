package reactions

import (
	"encoding/json"
	"testing"
	"time"
)

// testEpisode returns an episode whose recipients are one person id and one email, whose trigger is
// one activity on one thread, sent at a fixed time — the channel links() and classify() read.
func testEpisode() *episode {
	return &episode{
		ID:         "11111111-1111-4111-8111-111111111111",
		SendAt:     testNow,
		TriggerIDs: []string{"22222222-2222-4222-8222-222222222222"},
		recipients: map[string]bool{"33333333-3333-4333-8333-333333333333": true, "marco@acme.test": true},
		threads:    map[string]bool{"T-1": true},
	}
}

func replyActivity(typ, body string, parts ...participant) *activity {
	return &activity{ID: "a", Type: typ, Occurred: testNow.Add(time.Hour), Body: body, participants: parts}
}

func TestClassifyReplyTypes(t *testing.T) {
	ep := testEpisode()
	for _, typ := range []string{"EmailReply", "EmailReceived", "CustomerReplied", "SlackMessage"} {
		a := replyActivity(typ, "ok", participant{Raw: "marco@acme.test", Role: "from"})
		got := classify(ep, a, DefaultSilenceWindow)
		if len(got) != 1 || got[0] != "replied" {
			t.Errorf("%s: got %v, want [replied]", typ, got)
		}
	}
	// A reply type authored by our side is no customer reaction at all.
	ours := replyActivity("EmailReply", "following up", participant{Raw: "dana@vendor.example", Role: "from"})
	if got := classify(ep, ours, DefaultSilenceWindow); len(got) != 0 {
		t.Errorf("internal author: got %v, want none", got)
	}
}

func TestClassifyActivityTypes(t *testing.T) {
	ep := testEpisode()
	cases := []struct {
		typ   string
		parts []participant
		want  []string
	}{
		// Calendar/CRM reaction types only count when a customer side produced them — the responder,
		// the adder (changed_by), or the added person must be external (HAR-120 H1).
		{"MeetingAccepted", []participant{{Raw: "marco@acme.test", Role: "actor"}}, []string{"meeting_accepted"}},
		{"MeetingDeclined", []participant{{Raw: "marco@acme.test", Role: "actor"}}, []string{"conversation_cooled"}},
		{"MeetingParticipantAdded", []participant{{Raw: "priya@acme.test", Role: "attendee"}}, []string{"stakeholder_added"}},
		{"ContactAdded", []participant{
			{Raw: "crm:contact:817", Role: "mentioned"}, {Raw: "marco@acme.test", Role: "actor"}},
			[]string{"stakeholder_added"}},
	}
	for _, c := range cases {
		a := replyActivity(c.typ, "", c.parts...)
		if got := classify(ep, a, DefaultSilenceWindow); !sameStrings(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.typ, got, c.want)
		}
	}
	// Our own side doing the same things produces no customer reaction.
	ours := []struct {
		typ   string
		parts []participant
	}{
		{"MeetingAccepted", []participant{{Raw: "dana@vendor.example", Role: "actor"}}},
		{"MeetingDeclined", []participant{{Raw: "dana@vendor.example", Role: "actor"}}},
		{"MeetingParticipantAdded", []participant{{Raw: "dana@vendor.example", Role: "attendee"}}},
		{"ContactAdded", []participant{
			{Raw: "crm:contact:817", Role: "mentioned"}, {Raw: "dana@vendor.example", Role: "actor"}}},
	}
	for _, c := range ours {
		a := replyActivity(c.typ, "", c.parts...)
		if got := classify(ep, a, DefaultSilenceWindow); len(got) != 0 {
			t.Errorf("our %s: got %v, want none — our own actions are not customer reactions", c.typ, got)
		}
	}
	// Customer-scheduled meeting advances; one we organized does not.
	cust := replyActivity("MeetingScheduled", "", participant{Raw: "marco@acme.test", Role: "organizer"})
	if got := classify(ep, cust, DefaultSilenceWindow); !sameStrings(got, []string{"conversation_advanced"}) {
		t.Errorf("customer MeetingScheduled: got %v", got)
	}
	org := replyActivity("MeetingScheduled", "", participant{Raw: "dana@vendor.example", Role: "organizer"})
	if got := classify(ep, org, DefaultSilenceWindow); len(got) != 0 {
		t.Errorf("our MeetingScheduled: got %v, want none", got)
	}
}

func TestClassifySilenceWindow(t *testing.T) {
	ep := testEpisode()
	before := &activity{Type: "CustomerWentSilent", Occurred: ep.SendAt.Add(DefaultSilenceWindow - time.Hour)}
	at := &activity{Type: "CustomerWentSilent", Occurred: ep.SendAt.Add(DefaultSilenceWindow)}
	if got := classify(ep, before, DefaultSilenceWindow); len(got) != 0 {
		t.Errorf("silence before the window: got %v, want none — silence is never inferred early", got)
	}
	if got := classify(ep, at, DefaultSilenceWindow); !sameStrings(got, []string{"ignored"}) {
		t.Errorf("silence at the window: got %v, want [ignored]", got)
	}
}

func TestClassifyPhrases(t *testing.T) {
	ep := testEpisode()
	cases := []struct {
		body string
		want string
	}{
		{"Can you send over the security questionnaire?", "requested_document"},
		{"We're worried about the budget freeze on our side", "objection_raised"},
		{"Put this on hold until next quarter", "conversation_cooled"},
		{"We'll sign this week", "new_commitment"},
		{"Sounds good, talk next week", "conversation_advanced"},
	}
	for _, c := range cases {
		a := replyActivity("EmailReply", c.body, participant{Raw: "marco@acme.test", Role: "from"})
		got := classify(ep, a, DefaultSilenceWindow)
		if len(got) != 2 || got[0] != "replied" || got[1] != c.want {
			t.Errorf("%q: got %v, want [replied %s]", c.body, got, c.want)
		}
	}
	// A reply with no matching phrase yields just 'replied'.
	plain := replyActivity("EmailReply", "Thanks — confirmed for Thursday.", participant{Raw: "marco@acme.test", Role: "from"})
	if got := classify(ep, plain, DefaultSilenceWindow); !sameStrings(got, []string{"replied"}) {
		t.Errorf("plain reply: got %v, want [replied]", got)
	}
	// Phrase text in a non-reply type never classifies by phrase (authorship gate).
	meet := replyActivity("MeetingScheduled", "next steps", participant{Raw: "marco@acme.test", Role: "organizer"})
	if got := classify(ep, meet, DefaultSilenceWindow); !sameStrings(got, []string{"conversation_advanced"}) {
		// the type-derived reaction only — phrase rules would double it
		t.Errorf("meeting text: got %v", got)
	}
}

func TestStakeholderRoleReaction(t *testing.T) {
	mk := func(old, new_ any, key string) *activity {
		p, _ := json.Marshal(map[string]any{"object_type": "Contact",
			"fields": map[string]any{"Role": map[string]any{"old": old, "new": new_}}})
		return &activity{Type: "StakeholderRoleChanged", EventKey: key, Payload: p}
	}
	if got := stakeholderRoleReaction(mk(nil, "Champion", "field:Role:Champion")); got != "stakeholder_added" {
		t.Errorf("role gained: got %q", got)
	}
	if got := stakeholderRoleReaction(mk("Champion", nil, "field:Role:")); got != "stakeholder_removed" {
		t.Errorf("role emptied: got %q", got)
	}
	if got := stakeholderRoleReaction(mk("Champion", "Economic Buyer", "field:Role:Economic Buyer")); got != "" {
		t.Errorf("role swap: got %q, want none", got)
	}
	if got := stakeholderRoleReaction(mk("Champion", nil, "field:Other:x")); got != "" {
		t.Errorf("unselected field: got %q, want none", got)
	}
}

func TestStageOutcome(t *testing.T) {
	cases := []struct {
		old, new_, want string
	}{
		{"Commercial Review", "Negotiation", "stage_advanced"},
		{"Negotiation", "Discovery", "stage_regressed"},
		{"Negotiation", "Closed Won", "closed_won"},
		{"Negotiation", "Closed Lost", "closed_lost"},
		{"Discovery", "Discovery", ""},       // no move
		{"Unknown Stage", "Negotiation", ""}, // unraked side: never guess a direction
		{"Negotiation", "Some Custom", ""},
	}
	for _, c := range cases {
		if got := stageOutcome(c.old, c.new_); got != c.want {
			t.Errorf("%q -> %q: got %q, want %q", c.old, c.new_, got, c.want)
		}
	}
}

func TestClassifyOutcomes(t *testing.T) {
	ep := testEpisode()
	ep.OpportunityID = "44444444-4444-4444-8444-444444444444"
	other := testEpisode()
	other.OpportunityID = "55555555-5555-4555-8555-555555555555"

	stage := func(old, new_ string) *activity {
		p, _ := json.Marshal(map[string]any{"object_type": "Opportunity",
			"fields": map[string]any{"StageName": map[string]any{"old": old, "new": new_}}})
		return &activity{Type: "OpportunityStageChanged", EventKey: "field:StageName:" + new_,
			OpportunityID: ep.OpportunityID, Payload: p}
	}
	if got := classifyOutcomes(ep, stage("Commercial Review", "Negotiation")); len(got) != 1 || got[0].typ != "stage_advanced" {
		t.Errorf("stage move: got %+v", got)
	}
	if got := classifyOutcomes(ep, stage("Negotiation", "Closed Won")); len(got) != 1 || got[0].typ != "closed_won" {
		t.Errorf("closed won: got %+v", got)
	}
	// Another opportunity's move is not this episode's outcome.
	if got := classifyOutcomes(other, stage("Commercial Review", "Negotiation")); got != nil {
		t.Errorf("wrong opportunity: got %+v, want nil", got)
	}

	amount, _ := json.Marshal(map[string]any{"object_type": "Opportunity",
		"fields": map[string]any{"Amount": map[string]any{"old": 100000, "new": 150000}}})
	acv := &activity{Type: "CRMFieldChanged", EventKey: "field:Amount:150000",
		OpportunityID: ep.OpportunityID, Payload: amount}
	got := classifyOutcomes(ep, acv)
	if len(got) != 1 || got[0].typ != "acv_change" || got[0].value["delta"] != 50000.0 {
		t.Errorf("acv: got %+v", got)
	}
	// A non-Opportunity object or an unknown field is no outcome.
	notOpp, _ := json.Marshal(map[string]any{"object_type": "Contact",
		"fields": map[string]any{"Amount": map[string]any{"old": 1, "new": 2}}})
	acv.Payload = notOpp
	if got := classifyOutcomes(ep, acv); got != nil {
		t.Errorf("contact amount: got %+v, want nil", got)
	}
}

func TestLinks(t *testing.T) {
	ep := testEpisode()
	ep.CorrelationID = "corr-1"
	ep.OpportunityID = "opp-1"

	link := &activity{CorrelationID: "corr-1"}
	if !ep.links(link) {
		t.Error("correlation match should link")
	}
	caused := &activity{CausedBy: ep.TriggerIDs[0]}
	if !ep.links(caused) {
		t.Error("caused-by a trigger should link")
	}
	threaded := &activity{ThreadID: "T-1"}
	if !ep.links(threaded) {
		t.Error("same thread should link")
	}
	scope := &activity{Type: "ContactAdded"}
	if !ep.links(scope) {
		t.Error("account-scope type should link")
	}
	onOpp := &activity{Type: "MeetingScheduled", OpportunityID: "opp-1"}
	if !ep.links(onOpp) {
		t.Error("activity on the episode's opportunity should link")
	}
	involving := &activity{Type: "DocumentShared",
		participants: []participant{{Raw: "marco@acme.test", Role: "attendee"}}}
	if !ep.links(involving) {
		t.Error("a recipient as participant should link")
	}
	// A reply links only when the customer authored it.
	extReply := &activity{Type: "EmailReply",
		participants: []participant{{Raw: "priya@ext.test", Role: "from"}}}
	if !ep.links(extReply) {
		t.Error("external author on the account channel should link")
	}
	ourReply := &activity{Type: "EmailReply",
		participants: []participant{{Raw: "dana@vendor.example", Role: "from"}}}
	if ep.links(ourReply) {
		t.Error("our own send must not link as a customer reply")
	}
	unrelated := &activity{Type: "EmailReply",
		participants: []participant{{Raw: "dana@vendor.example", Role: "from"}},
		ThreadID:     "T-elsewhere"}
	if ep.links(unrelated) {
		t.Error("unlinked activity must not be attributed")
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
