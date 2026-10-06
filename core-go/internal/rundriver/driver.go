// Package rundriver is the bounded background worker that drives open AgentRuns through the run orchestrator to
// a published StrategySet (HAR-117 wiring). It lives inside core, never in a request path: the trigger pipeline
// only opens the run, and the driver picks it up.
//
//   - At start it releases the draft-step claims a previous process left behind (a crash cannot hold a run for
//     the claim's lease) and then looks for runs in pending or context_built, which resumes interrupted runs.
//   - It makes at most Concurrency orchestrator calls at a time and never two for one account (the database
//     already allows one open run per account; the driver does not rely on that alone).
//   - It consults the provider breaker before starting any call. With the breaker open nothing is called and
//     every run stays exactly as it was, resumable; the next pass after the breaker leaves the open state
//     continues them.
//   - A transient failure leaves the run resumable and backs that run off for RetryDelay; a permanent failure
//     has already failed the run in the orchestrator, which frees the account.
//   - Cancelling the context stops it cleanly: no new call starts and the call in flight is cancelled and
//     settled by the orchestrator (a resumable run), then Run returns.
//
// Scope decides which open runs are the driver's. The demo default is ScopePlay: only the run of a Play (its
// trigger activity is a demo_plays.activity_id) and any run whose draft step already has an attempt (so a crash
// resumes). A `ghostctl crmarena` import leaves one open placeholder run per account; driving those would cost
// 4+ model calls each and post a Message 2 with no Message 1. ScopeAll drives every open run.
//
// Exactly one core instance should drive runs (GHOST_ORCHESTRATOR=on): startup treats every running draft step
// as abandoned by a dead process.
package rundriver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
)

// Defaults of Options.
const (
	DefaultConcurrency = 1
	DefaultPoll        = 2 * time.Second
	DefaultRetryDelay  = 30 * time.Second
	// DefaultMaxAttempts is how many times one run is handed to the orchestrator before the driver stops
	// resuming it and fails it permanently (GHOST_ORCHESTRATOR_MAX_ATTEMPTS). Every transient failure is
	// deliberately resumable, so without a cap a provider that keeps refusing would be retried forever,
	// four model calls at a time.
	DefaultMaxAttempts = 5
	// candidateLimit bounds how many open runs one pass reads.
	candidateLimit = 50
	// driverLockKey is the advisory-lock key that allows exactly one driver per database. It is a fixed
	// constant shared by every core instance; ctxgraph uses its own key.
	driverLockKey int64 = 0x72756e64726976 // "rundriv"
)

// Scopes of Options.Scope (the values of GHOST_ORCHESTRATOR_SCOPE).
const (
	ScopePlay = "play"
	ScopeAll  = "all"
)

// Runner advances one run (*orchestrator.Service implements it).
type Runner interface {
	Run(ctx context.Context, runID string) (orchestrator.Outcome, error)
}

// Gate says whether model calls are currently paused (*providerbreaker.Breaker implements it; Open does not
// count a rejection, so peeking never disturbs the breaker's own accounting).
type Gate interface{ Open() bool }

// Options configures a Driver.
type Options struct {
	DB          *sql.DB
	Runner      Runner
	Scope       string        // ScopePlay (also the zero value) or ScopeAll
	Breaker     Gate          // nil: no gate (the orchestrator's own breaker guard still refuses calls)
	Concurrency int           // orchestrator calls in flight at once; 0 selects DefaultConcurrency
	Poll        time.Duration // how often to look for open runs; 0 selects DefaultPoll
	RetryDelay  time.Duration // how long a run that failed transiently waits; 0 selects DefaultRetryDelay
	// MaxAttempts is how many times one run may be handed to the orchestrator before the driver fails it
	// permanently and stops resuming it; 0 selects DefaultMaxAttempts.
	MaxAttempts int
	Clock       clock.Clock  // nil: the wall clock (backoff only; the replay clock is the orchestrator's business)
	Logger      *slog.Logger // nil discards logs
}

