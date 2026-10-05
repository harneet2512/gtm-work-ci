// Package coalesce runs the coalesced account-state recompute (HAR-96 §4/§15): it claims due
// recompute_jobs (at most one in flight per account, leased, FOR UPDATE SKIP LOCKED), extracts
// claims from the job's activities, adjudicates every claim of the account, folds the winners into a
// new AccountState and persists it with an optimistic version check and a state_history row, all
// in one transaction that also deletes the job. A burst of activities enqueued by ingest inside the
// debounce window is therefore one job and one recompute.
package coalesce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Defaults for Options.
const (
	DefaultLease       = 5 * time.Minute
	DefaultMaxAttempts = 5
	DefaultRetryDelay  = 5 * time.Second
	// DefaultParkRetry is how long a parked job waits before the backoff sweep retries it.
	DefaultParkRetry = 10 * time.Minute
	// DefaultWarnEvery is how often Run logs a warning while jobs are parked or activities quarantined.
	DefaultWarnEvery = 5 * time.Minute
	DefaultOwnDomain = "vendor.example"
	// maxDrainIterations bounds Drain so a misbehaving clock cannot spin it forever.
	maxDrainIterations = 100000
)

// Hook runs inside the recompute transaction after the new state and its history row are written
// and before commit, so a consumer (WP8) can write the state diff and signals atomically with the
// state. prev is nil for an account's first state. conflicts are the lower-standing claims that
// contradict a winner (ADR-0008); WP8 turns each into a field_contradicted signal. A hook error
// rolls the whole recompute back and the job is retried.
type Hook interface {
	AfterRecompute(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
		activityIDs []string, conflicts []claims.Conflict) error
}

// Detector is the transition detector (HAR-126, ADR-0012). It runs inside the recompute transaction before the
// hook, evaluates the transition rules against the new state and the signals recorded so far, writes the
// StateTransition the evidence earns and returns the account's relationship state and open transition, which
// the recompute puts on the state so the hook's diff sees a change of either. A detector error rolls the whole
// recompute back and the job is retried, like a hook error.
type Detector interface {
	Detect(ctx context.Context, tx *sql.Tx, st reducer.AccountState, deals []reducer.OpportunityState, activityIDs []string) (reducer.RelationshipState, *reducer.OpenTransition, error)
}

// HookFunc adapts a function to Hook.
type HookFunc func(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
	activityIDs []string, conflicts []claims.Conflict) error

// AfterRecompute implements Hook.
func (f HookFunc) AfterRecompute(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
	activityIDs []string, conflicts []claims.Conflict) error {
	return f(ctx, tx, prev, next, activityIDs, conflicts)
}

// Gate says whether model calls are paused right now; the provider circuit breaker implements it
// (HAR-135). While Open reports true no job is claimed, so nothing is retried or billed.
type Gate interface{ Open() bool }

// Options configures a Service. The zero value of every field selects a default.
type Options struct {
	// Extractor turns free text into candidates (the worker client); nil disables model extraction.
	Extractor claims.Extractor
	// ExtractorVersion is the worker prompt version (claims.DefaultExtractorVersion when empty).
	ExtractorVersion string
	// Hook defaults to a no-op.
	Hook Hook
	// Detector, when set, runs the transition rules after every recompute (nil disables transitions).
	Detector Detector
	// Clock defaults to the wall clock.
	Clock clock.Clock
	// AdjudicationClock, when set, is the time claims are adjudicated at (expiry, ranking) instead of Clock's.
	// A replay drives it with the world time of the event being folded, so the stored state and the world
	// graph at that time (claimstore.ClaimsAsOf, ADR-0019) adjudicate at the same instant. computed_at, lease
	// and debounce keep using Clock. Nil adjudicates at Clock: the production behaviour.
	AdjudicationClock clock.Clock
	// WorkerID names the claimer in recompute_jobs.claimed_by (host and pid when empty).
	WorkerID string
	// Lease is how long a claimed job is exclusively ours before another worker may reclaim it.
	Lease time.Duration
	// MaxAttempts parks a job (it is never claimed again until new activity merges into it).
	MaxAttempts int
	// RetryDelay is the backoff before a failed job becomes due again.
	RetryDelay time.Duration
	// ParkRetry is how long a parked job rests before it is retried with a fresh attempt budget
	// (a worker outage must not need an operator); new activity un-parks it sooner.
	ParkRetry time.Duration
	// WarnEvery is the interval of the backlog warning Run logs while anything is parked or quarantined.
	WarnEvery time.Duration
	// OwnDomain is our email domain; every other domain is a customer's.
	OwnDomain string
	// Breaker, when set, pauses claiming while the provider circuit breaker is open (HAR-135).
	Breaker Gate
	// Logger receives diagnostics (nil discards). Activity text is never logged.
	Logger *slog.Logger
}

