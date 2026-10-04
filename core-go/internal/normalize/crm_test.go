package normalize

import (
	"fmt"
	"testing"
)

const crmChangedAt = "2026-09-30T08:15:00Z"

// crmJSON renders a crm_change payload. fields/created/note are raw JSON fragments or "".
func crmJSON(objectType, recordID, account, changedBy, fields, extra string) string {
	acct := "null"
	if account != "" {
		acct = fmt.Sprintf("%q", account)
	}
	if fields != "" {
		fields = `,"fields":` + fields
	}
	return fmt.Sprintf(`{"kind":"crm_change","object_type":%q,"record_id":%q,"account_record_id":%s,
		"changed_at":"2026-09-30T08:15:00Z","changed_by":%q%s%s}`, objectType, recordID, acct, changedBy, fields, extra)
}

func TestNormalizeCRM(t *testing.T) {
	rep := part("dana@ghostvendor.com", "", "actor")
	runCases(t, []normCase{
		{
			name: "Opportunity StageName -> OpportunityStageChanged",
			ev: event(t, "crm", "opp:AC-4-EXP", "field:StageName:Negotiation", "",
				crmJSON("Opportunity", "opp:AC-4-EXP", "account:AC-4", "Dana@ghostvendor.com", `{"StageName":{"old":"Proposal","new":"Negotiation"}}`, "")),
			want: want{
				typ: "OpportunityStageChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4", oppHint: "opp:AC-4-EXP",
				summary: "opp:AC-4-EXP stage changed to Negotiation",
			},
		},
		{
			name: "Contact created -> ContactAdded with the contact as mentioned",
			ev: event(t, "crm", "contact:817", "created", "",
				crmJSON("Contact", "contact:817", "account:AC-4", "dana@ghostvendor.com", `{"Email":{"new":"p@acme.com"}}`, `,"created":true`)),
			want: want{
				typ: "ContactAdded", occurred: crmChangedAt,
				participants: []Participant{part("crm:contact:817", "", "mentioned")},
				accountKind:  HintCRM, accountHint: "account:AC-4",
				summary: "Contact contact:817 added",
			},
		},
		{
			name: "Contact Role__c -> StakeholderRoleChanged",
			ev: event(t, "crm", "contact:817", "field:Role__c:Champion", "",
				crmJSON("Contact", "contact:817", "account:AC-4", "dana@ghostvendor.com", `{"Role__c":{"old":null,"new":"Champion"}}`, "")),
			want: want{
				typ: "StakeholderRoleChanged", occurred: crmChangedAt,
				participants: []Participant{part("crm:contact:817", "", "mentioned")},
				accountKind:  HintCRM, accountHint: "account:AC-4",
				summary: "contact:817 Role__c changed to Champion",
			},
		},
		{
			name: "Contact Title -> StakeholderRoleChanged",
			ev: event(t, "crm", "contact:817", "field:Title:VP Security", "",
				crmJSON("Contact", "contact:817", "account:AC-4", "dana@ghostvendor.com", `{"Title":{"old":"Director","new":"VP Security"}}`, "")),
			want: want{
				typ: "StakeholderRoleChanged", occurred: crmChangedAt,
				participants: []Participant{part("crm:contact:817", "", "mentioned")},
				accountKind:  HintCRM, accountHint: "account:AC-4",
				summary: "contact:817 Title changed to VP Security",
			},
		},
		{
			name: "Note created -> CRMNoteAdded with note body as text",
			ev: event(t, "crm", "note:9", "created", "",
				crmJSON("Note", "note:9", "account:AC-4", "dana@ghostvendor.com", "", `,"created":true,"note_body":"Call with Priya: budget approved."`)),
			want: want{
				typ: "CRMNoteAdded", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4",
				summary: "CRM note added on note:9",
				body:    "Call with Priya: budget approved.",
			},
		},
		{
			name: "any other field -> CRMFieldChanged (numeric value, colon in value)",
			ev: event(t, "crm", "opp:AC-4-EXP", "field:Amount:120000", "",
				crmJSON("Opportunity", "opp:AC-4-EXP", "account:AC-4", "integration:enrich", `{"Amount":{"old":100000,"new":120000},"StageName":{"new":"Negotiation"}}`, "")),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt,
				participants: []Participant{part("integration:enrich", "", "actor")},
				accountKind:  HintCRM, accountHint: "account:AC-4", oppHint: "opp:AC-4-EXP",
				summary: "opp:AC-4-EXP Amount changed to 120000",
			},
		},
		{
			name: "field change with a colon in the new value",
			ev: event(t, "crm", "account:AC-4", "field:Website:https://acme.com", "",
				crmJSON("Account", "account:AC-4", "", "dana@ghostvendor.com", `{"Website":{"new":"https://acme.com"}}`, "")),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4", // Account records identify themselves
				summary: "account:AC-4 Website changed to https://acme.com",
			},
		},
		{
			name: "Account created -> CRMFieldChanged hinted by record id and the Website domain",
			ev: event(t, "crm", "account:AC-4", "created", "",
				crmJSON("Account", "account:AC-4", "account:AC-4", "Dana@ghostvendor.com", `{"Name":{"new":"Acme Corp"},"Website":{"new":"https://www.Acme.com/about"}}`, `,"created":true`)),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4", moreHints: []Hint{dom("acme.com")},
				summary: "Account account:AC-4 created",
			},
		},
		{
			name: "Account created without a Website has only the record id hint",
			ev: event(t, "crm", "account:AC-5", "created", "",
				crmJSON("Account", "account:AC-5", "", "dana@ghostvendor.com", `{"Name":{"new":"Beta"}}`, `,"created":true`)),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-5", summary: "Account account:AC-5 created",
			},
		},
		{
			name: "Opportunity created is keyed by its initial stage",
			ev: event(t, "crm", "opp:AC-4-EXP", "field:StageName:Discovery", "",
				crmJSON("Opportunity", "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com", `{"Name":{"new":"EU"},"StageName":{"new":"Discovery"},"Amount":{"new":156000}}`, `,"created":true`)),
			want: want{
				typ: "OpportunityStageChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4", oppHint: "opp:AC-4-EXP",
				summary: "opp:AC-4-EXP stage changed to Discovery",
			},
		},
		{
			name: "CloseDate and NextStep are plain CRMFieldChanged",
			ev: event(t, "crm", "opp:AC-4-EXP", "field:NextStep:Send order form", "",
				crmJSON("Opportunity", "opp:AC-4-EXP", "account:AC-4", "dana@ghostvendor.com", `{"NextStep":{"old":"x","new":"Send order form"},"CloseDate":{"new":"2026-10-30"}}`, "")),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{rep},
				accountKind: HintCRM, accountHint: "account:AC-4", oppHint: "opp:AC-4-EXP",
				summary: "opp:AC-4-EXP NextStep changed to Send order form",
			},
		},
		{
			name: "Opportunity field change without an account link still hints the opportunity",
			ev: event(t, "crm", "opp:X", "field:Amount:5", "",
				crmJSON("Opportunity", "opp:X", "", "dana@ghostvendor.com", `{"Amount":{"new":5}}`, "")),
			want: want{
				typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{rep},
				oppHint: "opp:X", summary: "opp:X Amount changed to 5",
			},
		},
	})
}

