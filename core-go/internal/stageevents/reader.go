package stageevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// ErrNotFound: the manifest or run does not exist (404).
var ErrNotFound = errors.New("stageevents: not found")

// notRecorded is the detail of a stage the pipeline never recorded for this run.
const notRecorded = "not recorded: this run did not come from a recorded Play"

// Reader builds progress documents from the stored stage events. It reads; it never writes and never infers a
// stage that has no row.
type Reader struct {
	db  *sql.DB
	clk clock.Clock
}

// NewReader returns a Reader over db. A nil clock selects the wall clock.
func NewReader(db *sql.DB, clk clock.Clock) (*Reader, error) {
	if db == nil {
		return nil, errors.New("stageevents: a database is required")
	}
	if clk == nil {
		clk = clock.Real{}
	}
	return &Reader{db: db, clk: clk}, nil
}

const stageColumns = `seq, stage, status, attempt, started_at, ended_at, refs::text, to_jsonb(eval_result_ids)::text, failure_kind, detail, updated_at`

// Manifest is GET /replay/manifests/{id}/progress: the Play's own stages, plus the decide, evals and cliff
// stages of the run that Play's event triggered.
func (r *Reader) Manifest(ctx context.Context, manifestID string) (Progress, error) {
	if !uuidPattern.MatchString(manifestID) {
		return Progress{}, ErrNotFound
	}
	var account string
	var activity sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT m.account_id::text, p.activity_id::text FROM demo_manifests m
 LEFT JOIN demo_plays p ON p.manifest_id = m.id WHERE m.id = $1::uuid`, manifestID).Scan(&account, &activity)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, fmt.Errorf("stageevents: read manifest %s: %w", manifestID, err)
	}
	var runID string
	if activity.Valid {
		if runID, err = r.runOfActivity(ctx, activity.String); err != nil {
			return Progress{}, err
		}
	}
	return r.build(ctx, "manifest", account, manifestID, runID, false)
}

// Run is GET /runs/{id}/progress. When the run's trigger activity is the activity of a recorded Play the Play's
// stages are included; otherwise the upstream stages are `unknown` (never recorded), not `passed`.
func (r *Reader) Run(ctx context.Context, runID string) (Progress, error) {
	if !uuidPattern.MatchString(runID) {
		return Progress{}, ErrNotFound
	}
	var account string
	var manifest sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT r.account_id::text,
   (SELECT p.manifest_id::text FROM demo_plays p WHERE p.activity_id = ANY (r.trigger_activity_ids) ORDER BY p.started_at LIMIT 1)
 FROM agent_runs r WHERE r.id = $1::uuid`, runID).Scan(&account, &manifest)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, fmt.Errorf("stageevents: read run %s: %w", runID, err)
	}
	return r.build(ctx, "run", account, manifest.String, runID, !manifest.Valid)
}

