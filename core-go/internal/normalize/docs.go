package normalize

import (
	"strings"
	"time"
)

const keySharedPrefix = "shared:"

type documentPayload struct {
	Kind       string    `json:"kind"`
	DocumentID string    `json:"document_id"`
	Title      string    `json:"title"`
	SharedAt   time.Time `json:"shared_at"`
	SharedBy   string    `json:"shared_by"`
	SharedWith []string  `json:"shared_with"`
}

// normalizeDocument implements the DocumentShared row; the event key is "shared:<shared_at>".
func normalizeDocument(ev SourceEvent) (Activity, error) {
	keyTime, ok := strings.CutPrefix(ev.SourceEventKey, keySharedPrefix)
	if !ok {
		return Activity{}, unsupported("docs event key %q has no mapping (want shared:<shared_at>)", ev.SourceEventKey)
	}
	p, err := decodePayload[documentPayload](ev, "document")
	if err != nil {
		return Activity{}, err
	}
	if err := requireObjectID(ev, p.DocumentID); err != nil {
		return Activity{}, err
	}
	if p.SharedAt.IsZero() {
		return Activity{}, invalid("document requires shared_at")
	}
	if at, err := time.Parse(time.RFC3339, keyTime); err != nil || !at.Equal(p.SharedAt) {
		return Activity{}, invalid("event key time %q does not match shared_at", keyTime)
	}

	sharedBy, ok := normalizeEmailAddress(p.SharedBy)
	if !ok {
		return Activity{}, invalid("shared_by %q is not a valid email", p.SharedBy)
	}
	act := newActivity(ev, "DocumentShared", p.SharedAt)
	act.participants = []Participant{{RawIdentity: sharedBy, Role: RoleActor}}
	recipients := make([]string, 0, len(p.SharedWith)+1)
	for _, raw := range p.SharedWith {
		email, ok := normalizeEmailAddress(raw)
		if !ok {
			return Activity{}, invalid("shared_with %q is not a valid email", raw)
		}
		act.participants = addParticipant(act.participants, Participant{RawIdentity: email, Role: RoleTo})
		recipients = append(recipients, email)
	}
	act.accountHints = domainHint(firstExternalDomain(append(recipients, sharedBy)...))
	act.summary = summarize("Document shared: " + p.Title)
	return act, nil
}
