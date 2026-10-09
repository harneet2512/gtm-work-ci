// Package play is WP26 (HAR-124): the event-N-invisible runtime assertion and Play, which releases the demo
// manifest's held-out event through the real pipeline and writes the AccountChange and BusinessIntelligenceUpdate
// it caused (HAR-129 section 3).
//
//	manifest --> Invisibility (nothing derived from event N exists, in Postgres, AccountState or Neo4j)
//	         --> Play: record the release, ingest event N, wait for recompute and graph projection,
//	                   build + write the change and its update, complete the record (one transaction)
//
// Play never writes state itself: ingest, recompute, diff, signals, transitions, trigger and graph projection
// are the existing pipeline. It is idempotent: a completed Play is refused with ErrAlreadyReleased and changes
// nothing; an interrupted one resumes where it stopped.
package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID reports whether s is a UUID in canonical text form.
func IsUUID(s string) bool { return uuidPattern.MatchString(s) }

// SourceRef is the idempotency key of the held-out event: where it comes from in the replay dataset.
type SourceRef struct {
	System   string
	ObjectID string
	EventKey string
}

// HeldOut is the manifest's event N (held_out_event.v1.json).
type HeldOut struct {
	EventID    string
	Origin     string
	Provenance string
	Source     SourceRef
	OccurredAt time.Time
	Position   int
	// PayloadSHA256 pins the event's payload in the replay dataset (payloadhash.SHA256); empty in a manifest that
	// predates the pin, which Play refuses.
	PayloadSHA256 string
}

// Manifest is the part of a demo manifest (demo_manifest.v1.json) Play reads.
type Manifest struct {
	ID            string
	AccountID     string
	OpportunityID string
	DataCutoff    time.Time
	ContentSHA256 string
	// LastEventID and LastPosition are the last history event (N-1): the cursor before Play.
	// LastSource is the idempotency key of that event: how its activity is found in the stores.
	LastEventID  string
	LastPosition int
	LastSource   SourceRef
	Held         HeldOut
}

type heldOutDoc struct {
	EventID    string `json:"event_id"`
	Provenance struct {
		Origin     string `json:"origin"`
		Provenance string `json:"provenance"`
		Source     struct {
			System   string `json:"source_system"`
			ObjectID string `json:"source_object_id"`
		} `json:"source"`
		EventKey string `json:"source_event_key"`
	} `json:"provenance"`
	OccurredAt time.Time `json:"occurred_at"`
	Position   int       `json:"replay_position"`
	PayloadSHA string    `json:"payload_sha256"`
}

func (h heldOutDoc) ref() SourceRef {
	return SourceRef{System: h.Provenance.Source.System, ObjectID: h.Provenance.Source.ObjectID, EventKey: h.Provenance.EventKey}
}

// rowQuerier is *sql.DB or *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrManifestNotFound: no such manifest (or not a uuid).
var ErrManifestNotFound = errors.New("play: manifest not found")

// LoadManifest reads one manifest. The held-out event is the only event it returns in full; history events are
// reduced to the cursor Play needs (the last one's id, position and idempotency key).
func LoadManifest(ctx context.Context, db rowQuerier, id string) (Manifest, error) {
	if !IsUUID(id) {
		return Manifest{}, ErrManifestNotFound
	}
	m := Manifest{ID: id}
	var held string
	var last sql.NullString // null when the manifest has no history events
	err := db.QueryRowContext(ctx, `
SELECT account_id::text, opportunity_id::text, data_cutoff, content_sha256, held_out_event::text, (events -> -1 -> 'event')::text
  FROM demo_manifests WHERE id = $1::uuid`, id).Scan(&m.AccountID, &m.OpportunityID, &m.DataCutoff, &m.ContentSHA256, &held, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return Manifest{}, ErrManifestNotFound
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("play: read manifest %s: %w", id, err)
	}
	var h heldOutDoc
	if err := json.Unmarshal([]byte(held), &h); err != nil {
		return Manifest{}, fmt.Errorf("play: decode held-out event of manifest %s: %w", id, err)
	}
	m.DataCutoff = m.DataCutoff.UTC()
	m.Held = HeldOut{EventID: h.EventID, Origin: h.Provenance.Origin, Provenance: h.Provenance.Provenance, Source: h.ref(),
		OccurredAt: h.OccurredAt.UTC(), Position: h.Position, PayloadSHA256: h.PayloadSHA}
	if !last.Valid || last.String == "" || last.String == "null" {
		return m, nil
	}
	var lastDoc heldOutDoc
	if err := json.Unmarshal([]byte(last.String), &lastDoc); err != nil {
		return Manifest{}, fmt.Errorf("play: decode last history event of manifest %s: %w", id, err)
	}
	m.LastEventID, m.LastPosition, m.LastSource = lastDoc.EventID, lastDoc.Position, lastDoc.ref()
	return m, nil
}