func (r *Reader) runOfActivity(ctx context.Context, activityID string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id::text FROM agent_runs WHERE trigger_activity_ids @> ARRAY[$1]::uuid[]
 ORDER BY created_at DESC, id LIMIT 1`, activityID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stageevents: find the run of activity %s: %w", activityID, err)
	}
	return id, nil
}

// build merges the manifest-scoped and run-scoped rows. The run's own stages (decide, evals) win over a
// manifest-level row (a Play records those only to say they were skipped); Cliff goes the other way, because
// the Cliff messages are recorded against the Play when it can be resolved. noUpstream marks a run with no Play:
// its upstream stages are unknown, not waiting, because nothing will ever record them.
func (r *Reader) build(ctx context.Context, scope, account, manifestID, runID string, noUpstream bool) (Progress, error) {
	manifestRows, err := r.rows(ctx, `manifest_id = $1::uuid AND run_id IS NULL`, manifestID)
	if err != nil {
		return Progress{}, err
	}
	runRows, err := r.rows(ctx, `run_id = $1::uuid`, runID)
	if err != nil {
		return Progress{}, err
	}
	stages := make([]StageDoc, 0, len(Order))
	for _, s := range Order {
		stages = append(stages, pick(s, manifestRows[s], runRows[s], noUpstream))
	}
	p := Progress{Scope: scope, AccountID: account, Overall: overallOf(stages), Stages: stages, GeneratedAt: r.clk.Now().UTC()}
	if manifestID != "" {
		p.ManifestID = &manifestID
	}
	if scope == "run" {
		p.RunID = &runID
	}
	return p, nil
}

func pick(s Stage, manifestRow, runRow *StageDoc, noUpstream bool) StageDoc {
	first, second := runRow, manifestRow
	if s == Cliff {
		first, second = manifestRow, runRow
	}
	switch {
	case first != nil:
		return *first
	case second != nil:
		return *second
	case noUpstream && (s == Ingest || s == Resolve || s == Graph || s == State):
		d := waitingDoc(s)
		detail := notRecorded
		d.Status, d.Detail = Unknown, &detail
		return d
	default:
		return waitingDoc(s)
	}
}

func waitingDoc(s Stage) StageDoc {
	return StageDoc{Stage: s, Status: Waiting, EvalResultIDs: []string{}}
}

func (r *Reader) rows(ctx context.Context, where, owner string) (map[Stage]*StageDoc, error) {
	out := map[Stage]*StageDoc{}
	if owner == "" {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+stageColumns+` FROM pipeline_stage_events WHERE `+where, owner)
	if err != nil {
		return nil, fmt.Errorf("stageevents: read stage events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanStage(rows)
		if err != nil {
			return nil, err
		}
		d = withDeadline(d, r.clk.Now().UTC())
		out[d.Stage] = &d
	}
	return out, rows.Err()
}

func scanStage(rows *sql.Rows) (StageDoc, error) {
	var d StageDoc
	var seq int64
	var started time.Time
	var ended, updated sql.NullTime
	var refs, ids string
	var kind, detail sql.NullString
	if err := rows.Scan(&seq, &d.Stage, &d.Status, &d.Attempt, &started, &ended, &refs, &ids, &kind, &detail, &updated); err != nil {
		return StageDoc{}, fmt.Errorf("stageevents: scan stage event: %w", err)
	}
	if err := json.Unmarshal([]byte(refs), &d.Refs); err != nil {
		return StageDoc{}, fmt.Errorf("stageevents: decode refs of %s: %w", d.Stage, err)
	}
	if err := json.Unmarshal([]byte(ids), &d.EvalResultIDs); err != nil || d.EvalResultIDs == nil {
		d.EvalResultIDs = []string{}
	}
	started = started.UTC()
	d.Seq, d.StartedAt = &seq, &started
	if ended.Valid {
		e := ended.Time.UTC()
		ms := e.Sub(started).Milliseconds()
		d.EndedAt, d.DurationMS = &e, &ms
	}
	if updated.Valid {
		u := updated.Time.UTC()
		d.UpdatedAt = &u
	}
	if kind.Valid {
		k := FailureKind(kind.String)
		d.FailureKind = &k
	}
	if detail.Valid {
		d.Detail = &detail.String
	}
	return d, nil
}

// overallOf summarizes the stages. A stage that ended `unknown` after an error stopped the pipeline as surely as
// a failure does; an `unknown` that was never recorded (upstream stages of a run with no Play) is not a stop.
func overallOf(stages []StageDoc) Overall {
	started, failed, open := false, false, false
	for _, d := range stages {
		switch d.Status {
		case Waiting:
			open = true
		case Running:
			started, open = true, true
		case Completed, Passed, Warning, Skipped:
			started = true
		case Failed:
			started, failed = true, true
		case Unknown:
			if d.Seq != nil {
				started, failed = true, true
			}
		}
	}
	switch {
	case failed:
		return Broken
	case !started:
		return NotStarted
	case open:
		return InProgress
	default:
		return Complete
	}
}
