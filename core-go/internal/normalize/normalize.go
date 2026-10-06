package normalize

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/dedupe"
)

// Envelope limits from contracts/schemas/source_event.v1.json.
const (
	maxObjectIDLen = 512
	maxEventKeyLen = 256
	maxConnectorLn = 128
	maxVersionLen  = 64
)

// knownSystems is common.v1.json#sourceSystem.
var knownSystems = map[string]struct{}{
	"crm": {}, "email": {}, "calendar": {}, "call": {}, "slack": {}, "sales_engagement": {},
	"marketing": {}, "product": {}, "support": {}, "docs": {}, "enrichment": {}, "agent": {},
	"human": {}, "ghost.clock": {},
}

// Normalize maps one SourceEvent onto a canonical Activity (contracts/normalization.md).
// It never performs I/O. Every input problem is returned as a *ValidationError.
func Normalize(ev SourceEvent) (Activity, error) {
	if err := validateEnvelope(ev); err != nil {
		return Activity{}, err
	}
	switch ev.SourceSystem {
	case "email":
		return normalizeEmail(ev)
	case "calendar":
		return normalizeCalendar(ev)
	case "call":
		return normalizeCall(ev)
	case "crm":
		return normalizeCRM(ev)
	case "slack":
		return normalizeSlack(ev)
	case "enrichment":
		return normalizeEnrichment(ev)
	case "docs":
		return normalizeDocument(ev)
	case "ghost.clock":
		return normalizeClock(ev)
	case "support":
		return normalizeSupport(ev)
	default:
		return Activity{}, unsupported("source_system %q has no normalizer", ev.SourceSystem)
	}
}

func validateEnvelope(ev SourceEvent) error {
	if _, ok := knownSystems[ev.SourceSystem]; !ok {
		return invalid("source_system %q is not a known source system", ev.SourceSystem)
	}
	if n := utf8.RuneCountInString(ev.SourceObjectID); n < 1 || n > maxObjectIDLen {
		return invalid("source_object_id must be 1-%d characters", maxObjectIDLen)
	}
	if n := utf8.RuneCountInString(ev.SourceEventKey); n < 1 || n > maxEventKeyLen {
		return invalid("source_event_key must be 1-%d characters", maxEventKeyLen)
	}
	if utf8.RuneCountInString(ev.Connector) > maxConnectorLn {
		return invalid("connector must be at most %d characters", maxConnectorLn)
	}
	if utf8.RuneCountInString(ev.ConnectorVersion) > maxVersionLen {
		return invalid("connector_version must be at most %d characters", maxVersionLen)
	}
	if err := validateOrigin(ev.Origin, ev.Provenance); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(ev.Payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return invalid("payload must be a JSON object")
	}
	if !json.Valid(trimmed) {
		return invalid("payload is not valid JSON")
	}
	if containsEscapedNUL(trimmed) {
		return invalid("payload contains a NUL character, which cannot be stored")
	}
	return nil
}

// decodePayload strictly decodes ev.Payload into T after checking payload.kind. Unknown
// fields are rejected because every payload schema sets additionalProperties:false.
func decodePayload[T any](ev SourceEvent, kind string) (T, error) {
	var out T
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(ev.Payload, &probe); err != nil {
		return out, invalid("payload: %v", err)
	}
	if probe.Kind != kind {
		return out, invalid("payload.kind is %q, source_system %q requires %q", probe.Kind, ev.SourceSystem, kind)
	}
	dec := json.NewDecoder(bytes.NewReader(ev.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return out, invalid("payload: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return out, invalid("payload has trailing data")
	}
	return out, nil
}

// newActivity fills the fields every activity shares; callers add the rest.
func newActivity(ev SourceEvent, activityType string, occurredAt time.Time) Activity {
	return Activity{
		idempotencyKey: dedupe.IdempotencyKey(ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey),
		activityType:   activityType,
		sourceSystem:   ev.SourceSystem,
		sourceObjectID: ev.SourceObjectID,
		sourceEventKey: ev.SourceEventKey,
		occurredAt:     occurredAt.UTC(),
		participants:   []Participant{},
		permissions:    Permissions{Visibility: DefaultVisibility},
		provenance: Provenance{
			SourceSystem:     ev.SourceSystem,
			SourceObjectID:   ev.SourceObjectID,
			Connector:        ev.Connector,
			ConnectorVersion: ev.ConnectorVersion,
		},
	}
}

// requireOccurredAt returns SourceEvent.occurred_at for mapping rows that take their time
// from the envelope rather than the payload.
func requireOccurredAt(ev SourceEvent) (time.Time, error) {
	if ev.OccurredAt == nil || ev.OccurredAt.IsZero() {
		return time.Time{}, invalid("occurred_at is required for %s event key %q", ev.SourceSystem, ev.SourceEventKey)
	}
	return ev.OccurredAt.UTC(), nil
}

// requireObjectID checks the envelope id against the id derived from the payload.
func requireObjectID(ev SourceEvent, want string) error {
	if ev.SourceObjectID != want {
		return invalid("source_object_id %q does not match the payload id %q", ev.SourceObjectID, want)
	}
	return nil
}

// summarize collapses whitespace and bounds the length of a summary line.
func summarize(text string) string {
	return truncateRunes(collapseSpace(text), MaxSummaryRunes)
}

// withDetail renders "<head>: <detail>", or just head when detail is blank.
func withDetail(head, detail string) string {
	if d := collapseSpace(detail); d != "" {
		return head + ": " + d
	}
	return head
}
