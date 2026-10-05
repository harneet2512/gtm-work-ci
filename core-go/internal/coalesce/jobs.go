package coalesce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Job is a claimed recompute_jobs row.
type Job struct {
	ID          int64
	AccountID   string
	ActivityIDs []string
	Attempts    int
}

const (
	uniqueViolation = "23505"
	// maxErrorTextRunes bounds the error text stored in last_error and quarantined_activities.reason.
	maxErrorTextRunes = 1000
)

// Claim takes the next due job whose account has no live in-flight job, or returns nil when there
// is none. Expired leases are reclaimed first (a crashed worker's job is merged into the account's
// pending job, or becomes pending again), and parked jobs are folded into a pending job when new
// activity arrived.
func (s *Service) Claim(ctx context.Context) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("coalesce: begin claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.clk.Now()
	if err := reclaim(ctx, tx, now, s.parkRetry); err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, pickDueJob, now).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if cerr := tx.Commit(); cerr != nil {
				return nil, fmt.Errorf("coalesce: commit empty claim: %w", cerr)
			}
			return nil, nil
		}
		return nil, fmt.Errorf("coalesce: pick job: %w", err)
	}
	job := Job{ID: id}
	var acts []string
	err = tx.QueryRowContext(ctx, takeJob, id, now, s.worker, now.Add(s.lease)).Scan(&job.AccountID, (*pgTextArray)(&acts), &job.Attempts)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, nil // another worker took the account's in-flight slot first
		}
		return nil, fmt.Errorf("coalesce: claim job %d: %w", id, err)
	}
	job.ActivityIDs = acts
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("coalesce: commit claim: %w", err)
	}
	return &job, nil
}

const pickDueJob = `
SELECT j.id FROM recompute_jobs j
 WHERE j.claimed_at IS NULL AND j.due_at <= $1
   AND NOT EXISTS (SELECT 1 FROM recompute_jobs f WHERE f.account_id = j.account_id AND f.claimed_at IS NOT NULL)
 ORDER BY j.due_at, j.id
 FOR UPDATE OF j SKIP LOCKED
 LIMIT 1`

const takeJob = `
UPDATE recompute_jobs SET claimed_at = $2, claimed_by = $3, lease_expires_at = $4, attempts = attempts + 1
 WHERE id = $1
RETURNING account_id::text, activity_ids::text[], attempts`

// reclaimJobs frees, in one statement, every in-flight job whose lease ran out and folds parked jobs
// into a newer pending job. A reclaimable job is merged into its account's pending job (and deleted)
// or, with no pending job, becomes pending again; a parked job without a pending job is left alone.
var reclaimJobs = `
WITH reclaimable AS (
    SELECT id, account_id, activity_ids, parked_at FROM recompute_jobs
     WHERE claimed_at IS NOT NULL AND (lease_expires_at <= $1::timestamptz OR parked_at IS NOT NULL)
     FOR UPDATE SKIP LOCKED),
merged AS (
    UPDATE recompute_jobs p
       SET activity_ids = ` + unionIDs("p.activity_ids", "r.activity_ids") + `
      FROM reclaimable r
     WHERE p.account_id = r.account_id AND p.claimed_at IS NULL
    RETURNING r.id AS reclaimed_id),
removed AS (
    DELETE FROM recompute_jobs WHERE id IN (SELECT reclaimed_id FROM merged) RETURNING id)
UPDATE recompute_jobs j
   SET claimed_at = NULL, claimed_by = NULL, lease_expires_at = NULL,
       last_error = CASE WHEN j.parked_at IS NULL THEN 'lease expired' ELSE j.last_error END,
       attempts = CASE WHEN j.parked_at IS NULL THEN j.attempts ELSE 0 END,
       parked_at = NULL
 WHERE j.id IN (SELECT id FROM reclaimable WHERE parked_at IS NULL OR parked_at <= $1::timestamptz - $2::bigint * interval '1 microsecond')
   AND j.id NOT IN (SELECT reclaimed_id FROM merged)`

