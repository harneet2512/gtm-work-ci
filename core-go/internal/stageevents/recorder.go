package stageevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// writeTimeout bounds a stage write made after the caller's context may already be gone.
const writeTimeout = 5 * time.Second

// maxDetail is the longest stored detail (the column's CHECK).
const maxDetail = 500

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Recorder writes stage events. Every method is safe on a nil *Recorder (it does nothing), so a pipeline that was
// built without progress recording needs no branches. Callers treat a recording error as non-fatal: the pipeline's
// work must not fail because its bookkeeping did; the stage then stays as it was last written (never invented).
type Recorder struct {
	db  *sql.DB
	clk clock.Clock
}

// NewRecorder returns a Recorder over db. A nil clock selects the wall clock: stage times are real execution times.
func NewRecorder(db *sql.DB, clk clock.Clock) (*Recorder, error) {
	if db == nil {
		return nil, errors.New("stageevents: a database is required")
	}
	if clk == nil {
		clk = clock.Real{}
	}
	return &Recorder{db: db, clk: clk}, nil
}

// Handle is one execution of one stage. A nil or no-op handle (a recorder that is off, or a stage that already
// passed) ignores Finish and Fail.
type Handle struct {
	rec   *Recorder
	id    string
	stage Stage
}

func (h *Handle) active() bool { return h != nil && h.rec != nil && h.id != "" }

func validStage(s Stage) bool {
	for _, o := range Order {
		if o == s {
			return true
		}
	}
	return false
}

func (s Scope) validate() error {
	if !uuidPattern.MatchString(s.AccountID) {
		return errors.New("stageevents: a scope needs an account id")
	}
	if s.ManifestID == "" && s.RunID == "" {
		return errors.New("stageevents: a scope needs a manifest or a run")
	}
	for _, id := range []string{s.ManifestID, s.RunID} {
		if id != "" && !uuidPattern.MatchString(id) {
			return fmt.Errorf("stageevents: %q is not a uuid", id)
		}
	}
	return nil
}

const beginManifest = `
INSERT INTO pipeline_stage_events (manifest_id, account_id, stage, status, attempt, started_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, 'running', 1, $4, $4)
ON CONFLICT (manifest_id, stage) WHERE run_id IS NULL DO UPDATE
   SET status = 'running', attempt = pipeline_stage_events.attempt + 1, started_at = EXCLUDED.started_at, ended_at = NULL,
       refs = '{}'::jsonb, eval_result_ids = '{}', failure_kind = NULL, detail = NULL, updated_at = EXCLUDED.updated_at
 WHERE pipeline_stage_events.status IN ('running', 'failed', 'unknown')
RETURNING id::text`

const beginRun = `
INSERT INTO pipeline_stage_events (run_id, account_id, stage, status, attempt, started_at, updated_at)
VALUES ($1::uuid, $2::uuid, $3, 'running', 1, $4, $4)
ON CONFLICT (run_id, stage) WHERE run_id IS NOT NULL DO UPDATE
   SET status = 'running', attempt = pipeline_stage_events.attempt + 1, started_at = EXCLUDED.started_at, ended_at = NULL,
       refs = '{}'::jsonb, eval_result_ids = '{}', failure_kind = NULL, detail = NULL, updated_at = EXCLUDED.updated_at
 WHERE pipeline_stage_events.status IN ('running', 'failed', 'unknown')
RETURNING id::text`

