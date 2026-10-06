// Package normalize turns a raw connector SourceEvent into a canonical Activity following
// contracts/normalization.md. Normalization is a pure, deterministic function: no I/O, no
// clock, no LLM.
package normalize

import (
	"encoding/json"
	"time"
)

// Participant roles (contracts common.v1.json#participantRole) used by normalization.
const (
	RoleActor     = "actor"
	RoleFrom      = "from"
	RoleTo        = "to"
	RoleCC        = "cc"
	RoleAttendee  = "attendee"
	RoleOrganizer = "organizer"
	RoleSpeaker   = "speaker"
	RoleMentioned = "mentioned"
)

// DefaultVisibility is the permission every normalized activity starts with.
const DefaultVisibility = "org"

// MaxSummaryRunes bounds Activity.Summary (the activity schema allows 1000; we stay short).
const MaxSummaryRunes = 300

// HintKind names the namespace a hint value belongs to, so that resolution only ever looks
// it up where it can legitimately match (a Slack channel name must never match a CRM id).
type HintKind string

// Hint kinds.
const (
	HintDomain      HintKind = "domain"       // lower-case ASCII (punycode) email/web domain
	HintCRM         HintKind = "crm"          // CRM record id such as account:AC-4 or opp:AC-4-EXP
	HintSlack       HintKind = "slack"        // channel name such as #deal-acme
	HintCalendar    HintKind = "calendar"     // calendar event id (source_object_id of a calendar activity)
	HintEmailThread HintKind = "email_thread" // email thread id; attacker-influenced, never pins an account
)

// Hint is a typed pointer from an activity to the entity it probably belongs to.
type Hint struct {
	Kind  HintKind
	Value string
}

// SourceEvent is the raw connector envelope accepted by POST /ingest
// (contracts/schemas/source_event.v1.json). Payload is retained verbatim.
type SourceEvent struct {
	SourceSystem     string     `json:"source_system"`
	SourceObjectID   string     `json:"source_object_id"`
	SourceEventKey   string     `json:"source_event_key"`
	OccurredAt       *time.Time `json:"occurred_at,omitempty"`
	Connector        string     `json:"connector,omitempty"`
	ConnectorVersion string     `json:"connector_version,omitempty"`
	// Origin and Provenance mark dataset replays and synthetic events (WP32, origin.go). They
	// are stored on source_events for audit only and never reach the Activity.
	Origin     string          `json:"origin,omitempty"`
	Provenance string          `json:"provenance,omitempty"`
	Payload    json.RawMessage `json:"payload"`
}

// Participant is one party of an activity as seen in the source. PersonID is resolved
// later by ingest/WP5, so it is not part of normalization.
type Participant struct {
	RawIdentity string
	DisplayName string
	Role        string
}

// Permissions mirrors common.v1.json#permissions (restricted to what normalization sets).
type Permissions struct {
	Visibility string `json:"visibility"`
}

// Provenance mirrors common.v1.json#provenance.
type Provenance struct {
	SourceSystem     string `json:"source_system"`
	SourceObjectID   string `json:"source_object_id"`
	Connector        string `json:"connector,omitempty"`
	ConnectorVersion string `json:"connector_version,omitempty"`
}

// Activity is the normalized, canonical form of one SourceEvent. It is an immutable value:
// fields are unexported, and accessors return copies of anything mutable.
type Activity struct {
	idempotencyKey   string
	activityType     string
	sourceSystem     string
	sourceObjectID   string
	sourceEventKey   string
	occurredAt       time.Time
	participants     []Participant
	accountHints     []Hint
	opportunityHints []Hint
	summary          string
	bodyText         string
	permissions      Permissions
	provenance       Provenance
}

// IdempotencyKey is dedupe.IdempotencyKey of the source triple.
func (a Activity) IdempotencyKey() string { return a.idempotencyKey }

// Type is the activity type (common.v1.json#activityType).
func (a Activity) Type() string { return a.activityType }

// SourceSystem is the originating system.
func (a Activity) SourceSystem() string { return a.sourceSystem }

// SourceObjectID is the object id in the source system.
func (a Activity) SourceObjectID() string { return a.sourceObjectID }

// SourceEventKey distinguishes events about the same object.
func (a Activity) SourceEventKey() string { return a.sourceEventKey }

// OccurredAt is when the event happened in the world (UTC).
func (a Activity) OccurredAt() time.Time { return a.occurredAt }

// Participants returns a copy of the participant list; never nil.
func (a Activity) Participants() []Participant {
	return append([]Participant{}, a.participants...)
}

// AccountHint is the value of the primary account hint (a domain, CRM account reference,
// channel name or calendar event id); empty when the source gave none. Use AccountHints for
// resolution: the value alone does not say which namespace it belongs to.
func (a Activity) AccountHint() string { return firstValue(a.accountHints) }

// AccountHints lists every account hint in priority order. Never nil.
func (a Activity) AccountHints() []Hint { return append([]Hint{}, a.accountHints...) }

// OpportunityHint is the value of the primary opportunity hint; empty when none.
func (a Activity) OpportunityHint() string { return firstValue(a.opportunityHints) }

// OpportunityHints lists every opportunity hint in priority order. Never nil.
func (a Activity) OpportunityHints() []Hint { return append([]Hint{}, a.opportunityHints...) }

func firstValue(hints []Hint) string {
	if len(hints) == 0 {
		return ""
	}
	return hints[0].Value
}

// Summary is a short deterministic one-line description.
func (a Activity) Summary() string { return a.summary }

// BodyText is the plain text used for extraction and evidence quotes; may be empty.
func (a Activity) BodyText() string { return a.bodyText }

// Permissions are the access rules; defaults to {visibility: org}.
func (a Activity) Permissions() Permissions { return a.permissions }

// Provenance records where the activity came from.
func (a Activity) Provenance() Provenance { return a.provenance }
