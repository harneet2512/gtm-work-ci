package demomine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// StateRef, Provenance and the other manifest types follow contracts/schemas/demo_manifest.v1.json.
type (
	// StateRef points at one state snapshot: the deal's (opportunity_id set, version >= 1) or the account's.
	StateRef struct {
		AccountID     string  `json:"account_id"`
		OpportunityID *string `json:"opportunity_id,omitempty"`
		Version       int     `json:"version"`
	}
	// SourceRef is the ingest identity of the event's source object.
	SourceRef struct {
		SourceSystem   string `json:"source_system"`
		SourceObjectID string `json:"source_object_id"`
	}
	// EventProvenance is where a replay event came from (origin dataset = the base replay).
	EventProvenance struct {
		Origin         string    `json:"origin"`
		Provenance     string    `json:"provenance"`
		Source         SourceRef `json:"source"`
		SourceEventKey string    `json:"source_event_key"`
	}
	// HeldOutEvent is held_out_event.v1.json: what the event is and nothing it caused.
	HeldOutEvent struct {
		EventID        string          `json:"event_id"`
		Provenance     EventProvenance `json:"provenance"`
		OccurredAt     string          `json:"occurred_at"`
		ReplayPosition int             `json:"replay_position"`
		// PayloadSHA256 pins the event's payload (payloadhash.SHA256 of the as-known payload): set on Event N, which
		// the replay dataset supplies at Play; history events are already in the database and carry none.
		PayloadSHA256 string `json:"payload_sha256,omitempty"`
	}
	// ManifestEvent is one history event with the state it produced.
	ManifestEvent struct {
		Event              HeldOutEvent `json:"event"`
		StateAfter         StateRef     `json:"state_after"`
		StateDiffID        *string      `json:"state_diff_id"`
		IsMaterial         bool         `json:"is_material"`
		MaterialDimensions []string     `json:"material_dimensions"`
	}
	// Expectations is what mining predicted Event N would change; no pipeline stage reads it.
	Expectations struct {
		IsMaterial         bool     `json:"is_material"`
		MaterialDimensions []string `json:"material_dimensions"`
	}
	// MiningInfo is how the case was found.
	MiningInfo struct {
		AccountsScanned int     `json:"accounts_scanned"`
		SequenceScore   float64 `json:"sequence_score"`
		Method          string  `json:"method"`
	}
	// Manifest is demo_manifest.v1.json.
	Manifest struct {
		ID                    string          `json:"id"`
		AccountID             string          `json:"account_id"`
		OpportunityID         string          `json:"opportunity_id"`
		DataCutoff            string          `json:"data_cutoff"`
		Events                []ManifestEvent `json:"events"`
		HeldOutEvent          HeldOutEvent    `json:"held_out_event"`
		WhySelected           string          `json:"why_selected"`
		SelectionExpectations *Expectations   `json:"selection_expectations"`
		Mining                *MiningInfo     `json:"mining"`
		ContentSHA256         string          `json:"content_sha256"`
		CreatedAt             string          `json:"created_at"`
	}
)

// FreezeOptions select the case to freeze.
type FreezeOptions struct {
	Dir           string
	OpportunityID string // Salesforce opportunity id
	HeldOutID     string // EventUUID of Event N
	Rules         *transitions.RuleSet
	Extractor     *ReplayExtractor // must match the miner's, or the history differs from the report
	// Report supplies why_selected, the expectations and the mining block for the case; nil needs Why.
	Report *Report
	Why    string
	Now    time.Time
}

// Frozen is a frozen case: the manifest, its canonical JSON and the replay of the history behind it.
type Frozen struct {
	Manifest Manifest
	JSON     []byte
	// Ingested counts the events materialized into the database (all events before Event N in replay order).
	Ingested int
}

