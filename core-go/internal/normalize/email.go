package normalize

import (
	"strings"
	"time"
)

type emailPayload struct {
	Kind              string    `json:"kind"`
	MessageID         string    `json:"message_id"`
	ThreadID          string    `json:"thread_id"`
	InReplyTo         *string   `json:"in_reply_to"`
	Direction         string    `json:"direction"`
	From              address   `json:"from"`
	To                []address `json:"to"`
	CC                []address `json:"cc"`
	Date              time.Time `json:"date"`
	Subject           string    `json:"subject"`
	BodyText          string    `json:"body_text"`
	Attachments       []string  `json:"attachments"`
	CRMOpportunityRef *string   `json:"crm_opportunity_ref"`
}

// normalizeEmail implements the three email rows of the mapping table.
func normalizeEmail(ev SourceEvent) (Activity, error) {
	if ev.SourceEventKey != "received" && ev.SourceEventKey != "sent" {
		return Activity{}, unsupported("email event key %q has no mapping (want received or sent)", ev.SourceEventKey)
	}
	p, err := decodePayload[emailPayload](ev, "email")
	if err != nil {
		return Activity{}, err
	}
	if err := requireObjectID(ev, p.MessageID); err != nil {
		return Activity{}, err
	}
	if p.ThreadID == "" || p.Date.IsZero() || len(p.To) == 0 {
		return Activity{}, invalid("email requires thread_id, date and at least one recipient")
	}
	activityType, err := emailType(ev.SourceEventKey, p)
	if err != nil {
		return Activity{}, err
	}
	participants, err := emailParticipants(p)
	if err != nil {
		return Activity{}, err
	}

	act := newActivity(ev, activityType, p.Date)
	act.participants = participants
	act.accountHints = emailAccountHints(p, participants)
	act.opportunityHints = append(hintOf(HintCRM, deref(p.CRMOpportunityRef)), hintOf(HintEmailThread, p.ThreadID)...)
	act.bodyText = p.BodyText
	act.summary = summarize(withDetail(emailSummaryHead(activityType, participants), p.Subject))
	return act, nil
}

func emailType(key string, p emailPayload) (string, error) {
	switch p.Direction {
	case "inbound":
		if key != "received" {
			return "", invalid("inbound email must use event key \"received\", got %q", key)
		}
		if strings.TrimSpace(deref(p.InReplyTo)) != "" {
			return "EmailReply", nil
		}
		return "EmailReceived", nil
	case "outbound":
		if key != "sent" {
			return "", invalid("outbound email must use event key \"sent\", got %q", key)
		}
		return "EmailSent", nil
	default:
		return "", invalid("email direction %q must be inbound or outbound", p.Direction)
	}
}

func emailParticipants(p emailPayload) ([]Participant, error) {
	var out []Participant
	groups := []struct {
		role  string
		addrs []address
	}{{RoleFrom, []address{p.From}}, {RoleTo, p.To}, {RoleCC, p.CC}}
	for _, g := range groups {
		for _, a := range g.addrs {
			part, err := participantFromAddress(a, g.role)
			if err != nil {
				return nil, err
			}
			out = addParticipant(out, part)
		}
	}
	return out, nil
}

// emailAccountHints: inbound looks at the sender first, outbound only at recipients; later
// To/CC domains follow as fallbacks (resolution tries them in order). Our own domain is never
// a hint. Core trusts the envelope's addresses: connectors must verify SPF/DKIM first.
func emailAccountHints(p emailPayload, participants []Participant) []Hint {
	var ordered []string
	roles := []string{RoleFrom, RoleTo, RoleCC}
	if p.Direction == "outbound" {
		roles = []string{RoleTo, RoleCC}
	}
	for _, role := range roles {
		for _, part := range participants {
			if part.Role == role {
				ordered = append(ordered, part.RawIdentity)
			}
		}
	}
	return externalDomainHints(ordered...)
}

func emailSummaryHead(activityType string, participants []Participant) string {
	role := RoleFrom
	label := "Email from "
	switch activityType {
	case "EmailReply":
		label = "Reply from "
	case "EmailSent":
		role, label = RoleTo, "Email to "
	}
	for _, part := range participants {
		if part.Role == role {
			return label + part.RawIdentity
		}
	}
	return strings.TrimSpace(label)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