// Service runs recomputes. It is safe for concurrent use; several services may share a database.
type Service struct {
	db         *sql.DB
	clk        clock.Clock
	adjClk     clock.Clock // nil: adjudicate at clk
	extractor  claims.Extractor
	version    string
	hook       Hook
	detector   Detector
	worker     string
	lease      time.Duration
	maxTries   int
	retryDelay time.Duration
	parkRetry  time.Duration
	warnEvery  time.Duration
	ownDomain  string
	log        *slog.Logger
	breaker    Gate
}

// New validates the options and returns a Service on db.
func New(db *sql.DB, opts Options) (*Service, error) {
	if db == nil {
		return nil, errors.New("coalesce: database is required")
	}
	if opts.Lease < 0 || opts.MaxAttempts < 0 || opts.RetryDelay < 0 || opts.ParkRetry < 0 || opts.WarnEvery < 0 {
		return nil, errors.New("coalesce: lease, max attempts and retry delay must not be negative")
	}
	s := &Service{db: db, clk: opts.Clock, extractor: opts.Extractor, version: opts.ExtractorVersion, hook: opts.Hook, detector: opts.Detector, worker: opts.WorkerID,
		lease: opts.Lease, maxTries: opts.MaxAttempts, retryDelay: opts.RetryDelay, parkRetry: opts.ParkRetry, warnEvery: opts.WarnEvery, ownDomain: opts.OwnDomain, log: opts.Logger, breaker: opts.Breaker}
	s.adjClk = opts.AdjudicationClock
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	if s.version == "" {
		s.version = claims.DefaultExtractorVersion
	}
	if s.hook == nil {
		s.hook = HookFunc(func(context.Context, *sql.Tx, *reducer.AccountState, reducer.AccountState, []string, []claims.Conflict) error {
			return nil
		})
	}
	if s.worker == "" {
		host, _ := os.Hostname()
		s.worker = fmt.Sprintf("%s:%d", host, os.Getpid())
	}
	if s.lease == 0 {
		s.lease = DefaultLease
	}
	if s.maxTries == 0 {
		s.maxTries = DefaultMaxAttempts
	}
	if s.retryDelay == 0 {
		s.retryDelay = DefaultRetryDelay
	}
	if s.parkRetry == 0 {
		s.parkRetry = DefaultParkRetry
	}
	if s.warnEvery == 0 {
		s.warnEvery = DefaultWarnEvery
	}
	if s.ownDomain == "" {
		s.ownDomain = DefaultOwnDomain
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return s, nil
}

// Recompute reports one finished recompute.
type Recompute struct {
	AccountID   string
	Version     int
	ActivityIDs []string
	Conflicts   []claims.Conflict
}

// DrainResult summarizes a Drain.
type DrainResult struct {
	Recomputes []Recompute
	// Failed counts jobs that errored and were released for retry (or parked).
	Failed int
}

// Drain processes due jobs until none is due, then returns. A job that fails is released with a
// backoff, so Drain terminates; its error is joined into the returned error.
func (s *Service) Drain(ctx context.Context) (DrainResult, error) {
	var res DrainResult
	var errs []error
	for i := 0; i < maxDrainIterations; i++ {
		if err := ctx.Err(); err != nil {
			return res, errors.Join(append(errs, err)...)
		}
		rec, claimed, err := s.RunOnce(ctx)
		switch {
		case err != nil && !claimed:
			return res, errors.Join(append(errs, err)...)
		case err != nil:
			res.Failed++
			errs = append(errs, err)
		case !claimed:
			return res, errors.Join(errs...)
		default:
			res.Recomputes = append(res.Recomputes, rec)
		}
	}
	return res, errors.Join(append(errs, errors.New("coalesce: drain iteration limit reached"))...)
}

// Run drains every poll interval until ctx is cancelled.
func (s *Service) Run(ctx context.Context, poll time.Duration) error {
	if poll <= 0 {
		return errors.New("coalesce: poll interval must be positive")
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastWarn time.Time
	for {
		if _, err := s.Drain(ctx); err != nil && ctx.Err() == nil {
			s.log.ErrorContext(ctx, "coalesce: drain failed", "error", err)
		}
		if time.Since(lastWarn) >= s.warnEvery {
			lastWarn = time.Now()
			s.warnBacklog(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