// Freeze materializes the world through Event N-1 into db (an empty database) and freezes the manifest. Event
// N is never ingested: only events that precede it in the replay order are applied, and the database is
// checked for N afterwards. The history is measured by the same World the miner used, so the manifest's
// snapshots are what the mined report scored.
func Freeze(ctx context.Context, db *sql.DB, o FreezeOptions) (Frozen, error) {
	snap, err := crmarena.Load(o.Dir)
	if err != nil {
		return Frozen{}, err
	}
	res, err := crmarena.Build(snap)
	if err != nil {
		return Frozen{}, err
	}
	cut, err := locate(res.Events, o.OpportunityID, o.HeldOutID)
	if err != nil {
		return Frozen{}, err
	}
	if err := RequireEmpty(ctx, db); err != nil {
		return Frozen{}, err
	}
	w, err := NewWorld(ctx, db, res, filepath.Join(o.Dir, "User.json"), o.Rules, extractorOrNil(o.Extractor))
	if err != nil {
		return Frozen{}, err
	}
	var applied []Applied
	for i := 0; i < cut.index; i++ { // strictly before Event N: it is never applied
		ap, err := w.Apply(ctx, res.Events[i])
		if err != nil {
			return Frozen{}, err
		}
		if res.Events[i].ReplayDeal() == o.OpportunityID {
			ap.Record.Position = len(applied) + 1
			applied = append(applied, ap)
		}
	}
	if err := checkNotIngested(ctx, db, res.Events[cut.index]); err != nil {
		return Frozen{}, err
	}
	m, err := buildManifest(o, res.Events[cut.index], applied)
	if err != nil {
		return Frozen{}, err
	}
	raw, err := seal(&m)
	return Frozen{Manifest: m, JSON: raw, Ingested: cut.index}, err
}

type cutPoint struct{ index int } // index of Event N in the replay order

// locate finds the opportunity's events and Event N among them. Event N must have history before it and be
// dated strictly after the event before it (time alone then separates history from Event N).
func locate(events []crmarena.Event, opp, heldOutID string) (cutPoint, error) {
	var mine []int
	for i, e := range events {
		if e.ReplayDeal() == opp {
			mine = append(mine, i)
		}
	}
	if len(mine) == 0 {
		return cutPoint{}, fmt.Errorf("demomine: opportunity %s has no events in the snapshot", opp)
	}
	for k, i := range mine {
		e := events[i]
		if EventUUID(e.Source.SourceSystem, e.Source.SourceObjectID, e.Source.SourceEventKey) != heldOutID {
			continue
		}
		if k == 0 {
			return cutPoint{}, errors.New("demomine: the held-out event is the opportunity's first event: there is no history before it")
		}
		if !events[i].OccurredAt().After(events[mine[k-1]].OccurredAt()) {
			return cutPoint{}, errors.New("demomine: the held-out event is not dated strictly after the event before it")
		}
		return cutPoint{index: i}, nil
	}
	return cutPoint{}, fmt.Errorf("demomine: event %s is not an event of opportunity %s", heldOutID, opp)
}