// Driver is the background worker. Build it with New and run it with Run or Start.
type Driver struct {
	opt  Options
	wake chan struct{}

	mu       sync.Mutex
	inflight map[string]string    // account id -> run id being driven
	retryAt  map[string]time.Time // run id -> not before
	paused   bool                 // the breaker was open on the previous pass (logs the edge once)
}

// New validates the options and applies the defaults.
func New(opt Options) (*Driver, error) {
	switch {
	case opt.DB == nil:
		return nil, errors.New("rundriver: a database is required")
	case opt.Runner == nil:
		return nil, errors.New("rundriver: a runner is required")
	case opt.Concurrency < 0:
		return nil, errors.New("rundriver: concurrency must not be negative")
	case opt.MaxAttempts < 0:
		return nil, errors.New("rundriver: max attempts must not be negative")
	case opt.Poll < 0 || opt.RetryDelay < 0:
		return nil, errors.New("rundriver: poll and retry delay must not be negative")
	}
	switch opt.Scope {
	case "":
		opt.Scope = ScopePlay
	case ScopePlay, ScopeAll:
	default:
		return nil, fmt.Errorf("rundriver: unknown scope %q (want %s or %s)", opt.Scope, ScopePlay, ScopeAll)
	}
	if opt.Concurrency == 0 {
		opt.Concurrency = DefaultConcurrency
	}
	if opt.Poll == 0 {
		opt.Poll = DefaultPoll
	}
	if opt.RetryDelay == 0 {
		opt.RetryDelay = DefaultRetryDelay
	}
	if opt.MaxAttempts == 0 {
		opt.MaxAttempts = DefaultMaxAttempts
	}
	if opt.Clock == nil {
		opt.Clock = clock.Real{}
	}
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Driver{opt: opt, wake: make(chan struct{}, 1), inflight: map[string]string{}, retryAt: map[string]time.Time{}}, nil
}

// Wake asks for a pass now instead of at the next poll. It never blocks.
func (d *Driver) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run drives runs until ctx is cancelled, then waits for the calls in flight and returns nil. An error means it
// could not start (taking the single-driver lock or releasing the abandoned claims failed); the caller decides
// whether that is fatal. While another core instance holds the lock the driver stands by, so two drivers never
// take each other's runs.
func (d *Driver) Run(ctx context.Context) error {
	conn, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	if conn == nil {
		return nil // asked to stop while another core drives this database
	}
	// Closing the connection releases the advisory lock; unlock explicitly first so the pooled connection is
	// not returned still holding it.
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, driverLockKey)
		_ = conn.Close()
	}()
	if n, err := d.releaseAbandoned(ctx); err != nil {
		return err
	} else if n > 0 {
		d.opt.Logger.Info("released run claims left by a previous process", "runs", n)
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, d.opt.Concurrency)
	ticker := time.NewTicker(d.opt.Poll)
	defer ticker.Stop()
	for {
		d.pass(ctx, &wg, slots)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		case <-d.wake:
		}
	}
}

// acquire takes the single-driver advisory lock on a dedicated connection, waiting while another core
// instance holds it. Exactly one driver may touch a database: startup treats every running draft step as
// abandoned by a dead process, so two drivers would take each other's runs. Closing the returned connection
// releases the lock; a nil connection means the context ended while standing by.
//
// The lock is session-scoped, so it is taken on a connection held for the driver's whole life: a pooled
// connection returned still holding it would leak the lock to the next user.
func (d *Driver) acquire(ctx context.Context) (*sql.Conn, error) {
	conn, err := d.opt.DB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("rundriver: single-driver connection: %w", err)
	}
	standingBy := false
	for {
		var got bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, driverLockKey).Scan(&got); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("rundriver: take the single-driver lock: %w", err)
		}
		if got {
			return conn, nil
		}
		if !standingBy {
			d.opt.Logger.Warn("another core instance drives this database; standing by")
			standingBy = true
		}
		select {
		case <-ctx.Done():
			_ = conn.Close()
			return nil, nil
		case <-time.After(d.opt.Poll):
		}
	}
}