// unionIDs is the SQL for the distinct, ordered union of two uuid[] expressions.
func unionIDs(a, b string) string {
	return "(SELECT array_agg(DISTINCT x ORDER BY x) FROM unnest(" + a + " || " + b + ") AS u(x))"
}

func reclaim(ctx context.Context, tx *sql.Tx, now time.Time, parkRetry time.Duration) error {
	if _, err := tx.ExecContext(ctx, reclaimJobs, now, parkRetry.Microseconds()); err != nil {
		return fmt.Errorf("coalesce: reclaim expired and parked jobs: %w", err)
	}
	return nil
}

// mergeIntoPending adds activity ids to the account's pending job, if there is one.
func mergeIntoPending(ctx context.Context, tx *sql.Tx, account string, acts []string) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM recompute_jobs WHERE account_id = $1::uuid AND claimed_at IS NULL FOR UPDATE`, account).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("coalesce: find pending job: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE recompute_jobs SET activity_ids = `+unionIDs("activity_ids", "$2::uuid[]")+` WHERE id = $1`,
		id, pgTextArray(acts))
	if err != nil {
		return false, fmt.Errorf("coalesce: merge into pending job %d: %w", id, err)
	}
	return true, nil
}

// Release hands a failed job back. With attempts left it becomes pending again after the retry delay
// with the error recorded; out of attempts it is parked (parked_at). If ingest created a pending job
// meanwhile the failed job's activities merge into it instead (and that fresh job's attempt count
// governs, so new activity un-parks the account).
func (s *Service) Release(ctx context.Context, job Job, cause error) error {
	return s.release(ctx, job, cause, false)
}

// Renew extends the in-flight job's lease to now+lease (a heartbeat). A job runs its model calls one
// after another, so without it two slow calls can outlast the lease and the job is re-claimed and
// paid for twice. It returns ErrLeaseLost when the job is no longer ours.
func (s *Service) Renew(ctx context.Context, job Job) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE recompute_jobs SET lease_expires_at = $3 WHERE id = $1 AND claimed_by = $2 AND claimed_at IS NOT NULL AND parked_at IS NULL`,
		job.ID, s.worker, s.clk.Now().Add(s.lease))
	if err != nil {
		return fmt.Errorf("coalesce: renew lease of job %d: %w", job.ID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	return nil
}

// release is Release; parkNow parks the job whatever its attempt count (a non-retryable provider error:
// every retry would fail, and bill, the same way).
func (s *Service) release(ctx context.Context, job Job, cause error, parkNow bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("coalesce: begin release: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	merged, err := mergeIntoPending(ctx, tx, job.AccountID, job.ActivityIDs)
	if err != nil {
		return err
	}
	reason := truncate(cause.Error(), maxErrorTextRunes)
	now := s.clk.Now()
	switch {
	case merged:
		_, err = tx.ExecContext(ctx, `DELETE FROM recompute_jobs WHERE id = $1 AND claimed_by = $2`, job.ID, s.worker)
	case parkNow || job.Attempts >= s.maxTries:
		_, err = tx.ExecContext(ctx, `
UPDATE recompute_jobs SET parked_at = $3, lease_expires_at = 'infinity', last_error = $4 WHERE id = $1 AND claimed_by = $2`,
			job.ID, s.worker, now, reason)
	default:
		_, err = tx.ExecContext(ctx, `
UPDATE recompute_jobs SET claimed_at = NULL, claimed_by = NULL, lease_expires_at = NULL, last_error = $3,
       due_at = GREATEST($4::timestamptz, first_enqueued_at)
 WHERE id = $1 AND claimed_by = $2`, job.ID, s.worker, reason, now.Add(s.retryDelay))
	}
	if err != nil {
		return fmt.Errorf("coalesce: release job %d: %w", job.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("coalesce: commit release of job %d: %w", job.ID, err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