// checkNotIngested proves Event N did not reach the database.
func checkNotIngested(ctx context.Context, db *sql.DB, e crmarena.Event) error {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM source_events WHERE source_system = $1 AND source_object_id = $2 AND source_event_key = $3`,
		e.Source.SourceSystem, e.Source.SourceObjectID, e.Source.SourceEventKey).Scan(&n)
	if err != nil {
		return fmt.Errorf("demomine: check the held-out event is absent: %w", err)
	}
	if n != 0 {
		return errors.New("demomine: the held-out event is in the database before freezing")
	}
	return nil
}

func buildManifest(o FreezeOptions, held crmarena.Event, applied []Applied) (Manifest, error) {
	if len(applied) == 0 {
		return Manifest{}, errors.New("demomine: no history events")
	}
	last := applied[len(applied)-1]
	m := Manifest{AccountID: last.AccountID, OpportunityID: last.OpportunityID, DataCutoff: last.Record.OccurredAt,
		Events: make([]ManifestEvent, 0, len(applied)), CreatedAt: o.Now.UTC().Format(time.RFC3339)}
	if m.AccountID == "" || m.OpportunityID == "" {
		return Manifest{}, errors.New("demomine: the last history event reached no account or deal")
	}
	for _, ap := range applied {
		me, err := manifestEvent(ap)
		if err != nil {
			return Manifest{}, err
		}
		m.Events = append(m.Events, me)
	}
	known, _, err := held.AsKnown()
	if err != nil {
		return Manifest{}, err
	}
	hr := baseRecord(known)
	hr.Position = len(applied) + 1
	m.HeldOutEvent = heldOut(hr)
	if m.HeldOutEvent.PayloadSHA256, err = payloadhash.SHA256(known.Source.Payload); err != nil {
		return Manifest{}, fmt.Errorf("demomine: pin the held-out event's payload: %w", err)
	}
	m.ID = manifestID(o, m.HeldOutEvent.EventID)
	return applySelection(m, o)
}

func manifestEvent(ap Applied) (ManifestEvent, error) {
	r := ap.Record
	if !r.Resolved || ap.AccountID == "" {
		return ManifestEvent{}, fmt.Errorf("demomine: history event %s/%s reached no account, so it has no state to snapshot", r.SourceSystem, r.SourceObjectID)
	}
	ref := StateRef{AccountID: ap.AccountID, Version: r.AccountStateVersion}
	if ap.OpportunityID != "" && r.OpportunityStateVersion >= 1 {
		opp := ap.OpportunityID
		ref = StateRef{AccountID: ap.AccountID, OpportunityID: &opp, Version: r.OpportunityStateVersion}
	}
	diff := ap.StateDiffID
	return ManifestEvent{Event: heldOut(r), StateAfter: ref, StateDiffID: &diff, IsMaterial: r.IsMaterial(),
		MaterialDimensions: nonNil(r.Dimensions)}, nil
}

func heldOut(r EventRecord) HeldOutEvent {
	return HeldOutEvent{EventID: r.EventID, OccurredAt: r.OccurredAt, ReplayPosition: r.Position,
		Provenance: EventProvenance{Origin: r.Origin, Provenance: r.Provenance,
			Source: SourceRef{SourceSystem: r.SourceSystem, SourceObjectID: r.SourceObjectID}, SourceEventKey: r.SourceEventKey}}
}

// applySelection fills why_selected, the expectations and the mining block from the mined report, and
// refuses a report whose history disagrees with the replay just done.
func applySelection(m Manifest, o FreezeOptions) (Manifest, error) {
	if o.Report != nil {
		if c, ok := o.Report.Find(o.OpportunityID, m.HeldOutEvent.EventID); ok {
			if err := sameHistory(c, m); err != nil {
				return m, err
			}
			m.WhySelected = c.WhySelected
			m.SelectionExpectations = &Expectations{IsMaterial: c.HeldOut.IsMaterial(), MaterialDimensions: nonNil(c.HeldOut.Dimensions)}
			m.Mining = &MiningInfo{AccountsScanned: max(o.Report.Counts.AccountsScanned, 1), SequenceScore: c.Score.Total,
				Method: "ghostctl mine-demo-cases, scoring " + o.Report.Scoring.Version}
			return m, nil
		}
	}
	if o.Why == "" {
		return m, errors.New("demomine: this case is not among the report's ranked candidates: pass --why with the reason it was selected, in terms of the mined transitions")
	}
	m.WhySelected = o.Why
	return m, nil
}

// sameHistory checks the report's history events and dimensions against the replay behind the manifest.
func sameHistory(c Case, m Manifest) error {
	if len(c.History) != len(m.Events) {
		return fmt.Errorf("demomine: the report has %d history events for this case, the replay has %d: the report is stale", len(c.History), len(m.Events))
	}
	for i, e := range c.History {
		got := m.Events[i]
		if e.EventID != got.Event.EventID || fmt.Sprint(e.Dimensions) != fmt.Sprint(got.MaterialDimensions) {
			return fmt.Errorf("demomine: history event %d differs between the report and the replay (snapshot, scoring or detector changed): re-run mine-demo-cases", i+1)
		}
	}
	return nil
}
