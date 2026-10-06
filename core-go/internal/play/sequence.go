package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// SequenceEvent is one event of a manifest's chronological sequence, in replay order. The held-out event is
// the last one; every other event is history that a replay reset re-releases through the real pipeline.
type SequenceEvent struct {
	EventID       string
	Position      int
	Origin        string
	Provenance    string
	Source        SourceRef
	OccurredAt    time.Time
	PayloadSHA256 string // pinned for the held-out event only
	HeldOut       bool
}

// ErrSequenceInvalid: the manifest's events are not a contiguous chronological sequence 1..N.
var ErrSequenceInvalid = errors.New("play: the manifest's event sequence is invalid")

// Sequence is the ordered events of a manifest and the cursor Play uses.
type Sequence struct {
	ManifestID    string
	AccountID     string
	OpportunityID string
	DataCutoff    time.Time
	ContentSHA256 string
	Events        []SequenceEvent // positions 1..N, held-out last
}

// N is the number of events in the sequence.
func (s Sequence) N() int { return len(s.Events) }

// At returns the event at 1-based position k, and whether it exists.
func (s Sequence) At(k int) (SequenceEvent, bool) {
	if k < 1 || k > len(s.Events) {
		return SequenceEvent{}, false
	}
	return s.Events[k-1], true
}

type sequenceRow struct {
	Event heldOutDoc `json:"event"`
}

// LoadSequence reads the full ordered event sequence of a manifest (history plus the held-out event). Unlike
// LoadManifest it keeps every history event, because a replay reset re-releases them one at a time. The
// positions must be exactly 1..N: a gap or a duplicate is refused, so the episode cursor cannot be ambiguous.
func LoadSequence(ctx context.Context, db rowQuerier, id string) (Sequence, error) {
	if !IsUUID(id) {
		return Sequence{}, ErrManifestNotFound
	}
	var eventsRaw, heldRaw string
	seq := Sequence{ManifestID: id}
	err := db.QueryRowContext(ctx, `
SELECT account_id::text, opportunity_id::text, data_cutoff, content_sha256, events::text, held_out_event::text
  FROM demo_manifests WHERE id = $1::uuid`, id).Scan(&seq.AccountID, &seq.OpportunityID, &seq.DataCutoff, &seq.ContentSHA256, &eventsRaw, &heldRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Sequence{}, ErrManifestNotFound
	}
	if err != nil {
		return Sequence{}, fmt.Errorf("play: read the event sequence of manifest %s: %w", id, err)
	}
	seq.DataCutoff = seq.DataCutoff.UTC()

	var history []sequenceRow
	if err := json.Unmarshal([]byte(eventsRaw), &history); err != nil {
		return Sequence{}, fmt.Errorf("play: decode the events of manifest %s: %w", id, err)
	}
	var held heldOutDoc
	if err := json.Unmarshal([]byte(heldRaw), &held); err != nil {
		return Sequence{}, fmt.Errorf("play: decode the held-out event of manifest %s: %w", id, err)
	}

	out := make([]SequenceEvent, 0, len(history)+1)
	for _, h := range history {
		out = append(out, sequenceEventOf(h.Event, false))
	}
	out = append(out, sequenceEventOf(held, true))
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	for i, ev := range out {
		if ev.Position != i+1 {
			return Sequence{}, fmt.Errorf("%w: manifest %s has position %d at index %d (positions must be 1..N)", ErrSequenceInvalid, id, ev.Position, i)
		}
	}
	seq.Events = out
	return seq, nil
}

func sequenceEventOf(h heldOutDoc, heldOut bool) SequenceEvent {
	return SequenceEvent{
		EventID:       h.EventID,
		Position:      h.Position,
		Origin:        h.Provenance.Origin,
		Provenance:    h.Provenance.Provenance,
		Source:        h.ref(),
		OccurredAt:    h.OccurredAt.UTC(),
		PayloadSHA256: h.PayloadSHA,
		HeldOut:       heldOut,
	}
}
