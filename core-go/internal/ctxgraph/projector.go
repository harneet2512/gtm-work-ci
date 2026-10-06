package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// Defaults for Options.
const (
	DefaultLease      = 5 * time.Minute
	DefaultRetryDelay = 5 * time.Second
	maxDrain          = 1_000_000
)

// Options configures a Projector. The zero value of every field selects a default.
type Options struct {
	Clock      clock.Clock
	WorkerID   string
	Lease      time.Duration
	RetryDelay time.Duration
	Logger     *slog.Logger
}

// Projector is the only writer of the Neo4j projection. It is safe for concurrent use; several
// projectors may share a database (the outbox hands each account to one at a time).
type Projector struct {
	db         *sql.DB
	g          *Graph
	clk        clock.Clock
	worker     string
	lease      time.Duration
	retryDelay time.Duration
	log        *slog.Logger
}

// NewProjector validates the options.
func NewProjector(db *sql.DB, g *Graph, opts Options) (*Projector, error) {
	if db == nil || g == nil {
		return nil, errors.New("ctxgraph: projector needs a database and a graph")
	}
	if opts.Lease < 0 || opts.RetryDelay < 0 {
		return nil, errors.New("ctxgraph: lease and retry delay must not be negative")
	}
	p := &Projector{db: db, g: g, clk: opts.Clock, worker: opts.WorkerID, lease: opts.Lease, retryDelay: opts.RetryDelay, log: opts.Logger}
	if p.clk == nil {
		p.clk = clock.Real{}
	}
	if p.worker == "" {
		host, _ := os.Hostname()
		p.worker = fmt.Sprintf("%s:%d", host, os.Getpid())
	}
	if p.lease == 0 {
		p.lease = DefaultLease
	}
	if p.retryDelay == 0 {
		p.retryDelay = DefaultRetryDelay
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	return p, nil
}

// EnsureSchema creates the Neo4j constraints and indexes (idempotent).
func (p *Projector) EnsureSchema(ctx context.Context) error { return p.g.ensureSchema(ctx) }

// Result describes one projection.
type Result struct {
	AccountID string
	JobID     int64 // 0 when projected outside the outbox (rebuild)
	Nodes     int
	Edges     int
	Digest    string
	Skipped   map[string]int
	Diff      Diff
	Duration  time.Duration
}

// RunOnce claims and projects one job. claimed is false when nothing was due. A failing job keeps its
// claim and is retried after the retry delay; its error is returned with claimed true.
func (p *Projector) RunOnce(ctx context.Context) (res Result, claimed bool, err error) {
	job, err := claimJob(ctx, p.db, p.worker, p.clk.Now(), p.lease)
	if err != nil || job == nil {
		return Result{}, false, err
	}
	res, err = p.project(ctx, job, job.AccountID)
	if err == nil {
		return res, true, nil
	}
	failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if ferr := failJob(failCtx, p.db, *job, p.worker, err, p.clk.Now(), p.retryDelay); ferr != nil {
		err = errors.Join(err, ferr)
	}
	return Result{}, true, fmt.Errorf("ctxgraph: job %d (account %s, attempt %d): %w", job.ID, job.AccountID, job.Attempts, err)
}

// Drain projects jobs until none is due and returns how many it projected. A job that fails is not
// retried within the drain (its retry delay has not elapsed): the first failure is returned.
func (p *Projector) Drain(ctx context.Context) (int, error) {
	for n := 0; n < maxDrain; n++ {
		_, claimed, err := p.RunOnce(ctx)
		if err != nil {
			return n, err
		}
		if !claimed {
			return n, nil
		}
	}
	return maxDrain, errors.New("ctxgraph: drain did not converge")
}

// Run projects jobs until ctx is cancelled, polling every poll when idle. Failures are logged and retried.
func (p *Projector) Run(ctx context.Context, poll time.Duration) error {
	for {
		_, claimed, err := p.RunOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			p.log.ErrorContext(ctx, "graph projection failed", "error", err)
		case claimed:
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// ProjectAccount projects one account outside the outbox (rebuild, repair). It records a checkpoint but
// no job and no diff row.
func (p *Projector) ProjectAccount(ctx context.Context, accountID string) (Result, error) {
	return p.project(ctx, nil, accountID)
}

func (p *Projector) snapshot(ctx context.Context, accountID string) (Snapshot, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("ctxgraph: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return BuildSnapshot(ctx, tx, accountID)
}

// project reads the account's canonical records, diffs them against the graph, records the diff in
// Postgres (so a crash after the graph write cannot lose it), writes only what differs, and completes
// the job with a checkpoint.
func (p *Projector) project(ctx context.Context, job *Job, accountID string) (Result, error) {
	start := p.clk.Now()
	snap, err := p.snapshot(ctx, accountID)
	if err != nil {
		return Result{}, err
	}
	nodes, edges, err := p.g.storedForAccount(ctx, accountID, snap)
	if err != nil {
		return Result{}, err
	}
	diff := computeDiff(snap, nodes, edges)
	if job != nil {
		if err := recordDiff(ctx, p.db, *job, accountID, diff); err != nil {
			return Result{}, err
		}
	}
	if err := p.apply(ctx, snap, diff); err != nil {
		return Result{}, err
	}
	res := Result{AccountID: accountID, Nodes: len(snap.Nodes), Edges: len(snap.Edges), Digest: Digest(snap.Hashes()), Skipped: snap.Skipped, Diff: diff}
	if job != nil {
		res.JobID = job.ID
	}
	if err := p.finish(ctx, job, res); err != nil {
		return Result{}, err
	}
	res.Duration = p.clk.Now().Sub(start)
	p.log.DebugContext(ctx, "graph projected", "account", accountID, "nodes", res.Nodes, "edges", res.Edges, "changes", len(diff.Changes))
	return res, nil
}

// apply writes the changed nodes, then the changed edges, then deletes what Postgres no longer has, all in
// one Neo4j transaction.
func (p *Projector) apply(ctx context.Context, snap Snapshot, diff Diff) error {
	n, e := writeSet(snap, diff)
	nk, ek := removalKeys(diff)
	return p.g.inTx(ctx, func(run exec) error {
		if err := upsertNodes(ctx, run, n); err != nil {
			return err
		}
		if err := upsertEdges(ctx, run, e); err != nil {
			return err
		}
		return deleteStale(ctx, run, nk, ek)
	})
}

func (p *Projector) finish(ctx context.Context, job *Job, res Result) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ctxgraph: begin completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := p.clk.Now()
	cp := Checkpoint{AccountID: res.AccountID, Nodes: res.Nodes, Edges: res.Edges, Digest: res.Digest, Skipped: res.Skipped, At: now}
	if job != nil {
		err = completeJob(ctx, tx, *job, p.worker, now, cp)
	} else {
		err = upsertCheckpoint(ctx, tx, res.AccountID, nil, now, cp)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ctxgraph: commit completion: %w", err)
	}
	return nil
}
