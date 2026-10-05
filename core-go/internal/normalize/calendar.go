package normalize

import (
	"strings"
	"time"
)

type calendarAttendee struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Response string `json:"response"`
}

type calendarPayload struct {
	Kind              string             `json:"kind"`
	EventID           string             `json:"event_id"`
	Title             string             `json:"title"`
	Start             time.Time          `json:"start"`
	End               time.Time          `json:"end"`
	Status            string             `json:"status"`
	Organizer         address            `json:"organizer"`
	Attendees         []calendarAttendee `json:"attendees"`
	Description       string             `json:"description"`
	CRMOpportunityRef *string            `json:"crm_opportunity_ref"`
}

const (
	keyResponsePrefix      = "response:"
	keyAttendeeAddedPrefix = "attendee_added:"
)

var attendeeResponses = map[string]struct{}{
	"needs_action": {}, "accepted": {}, "declined": {}, "tentative": {},
}

// normalizeCalendar implements the four calendar rows of the mapping table.
func normalizeCalendar(ev SourceEvent) (Activity, error) {
	key := ev.SourceEventKey
	if !isCalendarKey(key) {
		return Activity{}, unsupported("calendar event key %q has no mapping", key)
	}
	p, err := decodePayload[calendarPayload](ev, "calendar_event")
	if err != nil {
		return Activity{}, err
	}
	if err := validateCalendar(ev, p); err != nil {
		return Activity{}, err
	}

	var act Activity
	switch {
	case key == "scheduled":
		act, err = calendarScheduled(ev, p)
	case key == "completed":
		act, err = calendarCompleted(ev, p)
	case strings.HasPrefix(key, keyResponsePrefix):
		act, err = calendarResponse(ev, p)
	default:
		act, err = calendarAttendeeAdded(ev, p)
	}
	if err != nil {
		return Activity{}, err
	}
	act.accountHints = domainHint(calendarAccountDomain(p))
	act.opportunityHints = hintOf(HintCRM, deref(p.CRMOpportunityRef))
	return act, nil
}

func isCalendarKey(key string) bool {
	return key == "scheduled" || key == "completed" ||
		strings.HasPrefix(key, keyResponsePrefix) || strings.HasPrefix(key, keyAttendeeAddedPrefix)
}

func validateCalendar(ev SourceEvent, p calendarPayload) error {
	if err := requireObjectID(ev, p.EventID); err != nil {
		return err
	}
	if p.Start.IsZero() || p.End.IsZero() || p.End.Before(p.Start) {
		return invalid("calendar event needs start and end with end >= start")
	}
	switch p.Status {
	case "scheduled", "completed", "cancelled":
	default:
		return invalid("calendar status %q is not scheduled, completed or cancelled", p.Status)
	}
	if _, ok := normalizeEmailAddress(p.Organizer.Email); !ok {
		return invalid("organizer %q is not a valid email", p.Organizer.Email)
	}
	for _, a := range p.Attendees {
		if _, ok := normalizeEmailAddress(a.Email); !ok {
			return invalid("attendee %q is not a valid email", a.Email)
		}
		if _, ok := attendeeResponses[a.Response]; !ok {
			return invalid("attendee response %q is not valid", a.Response)
		}
	}
	return nil
}

func calendarScheduled(ev SourceEvent, p calendarPayload) (Activity, error) {
	if p.Status != "scheduled" {
		return Activity{}, invalid("event key \"scheduled\" requires status scheduled, got %q", p.Status)
	}
	at, err := requireOccurredAt(ev)
	if err != nil {
		return Activity{}, err
	}
	act := newActivity(ev, "MeetingScheduled", at)
	org, _ := participantFromAddress(p.Organizer, RoleOrganizer)
	act.participants = addParticipant(act.participants, org)
	act.participants = append(act.participants, attendeeParticipants(p, RoleAttendee)...)
	act.summary = summarize("Meeting scheduled: " + p.Title)
	return act, nil
}

