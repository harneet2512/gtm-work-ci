package demomine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
)

// miningCooldown makes the trigger judge every event on its own: the default cooldown would hide a later
// eligible event behind an earlier one, and a mined replay has no rep reading the first run.
const miningCooldown = time.Nanosecond

// World is a replay world on one database: the real ingest service, the real coalesced recompute with the
// WP8 pipeline (diff, signals, trigger evaluation, dry-run only) and, when rules are given, the transition
// detector. Events are applied one at a time, each drained before the next, on a clock set to the event's
// own time, so the state after event k is a function of events 1..k alone (no "now" leaks in).
type World struct {
	db       *sql.DB
	clk      *clock.Fixed
	svc      *ingest.Service
	co       *coalesce.Service
	dealUUID map[string]string // Salesforce opportunity id -> database id, learnt as deals appear
}

// Applied is the outcome of one event: the record (positions are set by the caller) and the database ids the
// manifest needs.
type Applied struct {
	Record        EventRecord
	AccountID     string
	OpportunityID string
	StateDiffID   string
}

// NewWorld seeds the seller's reps and prepares the services. userFile is the snapshot's User.json. rules is
// the transition rule set; nil leaves the detector off, as the product does by default. ext supplies the
// LLM-extraction claims (the offline replay worker); nil uses the rule extractors only.
func NewWorld(ctx context.Context, db *sql.DB, res crmarena.Result, userFile string, rules *transitions.RuleSet, ext claims.Extractor) (*World, error) {
	if len(res.Events) == 0 {
		return nil, errors.New("demomine: no events to replay")
	}
	clk := clock.NewFixed(res.Epoch())
	if _, err := graph.SeedCompany(ctx, db, res.Reps.Company(userFile), res.Epoch()); err != nil {
		return nil, err
	}
	svc, err := ingest.NewService(db, ingest.Options{Extension: graph.NewExtension(), Clock: clk})
	if err != nil {
		return nil, err
	}
	hook, err := pipeline.New(pipeline.Options{RunMode: runs.DryRun, Clock: clk, Cooldown: miningCooldown})
	if err != nil {
		return nil, err
	}
	opts := coalesce.Options{Hook: hook, Clock: clk, WorkerID: "demomine"}
	if ext != nil { // the replay worker only: recorded answers, no provider, no key
		opts.Extractor = ext
	}
	if rules != nil {
		opts.Detector = transitionstore.NewDetector(*rules)
	}
	co, err := coalesce.New(db, opts)
	if err != nil {
		return nil, err
	}
	return &World{db: db, clk: clk, svc: svc, co: co, dealUUID: map[string]string{}}, nil
}

// Apply ingests one event as it was known at its time, drains the recompute it causes and measures it.
func (w *World) Apply(ctx context.Context, e crmarena.Event) (Applied, error) {
	known, _, err := e.AsKnown()
	if err != nil {
		return Applied{}, err
	}
	w.clk.Set(known.OccurredAt())
	out := Applied{Record: baseRecord(known)}
	got, err := w.svc.Ingest(ctx, known.Source)
	if err != nil {
		return out, fmt.Errorf("demomine: ingest %s/%s: %w", e.Source.SourceObjectID, e.Source.SourceEventKey, err)
	}
	if got.Duplicate {
		return out, fmt.Errorf("demomine: %s/%s was already ingested: the world must be empty", e.Source.SourceObjectID, e.Source.SourceEventKey)
	}
	if got.AccountID == nil {
		return out, nil
	}
	drained, err := w.co.Drain(ctx)
	if err != nil || drained.Failed != 0 || len(drained.Recomputes) != 1 {
		return out, fmt.Errorf("demomine: recompute after %s/%s: %d recomputes, %d failed: %w",
			e.Source.SourceObjectID, e.Source.SourceEventKey, len(drained.Recomputes), drained.Failed, err)
	}
	out, err = w.measure(ctx, known, got.ActivityID, drained.Recomputes[0], out)
	if err != nil {
		return out, err
	}
	return out, closeRuns(ctx, w.db, out.StateDiffID, w.clk.Now())
}

func baseRecord(e crmarena.Event) EventRecord {
	origin, prov := e.Source.Origin, e.Source.Provenance
	if origin == "" {
		origin, prov = OriginDataset, ProvenanceCRMB2B
	}
	layer := LayerBase
	if strings.HasPrefix(prov, "synthetic:") {
		layer = LayerSynthetic
	}
	return EventRecord{EventID: EventUUID(e.Source.SourceSystem, e.Source.SourceObjectID, e.Source.SourceEventKey),
		SourceSystem: e.Source.SourceSystem, SourceObjectID: e.Source.SourceObjectID, SourceEventKey: e.Source.SourceEventKey,
		OccurredAt: e.OccurredAt().UTC().Format(time.RFC3339), Origin: origin, Provenance: prov, Layer: layer,
		ChangedFields: []string{}, Signals: []string{}, TriggerReasons: []string{}, Dimensions: []string{}, Evidence: []string{}}
}

