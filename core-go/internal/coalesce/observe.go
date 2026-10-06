package coalesce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotQuarantined: Unquarantine named an activity that is not quarantined.
var ErrNotQuarantined = errors.New("activity is not quarantined")

// ParkedJob is a recompute that exhausted its attempts and waits for new activity or an operator.
type ParkedJob struct {
	JobID         int64
	AccountID     string
	AccountName   string
	ActivityCount int
	Attempts      int
	LastError     string
	ParkedAt      time.Time
	Age           time.Duration // since ParkedAt, by the service clock
}

// Parked lists the parked jobs, oldest first.
func (s *Service) Parked(ctx context.Context) ([]ParkedJob, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT j.id, j.account_id::text, a.name, COALESCE(array_length(j.activity_ids, 1), 0), j.attempts, COALESCE(j.last_error, ''), j.parked_at
  FROM recompute_jobs j JOIN accounts a ON a.id = j.account_id
 WHERE j.parked_at IS NOT NULL ORDER BY j.parked_at, j.id`)
	if err != nil {
		return nil, fmt.Errorf("coalesce: list parked jobs: %w", err)
	}
	defer rows.Close()
	now := s.clk.Now()
	var out []ParkedJob
	for rows.Next() {
		var p ParkedJob
		if err := rows.Scan(&p.JobID, &p.AccountID, &p.AccountName, &p.ActivityCount, &p.Attempts, &p.LastError, &p.ParkedAt); err != nil {
			return nil, fmt.Errorf("coalesce: scan parked job: %w", err)
		}
		p.ParkedAt = p.ParkedAt.UTC()
		p.Age = now.Sub(p.ParkedAt)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read parked jobs: %w", err)
	}
	return out, nil
}

// QuarantinedActivity is an activity whose extraction failed permanently (or kept failing until the
// job's last attempt). Its account keeps recomputing without it.
type QuarantinedActivity struct {
	ActivityID    string
	AccountID     string
	Reason        string
	Permanent     bool
	Attempts      int
	QuarantinedAt time.Time
	Age           time.Duration
}

// Quarantined lists the quarantined activities, oldest first.
func (s *Service) Quarantined(ctx context.Context) ([]QuarantinedActivity, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT activity_id::text, account_id::text, reason, permanent, attempts, quarantined_at
  FROM quarantined_activities ORDER BY quarantined_at, activity_id`)
	if err != nil {
		return nil, fmt.Errorf("coalesce: list quarantined activities: %w", err)
	}
	defer rows.Close()
	now := s.clk.Now()
	var out []QuarantinedActivity
	for rows.Next() {
		var q QuarantinedActivity
		if err := rows.Scan(&q.ActivityID, &q.AccountID, &q.Reason, &q.Permanent, &q.Attempts, &q.QuarantinedAt); err != nil {
			return nil, fmt.Errorf("coalesce: scan quarantined activity: %w", err)
		}
		q.QuarantinedAt = q.QuarantinedAt.UTC()
		q.Age = now.Sub(q.QuarantinedAt)
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read quarantined activities: %w", err)
	}
	return out, nil
}

// warnBacklog logs one warning when jobs are parked or activities quarantined, naming the oldest of each
// so an operator can act (ghostctl jobs lists them with accounts and causes).
func (s *Service) warnBacklog(ctx context.Context) {
	parked, err := s.Parked(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "coalesce: cannot list parked jobs", "error", err)
		return
	}
	quarantined, err := s.Quarantined(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "coalesce: cannot list quarantined activities", "error", err)
		return
	}
	if len(parked) == 0 && len(quarantined) == 0 {
		return
	}
	attrs := []any{"parked_jobs", len(parked), "quarantined_activities", len(quarantined)}
	if len(parked) > 0 {
		attrs = append(attrs, "oldest_parked_account", parked[0].AccountID, "oldest_parked_age", parked[0].Age.Round(time.Second).String(), "oldest_parked_error", parked[0].LastError)
	}
	if len(quarantined) > 0 {
		attrs = append(attrs, "oldest_quarantined_activity", quarantined[0].ActivityID, "oldest_quarantined_age", quarantined[0].Age.Round(time.Second).String())
	}
	s.log.WarnContext(ctx, "coalesce: recompute work needs an operator (ghostctl jobs)", attrs...)
}

// quarantine records an activity as excluded from extraction. A repeat is a no-op.
func (s *Service) quarantine(ctx context.Context, job Job, activityID string, cause error, permanent bool) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO quarantined_activities (activity_id, account_id, reason, permanent, attempts, quarantined_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6) ON CONFLICT (activity_id) DO NOTHING`,
		activityID, job.AccountID, truncate(cause.Error(), maxErrorTextRunes), permanent, job.Attempts, s.clk.Now())
	if err != nil {
		return fmt.Errorf("coalesce: quarantine activity %s: %w", activityID, err)
	}
	return nil
}

// quarantinedAmong returns which of the ids are quarantined.
func (s *Service) quarantinedAmong(ctx context.Context, ids []string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT activity_id::text FROM quarantined_activities WHERE activity_id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("coalesce: read quarantine: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("coalesce: scan quarantine: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("coalesce: read rows: %w", err)
	}
	return out, nil
}

// Unquarantine lifts the quarantine of an activity (after the cause was fixed) and enqueues a
// recompute for its account, due immediately.
func (s *Service) Unquarantine(ctx context.Context, activityID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("coalesce: begin unquarantine: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var account string
	err = tx.QueryRowContext(ctx, `DELETE FROM quarantined_activities WHERE activity_id = $1::uuid RETURNING account_id::text`, activityID).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("coalesce: activity %s: %w", activityID, ErrNotQuarantined)
	}
	if err != nil {
		return fmt.Errorf("coalesce: lift quarantine of activity %s: %w", activityID, err)
	}
	now := s.clk.Now()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids) VALUES ($1::uuid, $2, $2, ARRAY[$3::uuid])
ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE SET
    activity_ids = `+unionIDs("recompute_jobs.activity_ids", "EXCLUDED.activity_ids"),
		account, now, activityID); err != nil {
		return fmt.Errorf("coalesce: enqueue recompute after unquarantine: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("coalesce: commit unquarantine of %s: %w", activityID, err)
	}
	return nil
}
