package ctxgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func decodeStrings(raw string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("ctxgraph: decode id list: %w", err)
	}
	return out, nil
}

func encodeCounts(m map[string]int) (string, error) {
	if m == nil {
		m = map[string]int{}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("ctxgraph: encode counts: %w", err)
	}
	return string(raw), nil
}

// Lag is the projection lag metric: how far the graph is behind Postgres.
type Lag struct {
	PendingJobs      int           `json:"pending_jobs"`
	InFlightJobs     int           `json:"in_flight_jobs"`
	FailingJobs      int           `json:"failing_jobs"` // unfinished jobs whose last attempt failed
	OldestUnfinished time.Duration `json:"-"`
	// OldestUnfinishedSeconds is the age of the oldest unprojected change; 0 when the graph is current.
	OldestUnfinishedSeconds float64 `json:"oldest_unfinished_seconds"`
	// LatencyP50Seconds and LatencyP95Seconds are enqueue-to-complete latency over the last 100 projections.
	LatencyP50Seconds float64    `json:"latency_p50_seconds"`
	LatencyP95Seconds float64    `json:"latency_p95_seconds"`
	LastProjectedAt   *time.Time `json:"last_projected_at"`
}

// ReadLag measures the lag as of now.
func ReadLag(ctx context.Context, db *sql.DB, now time.Time) (Lag, error) {
	var l Lag
	var oldest sql.NullFloat64
	var last sql.NullTime
	err := db.QueryRowContext(ctx, `
SELECT count(*) FILTER (WHERE claimed_at IS NULL),
       count(*) FILTER (WHERE claimed_at IS NOT NULL AND completed_at IS NULL),
       count(*) FILTER (WHERE completed_at IS NULL AND last_error IS NOT NULL),
       EXTRACT(EPOCH FROM ($1::timestamptz - min(enqueued_at) FILTER (WHERE completed_at IS NULL))),
       max(completed_at)
  FROM graph_projection_jobs`, now).Scan(&l.PendingJobs, &l.InFlightJobs, &l.FailingJobs, &oldest, &last)
	if err != nil {
		return Lag{}, fmt.Errorf("ctxgraph: read lag: %w", err)
	}
	if oldest.Valid && oldest.Float64 > 0 {
		l.OldestUnfinishedSeconds = oldest.Float64
		l.OldestUnfinished = time.Duration(oldest.Float64 * float64(time.Second))
	}
	if last.Valid {
		t := last.Time
		l.LastProjectedAt = &t
	}
	var p50, p95 sql.NullFloat64
	err = db.QueryRowContext(ctx, `
SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY d), percentile_cont(0.95) WITHIN GROUP (ORDER BY d)
  FROM (SELECT EXTRACT(EPOCH FROM (completed_at - enqueued_at)) AS d FROM graph_projection_jobs
         WHERE completed_at IS NOT NULL ORDER BY id DESC LIMIT 100) recent`).Scan(&p50, &p95)
	if err != nil {
		return Lag{}, fmt.Errorf("ctxgraph: read latency: %w", err)
	}
	l.LatencyP50Seconds, l.LatencyP95Seconds = p50.Float64, p95.Float64
	return l, nil
}

// Barrier answers "is the graph projection of this account complete?". No agent trigger may fire
// before it is: a run reads the graph, so it must read the graph of the world that woke it.
//
// Integration (WP8): pipeline.AfterRecompute creates the AgentRun inside the recompute transaction,
// and the graph job of that recompute is enqueued in the same transaction, so a run never exists
// without an unfinished job. Whatever starts or serves a run (the draft agent's context pulls)
// consults the barrier first. Context pulls do not (they are world-cut and read Postgres, ADR-0019).
type Barrier struct{ db *sql.DB }

// NewBarrier returns a barrier over the outbox.
func NewBarrier(db *sql.DB) *Barrier { return &Barrier{db: db} }

// Complete is true when the account has no unfinished projection job (pending or in flight).
func (b *Barrier) Complete(ctx context.Context, accountID string) (bool, error) {
	var unfinished bool
	err := b.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM graph_projection_jobs WHERE account_id = $1::uuid AND completed_at IS NULL)`, accountID).Scan(&unfinished)
	if err != nil {
		return false, fmt.Errorf("ctxgraph: barrier for %s: %w", accountID, err)
	}
	return !unfinished, nil
}

// Wait blocks until the account's projection is complete, polling every poll, or ctx ends.
func (b *Barrier) Wait(ctx context.Context, accountID string, poll time.Duration) error {
	for {
		done, err := b.Complete(ctx, accountID)
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("ctxgraph: projection of %s not complete: %w", accountID, ctx.Err())
		case <-time.After(poll):
		}
	}
}
