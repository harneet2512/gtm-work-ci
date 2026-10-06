package normalize

import (
	"fmt"
	"testing"
)

func calendarJSON(status, extra string) string {
	return fmt.Sprintf(`{"kind":"calendar_event","event_id":"ev1","title":"EU rollout sync",
		"start":"2026-10-01T14:00:00Z","end":"2026-10-01T14:45:00Z","status":%q,
		"organizer":{"email":"Dana@ghostvendor.com","name":"Dana Kim"},
		"attendees":[
			{"email":"lee@ghostvendor.com","response":"accepted"},
			{"email":"Marco@Acme.com","name":"Marco Ruiz","response":"declined"},
			{"email":"priya@acme.com","response":"needs_action"}
		]%s}`, status, extra)
}

func TestNormalizeCalendar(t *testing.T) {
	const changedAt = "2026-09-30T09:00:00Z"
	scheduledAttendees := []Participant{
		part("dana@ghostvendor.com", "Dana Kim", "organizer"),
		part("lee@ghostvendor.com", "", "attendee"),
		part("marco@acme.com", "Marco Ruiz", "attendee"),
		part("priya@acme.com", "", "attendee"),
	}
	runCases(t, []normCase{
		{
			name: "scheduled -> MeetingScheduled at the change time",
			ev:   event(t, "calendar", "ev1", "scheduled", changedAt, calendarJSON("scheduled", `,"crm_opportunity_ref":"opp:AC-4-EXP"`)),
			want: want{
				typ: "MeetingScheduled", occurred: changedAt, participants: scheduledAttendees,
				accountHint: "acme.com", oppHint: "opp:AC-4-EXP",
				summary: "Meeting scheduled: EU rollout sync",
			},
		},
		{
			name: "attendee accepted -> MeetingAccepted with the attendee as actor",
			ev:   event(t, "calendar", "ev1", "response:lee@ghostvendor.com:accepted", changedAt, calendarJSON("scheduled", "")),
			want: want{
				typ: "MeetingAccepted", occurred: changedAt,
				participants: []Participant{part("lee@ghostvendor.com", "", "actor")},
				accountHint:  "acme.com",
				summary:      "lee@ghostvendor.com accepted: EU rollout sync",
			},
		},
		{
			name: "attendee declined -> MeetingDeclined (address case-insensitive)",
			ev:   event(t, "calendar", "ev1", "response:marco@acme.com:declined", changedAt, calendarJSON("scheduled", "")),
			want: want{
				typ: "MeetingDeclined", occurred: changedAt,
				participants: []Participant{part("marco@acme.com", "Marco Ruiz", "actor")},
				accountHint:  "acme.com",
				summary:      "marco@acme.com declined: EU rollout sync",
			},
		},
		{
			name: "attendee added -> MeetingParticipantAdded",
			ev:   event(t, "calendar", "ev1", "attendee_added:priya@acme.com", changedAt, calendarJSON("scheduled", "")),
			want: want{
				typ: "MeetingParticipantAdded", occurred: changedAt,
				participants: []Participant{part("priya@acme.com", "", "attendee")},
				accountHint:  "acme.com",
				summary:      "priya@acme.com added to: EU rollout sync",
			},
		},
		{
			name: "completed -> MeetingCompleted at the end time with attendees only",
			ev:   event(t, "calendar", "ev1", "completed", "", calendarJSON("completed", "")),
			want: want{
				typ: "MeetingCompleted", occurred: "2026-10-01T14:45:00Z",
				participants: scheduledAttendees[1:],
				accountHint:  "acme.com",
				summary:      "Meeting completed: EU rollout sync",
			},
		},
		{
			name: "meeting with only internal people has no account hint",
			ev: event(t, "calendar", "ev1", "scheduled", changedAt, `{"kind":"calendar_event","event_id":"ev1","title":"1:1",
				"start":"2026-10-01T14:00:00Z","end":"2026-10-01T14:30:00Z","status":"scheduled",
				"organizer":{"email":"dana@ghostvendor.com"},"attendees":[{"email":"lee@ghostvendor.com","response":"accepted"}]}`),
			want: want{
				typ: "MeetingScheduled", occurred: changedAt,
				participants: []Participant{part("dana@ghostvendor.com", "", "organizer"), part("lee@ghostvendor.com", "", "attendee")},
				summary:      "Meeting scheduled: 1:1",
			},
		},
	})
}

func TestNormalizeCalendarRejectsInvalidInput(t *testing.T) {
	const changedAt = "2026-09-30T09:00:00Z"
	runCases(t, []normCase{
		{name: "scheduled without occurred_at", ev: event(t, "calendar", "ev1", "scheduled", "", calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "scheduled key on completed event", ev: event(t, "calendar", "ev1", "scheduled", changedAt, calendarJSON("completed", "")), errCode: CodeInvalidEvent},
		{name: "completed key on scheduled event", ev: event(t, "calendar", "ev1", "completed", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "cancelled events are not mapped", ev: event(t, "calendar", "ev1", "cancelled", changedAt, calendarJSON("cancelled", "")), errCode: CodeUnsupportedEvent},
		{name: "tentative response is not mapped", ev: event(t, "calendar", "ev1", "response:lee@ghostvendor.com:tentative", changedAt, calendarJSON("scheduled", "")), errCode: CodeUnsupportedEvent},
		{name: "response key disagrees with payload", ev: event(t, "calendar", "ev1", "response:lee@ghostvendor.com:declined", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "response from a non-attendee", ev: event(t, "calendar", "ev1", "response:ghost@acme.com:accepted", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "response without occurred_at", ev: event(t, "calendar", "ev1", "response:lee@ghostvendor.com:accepted", "", calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "malformed response key", ev: event(t, "calendar", "ev1", "response:nocolon", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "attendee_added for a non-attendee", ev: event(t, "calendar", "ev1", "attendee_added:ghost@acme.com", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "object id differs from event_id", ev: event(t, "calendar", "zzz", "scheduled", changedAt, calendarJSON("scheduled", "")), errCode: CodeInvalidEvent},
		{name: "wrong payload kind", ev: event(t, "calendar", "ev1", "scheduled", changedAt, `{"kind":"email"}`), errCode: CodeInvalidEvent},
		{name: "unknown status", ev: event(t, "calendar", "ev1", "scheduled", changedAt, calendarJSON("tentative", "")), errCode: CodeInvalidEvent},
		{name: "unparseable organizer", ev: event(t, "calendar", "ev1", "scheduled", changedAt, `{"kind":"calendar_event","event_id":"ev1","title":"t","start":"2026-10-01T14:00:00Z","end":"2026-10-01T14:30:00Z","status":"scheduled","organizer":{"email":"@@"},"attendees":[]}`), errCode: CodeInvalidEvent},
		{name: "end before start", ev: event(t, "calendar", "ev1", "completed", "", `{"kind":"calendar_event","event_id":"ev1","title":"t","start":"2026-10-01T15:00:00Z","end":"2026-10-01T14:30:00Z","status":"completed","organizer":{"email":"a@acme.com"},"attendees":[]}`), errCode: CodeInvalidEvent},
	})
}
