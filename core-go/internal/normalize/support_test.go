package normalize

import (
	"strings"
	"testing"
)

const caseJSON = `{"kind":"support_case","case_id":"case:5001","account_record_id":"account:A1",
	"contact_record_id":"contact:C1","owner_email":"Agent@vendor.example","subject":"Alerts missing",
	"description":"No update notifications.","priority":"Medium","origin":"Email",
	"opened_at":"2020-12-29T08:36:00Z","closed_at":%s}`

const chatJSON = `{"kind":"chat_transcript","transcript_id":"chat:5701","case_id":"case:5001",
	"account_record_id":"account:A1","contact_record_id":%s,"owner_email":%s,
	"ended_at":"2023-03-08T07:07:30Z","body_text":"[06:51] Jakob (Customer): Hi\n[06:52] Agent: Hello"}`

func supportCase(closedAt string) string { return strings.Replace(caseJSON, "%s", closedAt, 1) }

func chat(contact, owner string) string {
	return strings.Replace(strings.Replace(chatJSON, "%s", contact, 1), "%s", owner, 1)
}

func TestNormalizeSupport(t *testing.T) {
	people := []Participant{part("agent@vendor.example", "", "actor"), part("crm:contact:C1", "", "mentioned")}
	body := "Alerts missing\n\nNo update notifications."
	runCases(t, []normCase{
		{
			name: "case opened -> SupportTicketOpened at opened_at",
			ev:   event(t, "support", "case:5001", "opened", "", supportCase("null")),
			want: want{typ: "SupportTicketOpened", occurred: "2020-12-29T08:36:00Z", participants: people,
				accountKind: HintCRM, accountHint: "account:A1", summary: "Support case opened: Alerts missing", body: body},
		},
		{
			name: "case closed -> SupportTicketResolved at closed_at",
			ev:   event(t, "support", "case:5001", "closed", "", supportCase(`"2021-01-04T10:00:00Z"`)),
			want: want{typ: "SupportTicketResolved", occurred: "2021-01-04T10:00:00Z", participants: people,
				accountKind: HintCRM, accountHint: "account:A1", summary: "Support case resolved: Alerts missing", body: body},
		},
		{
			name: "chat ended -> ChatTranscriptReady with the transcript as body",
			ev:   event(t, "support", "chat:5701", "ended", "", chat(`"contact:C1"`, `"agent@vendor.example"`)),
			want: want{typ: "ChatTranscriptReady", occurred: "2023-03-08T07:07:30Z", participants: people,
				accountKind: HintCRM, accountHint: "account:A1", summary: "Live chat transcript chat:5701",
				body: "[06:51] Jakob (Customer): Hi\n[06:52] Agent: Hello"},
		},
		{
			name: "chat without contact or owner has no participants",
			ev:   event(t, "support", "chat:5701", "ended", "", chat("null", "null")),
			want: want{typ: "ChatTranscriptReady", occurred: "2023-03-08T07:07:30Z", accountKind: HintCRM,
				accountHint: "account:A1", body: "[06:51] Jakob (Customer): Hi\n[06:52] Agent: Hello"},
		},
	})
}

func TestNormalizeSupportRejectsInvalidInput(t *testing.T) {
	runCases(t, []normCase{
		{name: "closed key on an open case", ev: event(t, "support", "case:5001", "closed", "", supportCase("null")), errCode: CodeInvalidEvent},
		{name: "unknown key", ev: event(t, "support", "case:5001", "escalated", "", supportCase("null")), errCode: CodeUnsupportedEvent},
		{name: "object id differs from case_id", ev: event(t, "support", "case:9", "opened", "", supportCase("null")), errCode: CodeInvalidEvent},
		{name: "chat key on a case payload", ev: event(t, "support", "case:5001", "ended", "", supportCase("null")), errCode: CodeInvalidEvent},
		{name: "bad owner email", ev: event(t, "support", "chat:5701", "ended", "", chat("null", `"not-an-email"`)), errCode: CodeInvalidEvent},
		{name: "unknown payload kind", ev: event(t, "support", "x", "opened", "", `{"kind":"ticket"}`), errCode: CodeUnsupportedEvent},
		{name: "case closed before it opened", ev: event(t, "support", "case:5001", "closed", "", supportCase(`"2019-01-01T00:00:00Z"`)), errCode: CodeInvalidEvent},
	})
}
