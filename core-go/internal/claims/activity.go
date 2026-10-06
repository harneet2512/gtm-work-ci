package claims

import (
	"context"
	"encoding/json"
	"time"
)

// Participant is one party of an activity with its resolved person (empty when unresolved).
type Participant struct {
	RawIdentity string
	Role        string
	DisplayName string
	PersonID    string
}

// ActivityInput is the slice of an activity (plus its retained source payload) that extraction
// needs. Body is the normalized text quotes must be verbatim substrings of.
type ActivityInput struct {
	ID            string
	AccountID     string
	OpportunityID string
	Type          string
	SourceSystem  string
	OccurredAt    time.Time
	Body          string
	Payload       json.RawMessage
	Participants  []Participant

	// Envelope fields the worker's strict Activity schema requires; unused by the rules.
	SourceEventID  string
	SourceObjectID string
	IdempotencyKey string
	IngestedAt     time.Time
	Summary        string
	Permissions    json.RawMessage // activity.permissions; {"visibility":"org"} when empty
	Provenance     json.RawMessage // activity.provenance; derived from the source when empty
}

// Directory resolves a person from an email address. A false second result means the address
// is unknown (not an error).
type Directory interface {
	PersonIDByEmail(ctx context.Context, email string) (personID string, found bool, err error)
}

// Skip explains why part of a structured change produced no claim.
type Skip struct {
	Reason string
}

// RuleResult is the outcome of deterministic extraction for one activity.
type RuleResult struct {
	Claims  []Claim
	Skipped []Skip
}

// Extractor versions recorded in claims.extractor.
const (
	RuleCRM        = "rule:crm@1"
	RuleCalendar   = "rule:calendar@1"
	RuleEnrichment = "rule:enrichment@1"
)
