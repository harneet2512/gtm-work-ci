package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ExecuteStep runs the run's execute step through ex and records the outcome. It refuses an executor of
// another mode than the run's, and a live run nobody approved.
//
// A dry run is one transaction: the execute step ends "recorded" and the run reaches its terminal status
// "recorded" (ADR-0015), which frees the account for its next run.
//
// A live run is claimed, sent and recorded in three phases so a send is never repeated by a race or a
// retry: (1) one transaction locks the run, checks mode and approval and moves the execute step from
// pending (or failed) to running; (2) the send happens outside any transaction, with the run id as the
// idempotency key; (3) a second transaction records succeeded plus the external effect id and marks the
// run executed, or records failed. A crash between (2) and (3) leaves the step "running": it is never
// retried automatically, because the effect may have been sent; an operator reconciles it.
func ExecuteStep(ctx context.Context, db *sql.DB, ex Executor, runID string, e Effect) (Result, error) {
	e.IdempotencyKey = runID + ":execute"
	if ex.Mode() == DryRun {
		return executeDry(ctx, db, ex, runID, e)
	}
	return executeLive(ctx, db, ex, runID, e)
}

// dryRunnable are the statuses a dry run may be recorded from: a draft exists (drafted) or a human decided to
// go ahead (approved, edited). Anything earlier has nothing to record, awaiting_human has no decision yet, and
// rejected, ignored, cancelled, failed, executed and recorded are final: recording must never overwrite a
// human decision that HAR-97 learns from.
var dryRunnable = map[string]bool{"drafted": true, "approved": true, "edited": true}

func executeDry(ctx context.Context, db *sql.DB, ex Executor, runID string, e Effect) (Result, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("runs: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := recordDry(ctx, tx, ex, runID, e)
	if err != nil {
		return res, err
	}
	return res, commit(tx)
}

// RecordDryRunTx records a dry run's execute step inside the caller's transaction, so a caller that decides
// and records in one unit (HAR-129 send) cannot leave a decision without its recorded effect or the reverse.
// The same rules as ExecuteStep apply; the caller commits. Live runs go through ExecuteStep only.
func RecordDryRunTx(ctx context.Context, tx *sql.Tx, ex Executor, runID string, e Effect) (Result, error) {
	if ex.Mode() != DryRun {
		return Result{}, fmt.Errorf("%w: RecordDryRunTx takes a dry-run executor", ErrModeMismatch)
	}
	e.IdempotencyKey = runID + ":execute"
	return recordDry(ctx, tx, ex, runID, e)
}

func recordDry(ctx context.Context, tx *sql.Tx, ex Executor, runID string, e Effect) (Result, error) {
	status, _, err := lockRun(ctx, tx, runID, DryRun)
	if err != nil {
		return Result{}, err
	}
	if !dryRunnable[status] {
		return Result{}, fmt.Errorf("%w: run %s is %s", ErrNotRecordable, runID, status)
	}
	res, err := ex.Execute(ctx, e)
	if err != nil {
		return res, err
	}
	if err := finishStep(ctx, tx, runID, DryRun, "pending", res); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'recorded', updated_at = now() WHERE id = $1::uuid`, runID); err != nil {
		return res, fmt.Errorf("runs: mark dry run %s recorded: %w", runID, err)
	}
	return res, nil
}

func executeLive(ctx context.Context, db *sql.DB, ex Executor, runID string, e Effect) (Result, error) {
	if err := claimLive(ctx, db, runID); err != nil {
		return Result{}, err
	}
	res, sendErr := ex.Execute(ctx, e)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, errors.Join(sendErr, fmt.Errorf("runs: step of run %s left running after the send: %w", runID, err))
	}
	defer func() { _ = tx.Rollback() }()
	if err := finishStep(ctx, tx, runID, Live, "running", res); err != nil {
		return res, errors.Join(sendErr, err)
	}
	if sendErr == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'executed', updated_at = now() WHERE id = $1::uuid`, runID); err != nil {
			return res, fmt.Errorf("runs: mark run %s executed: %w", runID, err)
		}
	}
	if err := commit(tx); err != nil {
		return res, errors.Join(sendErr, err)
	}
	return res, sendErr
}

// claimLive is phase 1: lock, check, and move the execute step to running, committed before any send.
func claimLive(ctx context.Context, db *sql.DB, runID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("runs: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	status, _, err := lockRun(ctx, tx, runID, Live)
	if err != nil {
		return err
	}
	if status != "approved" && status != "edited" {
		return fmt.Errorf("%w: run %s is %s", ErrNotApproved, runID, status)
	}
	r, err := tx.ExecContext(ctx, `
UPDATE agent_run_steps SET status = 'running', started_at = now(), finished_at = NULL
 WHERE agent_run_id = $1::uuid AND step = 'execute' AND status IN ('pending', 'failed')`, runID)
	if err != nil {
		return fmt.Errorf("runs: claim execute step of %s: %w", runID, err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: run %s (%v)", ErrAlreadyExecuted, runID, err)
	}
	return commit(tx)
}

// lockRun row-locks the run for the transaction and checks its mode.
func lockRun(ctx context.Context, tx *sql.Tx, runID, wantMode string) (status, mode string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT run_mode, status FROM agent_runs WHERE id = $1::uuid FOR UPDATE`, runID).Scan(&mode, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("runs: run %s does not exist", runID)
	}
	if err != nil {
		return "", "", fmt.Errorf("runs: read run %s: %w", runID, err)
	}
	if mode != wantMode {
		return "", "", fmt.Errorf("%w: run %s is %s, executor is %s", ErrModeMismatch, runID, mode, wantMode)
	}
	return status, mode, nil
}

// finishStep records the execute step's outcome, only from the expected prior status.
func finishStep(ctx context.Context, tx *sql.Tx, runID, mode, from string, res Result) error {
	detail, err := json.Marshal(res.Detail)
	if err != nil {
		return fmt.Errorf("runs: encode execute detail: %w", err)
	}
	var effect any
	if res.ExternalEffectID != "" {
		effect = res.ExternalEffectID
	}
	r, err := tx.ExecContext(ctx, `
UPDATE agent_run_steps SET status = $4, detail = $5::jsonb, external_effect_id = $6,
       started_at = COALESCE(started_at, now()), finished_at = now()
 WHERE agent_run_id = $1::uuid AND step = 'execute' AND run_mode = $2 AND status = $3`,
		runID, mode, from, res.Status, string(detail), effect)
	if err != nil {
		return fmt.Errorf("runs: record execute step of %s: %w", runID, err)
	}
	if n, err := r.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("%w: run %s (%v)", ErrAlreadyExecuted, runID, err)
	}
	return nil
}

func commit(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runs: commit: %w", err)
	}
	return nil
}