// Start runs the driver in the background; the returned function cancels it and waits for it to finish, so the
// database is never closed under a running orchestration.
func (d *Driver) Start(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := d.Run(ctx); err != nil {
			d.opt.Logger.Error("run driver stopped", "error", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// pass starts a call for every open run it may start now.
func (d *Driver) pass(ctx context.Context, wg *sync.WaitGroup, slots chan struct{}) {
	if ctx.Err() != nil || d.gateClosed() {
		return
	}
	open, err := d.openRuns(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.opt.Logger.Error("could not list open runs", "error", err)
		}
		return
	}
	d.forget(open)
	for _, r := range open {
		select {
		case slots <- struct{}{}:
		default:
			return // every slot is busy; the finishing call wakes the driver
		}
		if !d.reserve(r) {
			<-slots
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			d.drive(ctx, r)
			d.Wake()
		}()
	}
}

// gateClosed reports the breaker's open state and logs each change of it once.
func (d *Driver) gateClosed() bool {
	if d.opt.Breaker == nil {
		return false
	}
	open := d.opt.Breaker.Open()
	d.mu.Lock()
	changed := open != d.paused
	d.paused = open
	d.mu.Unlock()
	switch {
	case changed && open:
		d.opt.Logger.Warn("provider breaker is open: no run is driven, open runs stay resumable")
	case changed:
		d.opt.Logger.Info("provider breaker is no longer open: driving open runs again")
	}
	return open
}

type openRun struct {
	ID, AccountID string
	Attempt       int // how many times the run has been claimed by a draft step; 0 when it has none
}

// openRunsSQL lists the runs the orchestrator may run, oldest first. Live runs are not the orchestrator's (I6).
// The scope clause is appended by openRuns; its parameter-free text never carries input. Attempt is the draft
// step's own counter, so the driver can stop resuming a run whose retry budget is spent.
const openRunsSQL = `SELECT r.id::text, r.account_id::text,
       COALESCE((SELECT (s.detail ->> 'attempt')::int FROM agent_run_steps s
                  WHERE s.agent_run_id = r.id AND s.step = 'draft' ORDER BY s.seq DESC LIMIT 1), 0)
  FROM agent_runs r
 WHERE r.status IN ('pending', 'context_built') AND r.run_mode = 'dry_run' AND r.workflow = 'post_interaction_followup'`

// playScopeSQL keeps the play's own run, and any run whose draft step has an attempt already (crash resume).
const playScopeSQL = `
 AND (EXISTS (SELECT 1 FROM demo_plays p WHERE p.activity_id = ANY (r.trigger_activity_ids))
   OR EXISTS (SELECT 1 FROM agent_run_steps s WHERE s.agent_run_id = r.id AND s.step = 'draft' AND s.detail ? 'attempt'))`

const openRunsOrder = ` ORDER BY r.created_at, r.id LIMIT $1`

// openRuns lists the runs the driver may start now, in its scope.
func (d *Driver) openRuns(ctx context.Context) ([]openRun, error) {
	q := openRunsSQL
	if d.opt.Scope == ScopePlay {
		q += playScopeSQL
	}
	rows, err := d.opt.DB.QueryContext(ctx, q+openRunsOrder, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("rundriver: list open runs: %w", err)
	}
	defer rows.Close()
	var out []openRun
	for rows.Next() {
		var r openRun
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Attempt); err != nil {
			return nil, fmt.Errorf("rundriver: scan run: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// forget drops the backoff of runs that are no longer open (published elsewhere, cancelled), so it cannot grow.
func (d *Driver) forget(open []openRun) {
	keep := make(map[string]bool, len(open))
	for _, r := range open {
		keep[r.ID] = true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for id := range d.retryAt {
		if !keep[id] {
			delete(d.retryAt, id)
		}
	}
}

// reserve takes the run's account unless it is busy or the run is backing off.
func (d *Driver) reserve(r openRun) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, busy := d.inflight[r.AccountID]; busy {
		return false
	}
	if until, ok := d.retryAt[r.ID]; ok {
		if d.opt.Clock.Now().Before(until) {
			return false
		}
		delete(d.retryAt, r.ID)
	}
	d.inflight[r.AccountID] = r.ID
	return true
}

func (d *Driver) release(r openRun, retryAfter time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.inflight, r.AccountID)
	if retryAfter > 0 {
		d.retryAt[r.ID] = d.opt.Clock.Now().Add(retryAfter)
	} else {
		delete(d.retryAt, r.ID)
	}
}

// drive makes one orchestrator call and classifies how it ended. A run that has already been claimed
// MaxAttempts times is not handed over again: it is failed permanently instead, which frees the account.
func (d *Driver) drive(ctx context.Context, r openRun) {
	log := d.opt.Logger.With("run_id", r.ID, "account_id", r.AccountID)
	if r.Attempt >= d.opt.MaxAttempts {
		d.release(r, 0)
		if err := d.failExhausted(ctx, r); err != nil {
			log.Error("could not fail a run that spent its retry budget", "error", err)
			return
		}
		log.Error("run failed permanently: retry budget spent", "attempts", r.Attempt)
		return
	}
	out, err := d.opt.Runner.Run(ctx, r.ID)
	switch {
	case err == nil:
		d.release(r, 0)
		log.Info("run published", "strategy_set_id", out.SetID, "episode_id", out.EpisodeID, "newly_published", out.Published)
	case ctx.Err() != nil:
		d.release(r, 0) // shutting down: the orchestrator left the run resumable
		log.Info("run interrupted by shutdown; it stays resumable")
	case errors.Is(err, orchestrator.ErrNotRunnable):
		d.release(r, 0) // it moved on between the list and the claim
	case errors.Is(err, orchestrator.ErrInProgress):
		d.release(r, d.opt.RetryDelay) // another caller owns it; look again later
	case orchestrator.IsPermanent(err):
		d.release(r, 0) // the run is failed and no longer listed
		log.Error("run failed permanently", "error", err)
	default:
		d.release(r, d.opt.RetryDelay)
		log.Warn("run paused after a transient failure; it will be resumed", "error", err, "retry_in", d.opt.RetryDelay)
	}
}

// failExhausted ends a run the driver will not hand to the orchestrator again: it has been claimed
// MaxAttempts times and each attempt left it resumable. Nothing else fails such a run (every transient error
// is deliberately resumable), so without this a provider that keeps refusing would be retried forever. The
// run is marked failed (terminal, so its account is free) and its last draft-step failure is recorded as
// permanent.
func (d *Driver) failExhausted(ctx context.Context, r openRun) error {
	reason := fmt.Sprintf("gave up after %d attempts: each left the run resumable, and the retry budget is spent", r.Attempt)
	tx, err := d.opt.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rundriver: fail an exhausted run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'failed', error = $2, updated_at = now()
 WHERE id = $1::uuid AND status IN ('pending', 'context_built') AND run_mode = 'dry_run'`, r.ID, reason); err != nil {
		return fmt.Errorf("rundriver: fail an exhausted run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_run_steps SET status = 'failed', finished_at = COALESCE(finished_at, now()),
 detail = detail || jsonb_build_object('error', jsonb_build_object('phase', 'retry', 'kind', 'permanent', 'reason', $2::text))
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, r.ID, reason); err != nil {
		return fmt.Errorf("rundriver: record an exhausted run's last failure: %w", err)
	}
	return tx.Commit()
}

// releaseAbandoned marks the draft step of every open run still "running" as failed (transient): no live
// process owns it any more. Without this a crashed process would hold its runs until the claim's lease (the run
// token's life plus a margin) ran out. The orchestrator then claims and continues them like any paused run.
func (d *Driver) releaseAbandoned(ctx context.Context) (int64, error) {
	res, err := d.opt.DB.ExecContext(ctx, `UPDATE agent_run_steps s SET status = 'failed', finished_at = now(),
 detail = s.detail || jsonb_build_object('error', jsonb_build_object('phase', 'restart', 'kind', 'transient',
   'reason', 'interrupted: the process that was generating this run stopped'))
 FROM agent_runs r
 WHERE s.agent_run_id = r.id AND s.step = 'draft' AND s.status = 'running'
   AND r.status IN ('pending', 'context_built') AND r.run_mode = 'dry_run'`)
	if err != nil {
		return 0, fmt.Errorf("rundriver: release abandoned claims: %w", err)
	}
	return res.RowsAffected()
}
