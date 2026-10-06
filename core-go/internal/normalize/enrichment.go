package normalize

import (
	"strings"
	"time"
)

type enrichmentSubject struct {
	Email  *string `json:"email"`
	Domain *string `json:"domain"`
}

type enrichmentPayload struct {
	Kind       string            `json:"kind"`
	Provider   string            `json:"provider"`
	Subject    enrichmentSubject `json:"subject"`
	ObservedAt time.Time         `json:"observed_at"`
	Facts      map[string]any    `json:"facts"`
}

// normalizeEnrichment implements the EnrichmentUpdated row. The object id has the shape
// "<provider>:<email or domain>:<observed_at>"; only the provider prefix is verified because
// connectors format the other parts themselves.
func normalizeEnrichment(ev SourceEvent) (Activity, error) {
	if ev.SourceEventKey != "observed" {
		return Activity{}, unsupported("enrichment event key %q has no mapping (want observed)", ev.SourceEventKey)
	}
	p, err := decodePayload[enrichmentPayload](ev, "enrichment")
	if err != nil {
		return Activity{}, err
	}
	if strings.TrimSpace(p.Provider) == "" || p.ObservedAt.IsZero() || p.Facts == nil {
		return Activity{}, invalid("enrichment requires provider, observed_at and facts")
	}
	if !strings.HasPrefix(ev.SourceObjectID, p.Provider+":") {
		return Activity{}, invalid("source_object_id %q must start with \"%s:\"", ev.SourceObjectID, p.Provider)
	}
	email, domain, err := enrichmentSubjectIdentity(p.Subject)
	if err != nil {
		return Activity{}, err
	}

	label := email
	if label == "" {
		label = domain
	}
	act := newActivity(ev, "EnrichmentUpdated", p.ObservedAt)
	if email != "" {
		act.participants = []Participant{{RawIdentity: email, Role: RoleMentioned}}
	}
	for _, d := range []string{domain, domainOf(email)} {
		if d != "" && !isInternalDomain(d) {
			act.accountHints = domainHint(d)
			break
		}
	}
	act.summary = summarize("Enrichment from " + p.Provider + " for " + label)
	return act, nil
}

// enrichmentSubjectIdentity validates and normalizes the subject; at least one of email and
// domain must be present.
func enrichmentSubjectIdentity(s enrichmentSubject) (email, domain string, err error) {
	if raw := strings.TrimSpace(deref(s.Email)); raw != "" {
		norm, ok := normalizeEmailAddress(raw)
		if !ok {
			return "", "", invalid("enrichment subject email %q is not valid", raw)
		}
		email = norm
	}
	if raw := strings.ToLower(strings.TrimSpace(deref(s.Domain))); raw != "" {
		ascii, ok := asciiDomain(raw)
		if !ok {
			return "", "", invalid("enrichment subject domain %q is not valid", raw)
		}
		domain = ascii
	}
	if email == "" && domain == "" {
		return "", "", invalid("enrichment subject needs an email or a domain")
	}
	return email, domain, nil
}
