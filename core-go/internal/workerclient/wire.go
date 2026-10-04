package workerclient

import (
	"encoding/json"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

type wireParticipant struct {
	RawIdentity string  `json:"raw_identity"`
	DisplayName string  `json:"display_name,omitempty"`
	Role        string  `json:"role"`
	PersonID    *string `json:"person_id"`
}

type wireActivity struct {
	ID             string            `json:"id"`
	IdempotencyKey string            `json:"idempotency_key"`
	ActivityType   string            `json:"activity_type"`
	SourceSystem   string            `json:"source_system"`
	SourceObjectID string            `json:"source_object_id"`
	SourceEventID  string            `json:"source_event_id"`
	OccurredAt     time.Time         `json:"occurred_at"`
	IngestedAt     time.Time         `json:"ingested_at"`
	Participants   []wireParticipant `json:"participants"`
	AccountID      *string           `json:"account_id"`
	OpportunityID  *string           `json:"opportunity_id"`
	PayloadRef     string            `json:"payload_ref"`
	Summary        *string           `json:"summary"`
	Permissions    json.RawMessage   `json:"permissions"`
	Provenance     json.RawMessage   `json:"provenance"`
}

type wireRequest struct {
	Activity         wireActivity         `json:"activity"`
	Text             string               `json:"text"`
	KnownPeople      []claims.KnownPerson `json:"known_people,omitempty"`
	ExtractorVersion string               `json:"extractor_version,omitempty"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// newWireRequest maps an extraction request onto the worker's strict request schema.
func newWireRequest(req claims.ExtractRequest) wireRequest {
	a := req.Activity
	parts := make([]wireParticipant, len(a.Participants))
	for i, p := range a.Participants {
		parts[i] = wireParticipant{RawIdentity: p.RawIdentity, DisplayName: p.DisplayName, Role: p.Role, PersonID: optional(p.PersonID)}
	}
	perms := a.Permissions
	if len(perms) == 0 {
		perms = json.RawMessage(`{"visibility":"org"}`)
	}
	prov := a.Provenance
	if len(prov) == 0 {
		prov, _ = json.Marshal(map[string]string{"source_system": a.SourceSystem, "source_object_id": a.SourceObjectID})
	}
	return wireRequest{
		Activity: wireActivity{
			ID: a.ID, IdempotencyKey: a.IdempotencyKey, ActivityType: a.Type, SourceSystem: a.SourceSystem,
			SourceObjectID: a.SourceObjectID, SourceEventID: a.SourceEventID, OccurredAt: a.OccurredAt.UTC(), IngestedAt: a.IngestedAt.UTC(),
			Participants: parts, AccountID: optional(a.AccountID), OpportunityID: optional(a.OpportunityID),
			PayloadRef: "source_events/" + a.SourceEventID, Summary: optional(a.Summary), Permissions: perms, Provenance: prov,
		},
		Text: req.Text, KnownPeople: req.KnownPeople, ExtractorVersion: req.ExtractorVersion,
	}
}