func TestNormalizeCRMRejectsInvalidInput(t *testing.T) {
	stage := `{"StageName":{"old":"Proposal","new":"Negotiation"}}`
	runCases(t, []normCase{
		{name: "key names a field that did not change", ev: event(t, "crm", "opp:1", "field:Amount:5", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", stage, "")), errCode: CodeInvalidEvent},
		{name: "key value disagrees with payload", ev: event(t, "crm", "opp:1", "field:StageName:Closed Won", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", stage, "")), errCode: CodeInvalidEvent},
		{name: "created key without created flag", ev: event(t, "crm", "contact:1", "created", "", crmJSON("Contact", "contact:1", "account:A", "d@ghostvendor.com", "", "")), errCode: CodeInvalidEvent},
		{name: "Account created flag required", ev: event(t, "crm", "account:A", "created", "", crmJSON("Account", "account:A", "account:A", "d@ghostvendor.com", `{"Name":{"new":"A"}}`, "")), errCode: CodeInvalidEvent},
		{name: "Note without a body", ev: event(t, "crm", "note:1", "created", "", crmJSON("Note", "note:1", "account:A", "d@ghostvendor.com", "", `,"created":true,"note_body":null`)), errCode: CodeInvalidEvent},
		{name: "unknown key shape", ev: event(t, "crm", "opp:1", "deleted", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", stage, "")), errCode: CodeUnsupportedEvent},
		{name: "malformed field key", ev: event(t, "crm", "opp:1", "field:StageName", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", stage, "")), errCode: CodeInvalidEvent},
		{name: "field without new value", ev: event(t, "crm", "opp:1", "field:Amount:5", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", `{"Amount":{"old":4}}`, "")), errCode: CodeInvalidEvent},
		{name: "object id differs from record_id", ev: event(t, "crm", "opp:2", "field:StageName:Negotiation", "", crmJSON("Opportunity", "opp:1", "account:A", "d@ghostvendor.com", stage, "")), errCode: CodeInvalidEvent},
		{name: "unknown object type", ev: event(t, "crm", "x:1", "created", "", crmJSON("Lead", "x:1", "", "d@ghostvendor.com", "", `,"created":true`)), errCode: CodeInvalidEvent},
		{name: "missing changed_by", ev: event(t, "crm", "opp:1", "field:StageName:Negotiation", "", crmJSON("Opportunity", "opp:1", "account:A", "", stage, "")), errCode: CodeInvalidEvent},
		{name: "wrong payload kind", ev: event(t, "crm", "opp:1", "created", "", `{"kind":"slack_message"}`), errCode: CodeInvalidEvent},
	})
}

func TestDomainFromWebsite(t *testing.T) {
	cases := []struct{ in, want string }{
		{"acme.com", "acme.com"},
		{"ACME.com", "acme.com"},
		{"https://www.acme.com/about?x=1", "acme.com"},
		{"http://acme.com:8080/", "acme.com"},
		{"www.acme.com/pricing", "acme.com"},
		{"  https://sub.acme.co.uk  ", "sub.acme.co.uk"},
		{"ghostvendor.com", ""},
		{"https://mail.ghostvendor.com", ""},
		{"not a domain", ""},
		{"localhost", ""},
		{"", ""},
		{"https://", ""},
	}
	for _, tc := range cases {
		if got := DomainFromWebsite(tc.in); got != tc.want {
			t.Errorf("DomainFromWebsite(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAccountCreatedDomainFieldAlternatives(t *testing.T) {
	for _, field := range []string{"Website", "Domain", "Domain__c"} {
		payload := crmJSON("Account", "account:AC-9", "", "dana@ghostvendor.com", `{"`+field+`":{"new":"nine.io"}}`, `,"created":true`)
		act, err := Normalize(event(t, "crm", "account:AC-9", "created", "", payload))
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if got := act.AccountHints(); len(got) != 2 || got[1].Value != "nine.io" {
			t.Errorf("%s: hints = %v", field, got)
		}
	}
}
