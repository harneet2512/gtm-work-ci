package graph

import (
	"encoding/json"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// EntityType names a kind of graph node (common.v1.json#entityType). Accounts, people and
// opportunities are tables; activities are rows of activities; documents have no table.
type EntityType string

// Node types.
const (
	EntityAccount     EntityType = "account"
	EntityPerson      EntityType = "person"
	EntityOpportunity EntityType = "opportunity"
	EntityActivity    EntityType = "activity"
	EntityDocument    EntityType = "document"
)

// SourceKey is one identity in a source system: ("email", "priya@acme.com"),
// ("crm", "contact:817"), ("call", "C19:speaker_02"), ("slack", "U02DANA").
type SourceKey struct {
	System string
	Key    string
}

// SourceKeyOf translates a participant's raw identity (see ingest.MappingKey); the zero value
// means the identity has no mapping namespace.
func SourceKeyOf(raw string) SourceKey {
	system, key := ingest.MappingKey(raw)
	return SourceKey{System: system, Key: key}
}

// IsZero reports whether k is not a mappable identity.
func (k SourceKey) IsZero() bool { return k.System == "" }

// Raw is the inverse of SourceKeyOf: the raw identity as activity_participants stores it.
func (k SourceKey) Raw() string {
	if k.System == "email" {
		return k.Key
	}
	return k.System + ":" + k.Key
}

// EmailKey is the SourceKey of an email address.
func EmailKey(address string) SourceKey { return SourceKey{System: "email", Key: address} }

// CRMKey is the SourceKey of a CRM record id.
func CRMKey(record string) SourceKey { return SourceKey{System: "crm", Key: record} }

// evidenceJSON renders provenance details (a cue, a rule, a source file) for the evidence
// columns. The input is always a flat map of strings, which cannot fail to marshal.
func evidenceJSON(fields map[string]string) json.RawMessage {
	if len(fields) == 0 {
		return nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil
	}
	return raw
}