func (w *World) measure(ctx context.Context, e crmarena.Event, activityID string, rc coalesce.Recompute, out Applied) (Applied, error) {
	rec := out.Record
	rec.Resolved, rec.AccountStateVersion = true, rc.Version
	out.AccountID = rc.AccountID
	opp, err := w.dealOf(ctx, e, activityID)
	if err != nil {
		return out, err
	}
	out.OpportunityID = opp
	obs := Observation{}
	var changes json.RawMessage
	err = w.db.QueryRowContext(ctx, `SELECT id::text, is_material, changes FROM state_diffs WHERE account_id = $1::uuid AND to_version = $2`,
		rc.AccountID, rc.Version).Scan(&out.StateDiffID, &rec.MaterialDiff, &changes)
	if err != nil {
		return out, fmt.Errorf("demomine: read the state diff of %s v%d: %w", rc.AccountID, rc.Version, err)
	}
	obs.RelationshipChanged = relationshipMoved(changes)
	if obs.Signals, err = w.signals(ctx, out.StateDiffID, opp); err != nil {
		return out, err
	}
	if err := w.db.QueryRowContext(ctx, `SELECT eligible FROM trigger_evaluations WHERE state_diff_id = $1::uuid`,
		out.StateDiffID).Scan(&obs.TriggerEligible); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("demomine: read the trigger evaluation: %w", err)
	}
	if obs.TriggerReasons, err = w.reasons(ctx, out.StateDiffID); err != nil {
		return out, err
	}
	if opp != "" {
		if rec.OpportunityStateVersion, obs.DealCreated, obs.ChangedFields, err = w.dealChange(ctx, opp, rc.ActivityIDs); err != nil {
			return out, err
		}
	}
	rec.DealCreated, rec.ChangedFields, rec.Signals = obs.DealCreated, nonNil(obs.ChangedFields), nonNil(obs.Signals)
	rec.RelationshipChanged, rec.TriggerEligible, rec.TriggerReasons = obs.RelationshipChanged, obs.TriggerEligible, nonNil(obs.TriggerReasons)
	rec.Dimensions, rec.Evidence = Classify(obs)
	rec.Dimensions, rec.Evidence = nonNil(rec.Dimensions), nonNil(rec.Evidence)
	out.Record = rec
	return out, nil
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// dealOf is the database id of the deal the event belongs to ("" when it belongs to none, or the deal has
// no activity yet). A deal's own activities carry its id; a contact's creation takes it from the deal that
// introduced the contact.
func (w *World) dealOf(ctx context.Context, e crmarena.Event, activityID string) (string, error) {
	sf := e.ReplayDeal()
	if sf == "" {
		return "", nil
	}
	if id, ok := w.dealUUID[sf]; ok {
		return id, nil
	}
	var id sql.NullString
	if err := w.db.QueryRowContext(ctx, `SELECT opportunity_id::text FROM activities WHERE id = $1::uuid`, activityID).Scan(&id); err != nil {
		return "", fmt.Errorf("demomine: read the deal of activity %s: %w", activityID, err)
	}
	if e.DealID == sf && id.Valid {
		w.dealUUID[sf] = id.String
		return id.String, nil
	}
	return "", nil
}

func relationshipMoved(changes json.RawMessage) bool {
	var list []struct {
		Field    string `json:"field"`
		Material bool   `json:"material"`
	}
	if json.Unmarshal(changes, &list) != nil {
		return false
	}
	for _, c := range list {
		if c.Material && (c.Field == "relationship_state" || c.Field == "open_transition") {
			return true
		}
	}
	return false
}

func (w *World) signals(ctx context.Context, diffID, opp string) ([]string, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT DISTINCT signal_type FROM signals
 WHERE state_diff_id = $1::uuid AND (opportunity_id IS NULL OR opportunity_id::text = $2) ORDER BY 1`, diffID, opp)
	if err != nil {
		return nil, fmt.Errorf("demomine: read signals: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (w *World) reasons(ctx context.Context, diffID string) ([]string, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT DISTINCT unnest(reason_codes) FROM trigger_evaluations WHERE state_diff_id = $1::uuid ORDER BY 1`, diffID)
	if err != nil {
		return nil, fmt.Errorf("demomine: read trigger reasons: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// dealChange reads the deal's two latest state versions: its current version, whether this recompute created
// its first one, and the fields that changed when this recompute wrote a new version.
func (w *World) dealChange(ctx context.Context, opp string, activityIDs []string) (version int, created bool, changed []string, err error) {
	rows, err := w.db.QueryContext(ctx, `SELECT version, state, trigger_activity_ids && $2::uuid[] FROM opportunity_state_history
 WHERE opportunity_id = $1::uuid ORDER BY version DESC LIMIT 2`, opp, signalstore.UUIDArray(activityIDs))
	if err != nil {
		return 0, false, nil, fmt.Errorf("demomine: read deal state history: %w", err)
	}
	defer rows.Close()
	var states [][]byte
	var latestIsNew bool
	for rows.Next() {
		var v int
		var raw []byte
		var touched bool
		if err := rows.Scan(&v, &raw, &touched); err != nil {
			return 0, false, nil, err
		}
		if len(states) == 0 {
			version, latestIsNew = v, touched
		}
		states = append(states, raw)
	}
	if err := rows.Err(); err != nil || len(states) == 0 || !latestIsNew {
		return version, false, nil, err
	}
	if len(states) == 1 {
		return version, true, nil, nil
	}
	changed, err = ChangedFields(states[1], states[0])
	return version, false, changed, err
}