// Begin records that the stage started (committed at once, so a poller sees it running). A stage that already
// finished (passed, warned or skipped) is not rewritten: an idempotent replay that re-executes it gets a no-op
// handle. A stage that failed, stayed unknown or was left running by a crashed attempt is re-run: its attempt
// counter grows and its previous outcome is cleared.
func (r *Recorder) Begin(ctx context.Context, scope Scope, stage Stage) (*Handle, error) {
	if r == nil {
		return nil, nil
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if !validStage(stage) {
		return nil, fmt.Errorf("stageevents: unknown stage %q", stage)
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	q, owner := beginManifest, scope.ManifestID
	if scope.RunID != "" {
		q, owner = beginRun, scope.RunID
	}
	var id string
	err := r.db.QueryRowContext(ctx, q, owner, scope.AccountID, string(stage), r.clk.Now().UTC()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return &Handle{rec: r, stage: stage}, nil // finished before: nothing to rewrite
	}
	if err != nil {
		return nil, fmt.Errorf("stageevents: begin %s: %w", stage, err)
	}
	return &Handle{rec: r, id: id, stage: stage}, nil
}

// Finish records the stage's end. status is completed (every stage but evals), passed or warning (evals only:
// they are eval verdicts), failed, skipped or unknown. The judging stage (evals) is failed only as a judgment: it needs the failing results, and an execution error there is
// unknown (use Fail).
func (h *Handle) Finish(ctx context.Context, status Status, out Outcome) error {
	if !h.active() {
		return nil
	}
	return h.rec.finish(ctx, h, status, nil, out)
}

// Fail records that the stage did not complete because of err. The kind comes from ClassifyError; the stored
// detail never repeats raw error text except for a contract failure (an operator must act on those). The judging
// stage reports any error as unknown, never failed: a transport problem is not an eval verdict.
func (h *Handle) Fail(ctx context.Context, err error) error { return h.FailWhile(ctx, err, "") }

// FailWhile is Fail with one sentence of context appended to the stored detail, for a stage that spans more than
// one step (the graph stage stays open through the write of the account change, so a failed write must say it
// was the write and not the projection).
func (h *Handle) FailWhile(ctx context.Context, err error, while string) error {
	if !h.active() {
		return nil
	}
	kind := ClassifyError(err)
	status := Failed
	if h.stage == Evals {
		status = Unknown
	}
	detail := failureDetail(kind, err)
	if while != "" {
		detail = truncate(while + ": " + detail)
	}
	return h.rec.finish(ctx, h, status, &kind, Outcome{Detail: detail})
}

func failureDetail(kind FailureKind, err error) string {
	switch kind {
	case Transport:
		return "timed out or a dependency was unreachable; the outcome is not known and a retry may succeed"
	case Contract:
		return truncate(err.Error())
	default:
		return "internal error (see the core log)"
	}
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxDetail {
		return s
	}
	return string(r[:maxDetail-1]) + "…"
}

// Record writes a stage that starts and ends in one step (a skipped stage, a stage derived from stored facts).
func (r *Recorder) Record(ctx context.Context, scope Scope, stage Stage, status Status, out Outcome) error {
	h, err := r.Begin(ctx, scope, stage)
	if err != nil {
		return err
	}
	return h.Finish(ctx, status, out)
}

// MergeRefs adds refs to a finished stage's row: a row the stage produced after it ended (the AccountChange
// that Play writes in its last transaction). Existing keys are overwritten, the status is untouched, and a
// stage with no finished row is an error (a ref is never attached to a stage that did not run).
func (r *Recorder) MergeRefs(ctx context.Context, scope Scope, stage Stage, refs Refs) error {
	if r == nil {
		return nil
	}
	if err := scope.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("stageevents: encode refs: %w", err)
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	q, owner := `UPDATE pipeline_stage_events SET refs = refs || $3::jsonb, updated_at = $4
 WHERE manifest_id = $1::uuid AND run_id IS NULL AND stage = $2 AND status IN ('completed', 'passed', 'warning')`, scope.ManifestID
	if scope.RunID != "" {
		q, owner = `UPDATE pipeline_stage_events SET refs = refs || $3::jsonb, updated_at = $4
 WHERE run_id = $1::uuid AND stage = $2 AND status IN ('completed', 'passed', 'warning')`, scope.RunID
	}
	res, err := r.db.ExecContext(ctx, q, owner, string(stage), string(raw), r.clk.Now().UTC())
	if err != nil {
		return fmt.Errorf("stageevents: merge refs into %s: %w", stage, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("stageevents: %s has no finished row to attach refs to", stage)
	}
	return nil
}

func (r *Recorder) finish(ctx context.Context, h *Handle, status Status, kind *FailureKind, out Outcome) error {
	if status == Waiting || status == Running {
		return fmt.Errorf("stageevents: %s is not an ending status", status)
	}
	if err := checkEnding(h.stage, status); err != nil {
		return err
	}
	if h.stage == Evals && status == Failed && (kind != nil || len(out.EvalResultIDs) == 0) {
		return errors.New("stageevents: the evals stage fails only as a judgment: name the failing results")
	}
	refs, err := json.Marshal(out.Refs)
	if err != nil {
		return fmt.Errorf("stageevents: encode refs: %w", err)
	}
	ids := out.EvalResultIDs
	if ids == nil {
		ids = []string{}
	}
	idsJSON, _ := json.Marshal(ids)
	var k any
	if kind != nil {
		k = string(*kind)
	}
	var detail any
	if out.Detail != "" {
		detail = truncate(out.Detail)
	}
	ctx, cancel := writeContext(ctx)
	defer cancel()
	now := r.clk.Now().UTC()
	res, err := r.db.ExecContext(ctx, `UPDATE pipeline_stage_events
   SET status = $2, ended_at = GREATEST($3::timestamptz, started_at), refs = $4::jsonb,
       eval_result_ids = ARRAY(SELECT jsonb_array_elements_text($5::jsonb))::uuid[], failure_kind = $6, detail = $7, updated_at = $3
 WHERE id = $1::uuid AND status = 'running'`, h.id, string(status), now, string(refs), string(idsJSON), k, detail)
	if err != nil {
		return fmt.Errorf("stageevents: finish %s: %w", h.stage, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("stageevents: %s is no longer running (rows %d, err %v)", h.stage, n, err)
	}
	return nil
}

// checkEnding keeps the vocabulary honest: a verdict status for the judging stage only, "completed" for the rest.
func checkEnding(stage Stage, status Status) error {
	switch {
	case stage == Evals && status == Completed:
		return errors.New("stageevents: evals ends passed, warning, failed or unknown, never completed: it renders verdicts")
	case stage != Evals && (status == Passed || status == Warning):
		return fmt.Errorf("stageevents: %s is not an eval verdict: a %s stage ends completed", status, stage)
	}
	return nil
}

// writeContext detaches the write from the caller's cancellation: a Play that timed out must still record
// where it stopped.
func writeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
}
