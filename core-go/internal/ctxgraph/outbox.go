package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// Enqueue reasons recorded on a job.
const (
	ReasonIngest    = "ingest"
	ReasonRecompute = "recompute"
	ReasonKnowledge = "knowledge"
	ReasonManual    = "manual"
)

// ErrLeaseLost: another worker took the job over after its lease expired.
var ErrLeaseLost = errors.New("ctxgraph: projection job lease lost")

const uniqueViolation = "23505"

// Enqueue records that the account's canonical records changed. It must run in the same transaction as
// the Postgres change it reports (the transactional outbox): the job commits with the change or not at
// all. Jobs coalesce: at most one pending job per account; its activity ids and reasons are unioned and
// its enqueued_at stays the oldest, so lag is measured from the first unprojected change.
func Enqueue(ctx context.Context, db claimstore.DB, accountID string, activityIDs []string, reason string, now time.Time) error {
	if accountID == "" {
		return errors.New("ctxgraph: enqueue needs an account id")
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO graph_projection_jobs (account_id, activity_ids, reasons, enqueued_at)
VALUES ($1::uuid, $2::uuid[], $3::text[], $4)
ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE
   SET activity_ids = (SELECT COALESCE(array_agg(DISTINCT x ORDER BY x), '{}') FROM unnest(graph_projection_jobs.activity_ids || EXCLUDED.activity_ids) AS u(x)),
       reasons = (SELECT COALESCE(array_agg(DISTINCT x ORDER BY x), '{}') FROM unnest(graph_projection_jobs.reasons || EXCLUDED.reasons) AS u(x))`,
		accountID, signalstore.UUIDArray(activityIDs), signalstore.UUIDArray([]string{reason}), now)
	if err != nil {
		return fmt.Errorf("ctxgraph: enqueue projection of %s: %w", accountID, err)
	}
	return nil
}

// Job is a claimed projection job.
type Job struct {
	ID          int64
	AccountID   string
	ActivityIDs []string
	EnqueuedAt  time.Time
	Attempts    int
}

const jobColumns = `id, account_id::text, to_jsonb(activity_ids)::text, enqueued_at, attempts`

// claimJob takes a job for worker: first a job whose lease expired (a crashed or failed projection is
// re-run in place, so its recorded diff survives), else the oldest pending job of an account that has
// no live in-flight job. nil means nothing is due.
func claimJob(ctx context.Context, db *sql.DB, worker string, now time.Time, lease time.Duration) (*Job, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// A rebuild holds the exclusive side of this lock while it wipes and re-projects: nothing is claimed meanwhile.
	var free bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock_shared($1)`, rebuildLockKey).Scan(&free); err != nil {
		return nil, fmt.Errorf("ctxgraph: rebuild lock: %w", err)
	}
	if !free {
		return nil, nil
	}
	job, err := claimIn(ctx, tx, worker, now, lease)
	if err != nil || job == nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ctxgraph: commit claim: %w", err)
	}
	return job, nil
}

// rebuildLockKey is the advisory lock a rebuild holds exclusively and claimJob takes shared.
const rebuildLockKey int64 = 7261001

func claimIn(ctx context.Context, tx *sql.Tx, worker string, now time.Time, lease time.Duration) (*Job, error) {
	expires := now.Add(lease)
	row := tx.QueryRowContext(ctx, `
UPDATE graph_projection_jobs SET claimed_by = $2, lease_expires_at = $3, attempts = attempts + 1
 WHERE id = (SELECT id FROM graph_projection_jobs WHERE claimed_at IS NOT NULL AND completed_at IS NULL AND lease_expires_at <= $1
              ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
RETURNING `+jobColumns, now, worker, expires)
	job, err := scanJob(row)
	if err != nil || job != nil {
		return job, err
	}
	row = tx.QueryRowContext(ctx, `
UPDATE graph_projection_jobs SET claimed_at = $1, claimed_by = $2, lease_expires_at = $3, attempts = attempts + 1
 WHERE id = (SELECT j.id FROM graph_projection_jobs j
              WHERE j.claimed_at IS NULL
                AND NOT EXISTS (SELECT 1 FROM graph_projection_jobs f WHERE f.account_id = j.account_id AND f.claimed_at IS NOT NULL AND f.completed_at IS NULL)
              ORDER BY j.enqueued_at, j.id FOR UPDATE OF j SKIP LOCKED LIMIT 1)
RETURNING `+jobColumns, now, worker, expires)
	job, err = scanJob(row)
	if isUniqueViolation(err) {
		return nil, nil // another worker took the account's in-flight slot first
	}
	return job, err
}

func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	var acts string
	err := row.Scan(&j.ID, &j.AccountID, &acts, &j.EnqueuedAt, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: claim projection job: %w", err)
	}
	ids, err := decodeStrings(acts)
	if err != nil {
		return nil, err
	}
	j.ActivityIDs = ids
	return &j, nil
}

// failJob records the error and lets the lease run out after delay; the same job is then retried.
func failJob(ctx context.Context, db *sql.DB, job Job, worker string, cause error, now time.Time, delay time.Duration) error {
	_, err := db.ExecContext(ctx, `
UPDATE graph_projection_jobs SET last_error = $3, lease_expires_at = $4 WHERE id = $1 AND claimed_by = $2 AND completed_at IS NULL`,
		job.ID, worker, clip(cause.Error(), 1000), now.Add(delay))
	if err != nil {
		return fmt.Errorf("ctxgraph: record failure of job %d: %w", job.ID, err)
	}
	return nil
}

// completeJob marks the job done and writes the account's checkpoint, in one transaction.
func completeJob(ctx context.Context, tx *sql.Tx, job Job, worker string, now time.Time, cp Checkpoint) error {
	res, err := tx.ExecContext(ctx, `
UPDATE graph_projection_jobs SET completed_at = $3, last_error = NULL WHERE id = $1 AND claimed_by = $2 AND completed_at IS NULL`, job.ID, worker, now)
	if err != nil {
		return fmt.Errorf("ctxgraph: complete job %d: %w", job.ID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	return upsertCheckpoint(ctx, tx, job.AccountID, &job.ID, now, cp)
}

// upsertCheckpoint stores what the graph holds for the account after a projection. jobID is nil for a
// projection outside the outbox (rebuild).
func upsertCheckpoint(ctx context.Context, tx *sql.Tx, accountID string, jobID *int64, now time.Time, cp Checkpoint) error {
	skipped, err := encodeCounts(cp.Skipped)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO graph_projection_checkpoints (account_id, last_job_id, projected_at, node_count, edge_count, snapshot_hash, skipped)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::jsonb)
ON CONFLICT (account_id) DO UPDATE SET last_job_id = EXCLUDED.last_job_id, projected_at = EXCLUDED.projected_at,
       node_count = EXCLUDED.node_count, edge_count = EXCLUDED.edge_count, snapshot_hash = EXCLUDED.snapshot_hash, skipped = EXCLUDED.skipped`,
		accountID, jobID, now, cp.Nodes, cp.Edges, cp.Digest, skipped)
	if err != nil {
		return fmt.Errorf("ctxgraph: write checkpoint of %s: %w", accountID, err)
	}
	return nil
}

// Checkpoint is what the graph held for an account after its latest completed projection.
type Checkpoint struct {
	AccountID string
	Nodes     int
	Edges     int
	Digest    string
	Skipped   map[string]int
	At        time.Time
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
