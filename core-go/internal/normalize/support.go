package normalize

import (
	"encoding/json"
	"strings"
	"time"
)

// Support event keys (contracts/normalization.md, support rows).
const (
	keyCaseOpened = "opened"
	keyCaseClosed = "closed"
	keyChatEnded  = "ended"
)

type supportCasePayload struct {
	Kind            string     `json:"kind"`
	CaseID          string     `json:"case_id"`
	AccountRecordID string     `json:"account_record_id"`
	ContactRecordID *string    `json:"contact_record_id"`
	OwnerEmail      *string    `json:"owner_email"`
	Subject         string     `json:"subject"`
	Description     *string    `json:"description"`
	Priority        *string    `json:"priority"`
	Origin          *string    `json:"origin"`
	OpenedAt        time.Time  `json:"opened_at"`
	ClosedAt        *time.Time `json:"closed_at"`
}

type chatTranscriptPayload struct {
	Kind            string    `json:"kind"`
	TranscriptID    string    `json:"transcript_id"`
	CaseID          *string   `json:"case_id"`
	AccountRecordID string    `json:"account_record_id"`
	ContactRecordID *string   `json:"contact_record_id"`
	OwnerEmail      *string   `json:"owner_email"`
	EndedAt         time.Time `json:"ended_at"`
	BodyText        string    `json:"body_text"`
}

// normalizeSupport implements the support rows: support_case (opened/closed) and chat_transcript (ended).
func normalizeSupport(ev SourceEvent) (Activity, error) {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(ev.Payload, &probe); err != nil {
		return Activity{}, invalid("payload: %v", err)
	}
	key := ev.SourceEventKey
	if key != keyCaseOpened && key != keyCaseClosed && key != keyChatEnded {
		return Activity{}, unsupported("support event key %q has no mapping (want opened, closed or ended)", key)
	}
	switch probe.Kind {
	case "support_case":
		return normalizeCase(ev)
	case "chat_transcript":
		return normalizeChat(ev)
	default:
		return Activity{}, unsupported("support payload kind %q has no mapping", probe.Kind)
	}
}

func normalizeCase(ev SourceEvent) (Activity, error) {
	p, err := decodePayload[supportCasePayload](ev, "support_case")
	if err != nil {
		return Activity{}, err
	}
	if err := requireObjectID(ev, p.CaseID); err != nil {
		return Activity{}, err
	}
	if p.OpenedAt.IsZero() || strings.TrimSpace(p.AccountRecordID) == "" {
		return Activity{}, invalid("support_case requires opened_at and account_record_id")
	}
	activityType, at, verb := "SupportTicketOpened", p.OpenedAt, "opened"
	switch ev.SourceEventKey {
	case keyCaseClosed:
		if p.ClosedAt == nil || p.ClosedAt.Before(p.OpenedAt) {
			return Activity{}, invalid("event key \"closed\" requires a closed_at not before opened_at")
		}
		activityType, at, verb = "SupportTicketResolved", *p.ClosedAt, "resolved"
	case keyChatEnded:
		return Activity{}, invalid("event key \"ended\" belongs to chat_transcript, not support_case")
	}
	parts, err := supportParticipants(p.OwnerEmail, p.ContactRecordID)
	if err != nil {
		return Activity{}, err
	}
	act := newActivity(ev, activityType, at)
	act.participants = parts
	act.accountHints = hintOf(HintCRM, p.AccountRecordID)
	act.bodyText = strings.TrimSpace(p.Subject + "\n\n" + deref(p.Description))
	act.summary = summarize(withDetail("Support case "+verb, p.Subject))
	return act, nil
}

func normalizeChat(ev SourceEvent) (Activity, error) {
	if ev.SourceEventKey != keyChatEnded {
		return Activity{}, invalid("chat_transcript uses event key \"ended\", got %q", ev.SourceEventKey)
	}
	p, err := decodePayload[chatTranscriptPayload](ev, "chat_transcript")
	if err != nil {
		return Activity{}, err
	}
	if err := requireObjectID(ev, p.TranscriptID); err != nil {
		return Activity{}, err
	}
	if p.EndedAt.IsZero() || strings.TrimSpace(p.AccountRecordID) == "" || strings.TrimSpace(p.BodyText) == "" {
		return Activity{}, invalid("chat_transcript requires ended_at, account_record_id and body_text")
	}
	parts, err := supportParticipants(p.OwnerEmail, p.ContactRecordID)
	if err != nil {
		return Activity{}, err
	}
	act := newActivity(ev, "ChatTranscriptReady", p.EndedAt)
	act.participants = parts
	act.accountHints = hintOf(HintCRM, p.AccountRecordID)
	act.bodyText = p.BodyText
	act.summary = summarize("Live chat transcript " + p.TranscriptID)
	return act, nil
}

// supportParticipants: our owner is the actor, the CRM contact is mentioned.
func supportParticipants(owner, contact *string) ([]Participant, error) {
	parts := []Participant{}
	if raw := strings.TrimSpace(deref(owner)); raw != "" {
		email, ok := normalizeEmailAddress(raw)
		if !ok {
			return nil, invalid("owner_email %q is not a valid email", raw)
		}
		parts = append(parts, Participant{RawIdentity: email, Role: RoleActor})
	}
	if c := strings.TrimSpace(deref(contact)); c != "" {
		parts = append(parts, Participant{RawIdentity: "crm:" + c, Role: RoleMentioned})
	}
	return parts, nil
}