func calendarCompleted(ev SourceEvent, p calendarPayload) (Activity, error) {
	if p.Status != "completed" {
		return Activity{}, invalid("event key \"completed\" requires status completed, got %q", p.Status)
	}
	act := newActivity(ev, "MeetingCompleted", p.End)
	act.participants = attendeeParticipants(p, RoleAttendee)
	act.summary = summarize("Meeting completed: " + p.Title)
	return act, nil
}

// calendarResponse handles "response:<email>:<response>".
func calendarResponse(ev SourceEvent, p calendarPayload) (Activity, error) {
	rest := strings.TrimPrefix(ev.SourceEventKey, keyResponsePrefix)
	cut := strings.LastIndex(rest, ":")
	if cut <= 0 || cut == len(rest)-1 {
		return Activity{}, invalid("event key %q must look like response:<email>:<response>", ev.SourceEventKey)
	}
	email, ok := normalizeEmailAddress(rest[:cut])
	if !ok {
		return Activity{}, invalid("event key %q names an invalid email", ev.SourceEventKey)
	}
	response := rest[cut+1:]
	var activityType, verb string
	switch response {
	case "accepted":
		activityType, verb = "MeetingAccepted", "accepted"
	case "declined":
		activityType, verb = "MeetingDeclined", "declined"
	case "tentative", "needs_action":
		return Activity{}, unsupported("attendee response %q has no activity mapping", response)
	default:
		return Activity{}, invalid("attendee response %q is not valid", response)
	}
	att, found := findAttendee(p, email)
	if !found || att.Response != response {
		return Activity{}, invalid("payload does not show %s with response %s", email, response)
	}
	at, err := requireOccurredAt(ev)
	if err != nil {
		return Activity{}, err
	}
	act := newActivity(ev, activityType, at)
	act.participants = []Participant{{RawIdentity: email, DisplayName: strings.TrimSpace(att.Name), Role: RoleActor}}
	act.summary = summarize(email + " " + verb + ": " + p.Title)
	return act, nil
}

// calendarAttendeeAdded handles "attendee_added:<email>".
func calendarAttendeeAdded(ev SourceEvent, p calendarPayload) (Activity, error) {
	raw := strings.TrimPrefix(ev.SourceEventKey, keyAttendeeAddedPrefix)
	email, ok := normalizeEmailAddress(raw)
	if !ok {
		return Activity{}, invalid("event key %q names an invalid email", ev.SourceEventKey)
	}
	att, found := findAttendee(p, email)
	if !found {
		return Activity{}, invalid("%s is not an attendee in the payload", email)
	}
	at, err := requireOccurredAt(ev)
	if err != nil {
		return Activity{}, err
	}
	act := newActivity(ev, "MeetingParticipantAdded", at)
	act.participants = []Participant{{RawIdentity: email, DisplayName: strings.TrimSpace(att.Name), Role: RoleAttendee}}
	act.summary = summarize(email + " added to: " + p.Title)
	return act, nil
}

func findAttendee(p calendarPayload, email string) (calendarAttendee, bool) {
	for _, a := range p.Attendees {
		if norm, _ := normalizeEmailAddress(a.Email); norm == email {
			return a, true
		}
	}
	return calendarAttendee{}, false
}

func attendeeParticipants(p calendarPayload, role string) []Participant {
	out := make([]Participant, 0, len(p.Attendees))
	for _, a := range p.Attendees {
		part, _ := participantFromAddress(address{Email: a.Email, Name: a.Name}, role) // validated
		out = addParticipant(out, part)
	}
	return out
}

// calendarAccountDomain: first external attendee, then the organizer.
func calendarAccountDomain(p calendarPayload) string {
	emails := make([]string, 0, len(p.Attendees)+1)
	for _, a := range p.Attendees {
		emails = append(emails, a.Email)
	}
	return firstExternalDomain(append(emails, p.Organizer.Email)...)
}
