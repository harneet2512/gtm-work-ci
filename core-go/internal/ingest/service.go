// Package ingest persists normalized source events: one transaction per event that stores the
// raw event, the activity and its participants, resolves the account, and either enqueues a
// coalesced state recompute or records the activity as unresolved.
package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Options configures a Service. The zero value of Resolver and Clock selects the defaults
// (MappingResolver, wall clock).
type Options struct {
	Resolver Resolver
	// Extension, when set, adds identity resolution around the default resolver (WP5).
	Extension Extension
	Clock     clock.Clock
	// Debounce is how long after the latest activity a recompute becomes due.
	Debounce time.Duration
	// MaxWait caps how long after the first pending activity the recompute may be postponed.
	MaxWait time.Duration
	// StatementTimeout and LockTimeout bound every statement of the ingest transaction
	// (SET LOCAL); zero selects DefaultStatementTimeout / DefaultLockTimeout.
	StatementTimeout time.Duration
	LockTimeout      time.Duration
	// Logger receives diagnostics (nil discards). Payload contents are never logged.
	Logger *slog.Logger
	// AfterAttribute, when set, runs in the ingest transaction once an activity is attributed to an
	// account (the graph projection outbox hooks in here). A returned error rolls the ingest back.
	AfterAttribute AttributeHook
}

// AttributeHook is called inside the ingest transaction for an activity attributed to accountID.
type AttributeHook func(ctx context.Context, tx *sql.Tx, accountID, activityID string, now time.Time) error

// Defaults for the per-transaction database timeouts.
const (
	DefaultStatementTimeout = 10 * time.Second
	DefaultLockTimeout      = 5 * time.Second
)

// Result is the outcome of one ingest call (core.yaml#IngestResult).
type Result struct {
	ActivityID    string `json:"activity_id"`
	SourceEventID string `json:"source_event_id"`
	// Duplicate is true when the event had been delivered before; nothing but the source
	// event's delivery counter changed.
	Duplicate bool `json:"duplicate"`
	// AccountID is nil when the activity could not be attributed to an account yet.
	AccountID *string `json:"account_id"`
	// RecomputeDueAt is when the account's pending recompute job becomes due; nil for
	// duplicates and unresolved activities.
	RecomputeDueAt *time.Time `json:"recompute_due_at"`
}

// Service ingests source events. It is safe for concurrent use.
type Service struct {
	db       *sql.DB
	resolver Resolver
	ext      Extension
	clock    clock.Clock
	debounce time.Duration
	maxWait  time.Duration
	stmtTime time.Duration
	lockTime time.Duration
	after    AttributeHook
	log      *slog.Logger
}

// NewService validates the options and returns a Service on db.
func NewService(db *sql.DB, opts Options) (*Service, error) {
	if db == nil {
		return nil, errors.New("ingest: database is required")
	}
	if opts.Debounce < 0 {
		return nil, errors.New("ingest: debounce must not be negative")
	}
	if opts.MaxWait < opts.Debounce {
		return nil, errors.New("ingest: max wait must be >= debounce")
	}
	if opts.StatementTimeout < 0 || opts.LockTimeout < 0 {
		return nil, errors.New("ingest: timeouts must not be negative")
	}
	s := &Service{db: db, resolver: opts.Resolver, ext: opts.Extension, clock: opts.Clock, debounce: opts.Debounce, maxWait: opts.MaxWait,
		stmtTime: opts.StatementTimeout, lockTime: opts.LockTimeout, after: opts.AfterAttribute, log: opts.Logger}
	if s.stmtTime == 0 {
		s.stmtTime = DefaultStatementTimeout
	}
	if s.lockTime == 0 {
		s.lockTime = DefaultLockTimeout
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if s.resolver == nil {
		s.resolver = MappingResolver{}
	}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	return s, nil
}

// Ingest normalizes and stores one event atomically. Input problems come back as a
// *normalize.ValidationError before the database is touched; everything else is an
// infrastructure error. Redelivery of the same (system, object, event key) is idempotent.
func (s *Service) Ingest(ctx context.Context, ev normalize.SourceEvent) (Result, error) {
	act, err := normalize.Normalize(ev)
	if err != nil {
		return Result{}, err
	}
	now := s.clock.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("ingest: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful commit

	// A stuck statement or lock must fail the request instead of pinning a pooled connection.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", s.stmtTime.Milliseconds())); err != nil {
		return Result{}, fmt.Errorf("ingest: set statement timeout: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", s.lockTime.Milliseconds())); err != nil {
		return Result{}, fmt.Errorf("ingest: set lock timeout: %w", err)
	}

	res, err := s.ingestTx(ctx, tx, ev, act, now)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("ingest: commit: %w", err)
	}
	return res, nil
}

func (s *Service) ingestTx(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity, now time.Time) (Result, error) {
	eventID, inserted, samePayload, err := insertSourceEvent(ctx, tx, ev, act, now)
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		if !samePayload {
			s.log.DebugContext(ctx, "duplicate delivery differs from the stored one (payload or origin markers); keeping the original",
				"source_system", ev.SourceSystem, "source_object_id", ev.SourceObjectID, "source_event_key", ev.SourceEventKey)
		}
		return existingResult(ctx, tx, eventID)
	}

	prepared, resolution, err := s.prepareAndResolve(ctx, tx, ev, act)
	if err != nil {
		return Result{}, err
	}
	activityID, err := insertActivity(ctx, tx, eventID, act, resolution, now)
	if err != nil {
		return Result{}, err
	}
	if err := insertParticipants(ctx, tx, activityID, act.Participants()); err != nil {
		return Result{}, err
	}
	fin, err := s.finalize(ctx, tx, FinalizeInput{Event: ev, Activity: act, ActivityID: activityID,
		Resolution: resolution, Prepared: prepared, EnqueueRecompute: s.enqueuer(tx, now)})
	if err != nil {
		return Result{}, err
	}
	res, err := s.attribute(ctx, tx, eventID, activityID, resolution, now)
	if err != nil {
		return Result{}, err
	}
	if fin.EntitiesChanged || (prepared != nil && prepared.EntitiesChanged()) {
		if err := s.reresolve(ctx, tx, now); err != nil {
			return Result{}, err
		}
	}
	return res, nil
}

// prepareAndResolve runs the extension's Prepare (if any), then the resolver.
func (s *Service) prepareAndResolve(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (Prepared, Resolution, error) {
	var prepared Prepared
	var err error
	if s.ext != nil {
		if prepared, err = s.ext.Prepare(ctx, tx, ev, act); err != nil {
			return nil, Resolution{}, fmt.Errorf("ingest: prepare: %w", err)
		}
	}
	resolution, err := s.resolver.Resolve(ctx, tx, act)
	if err != nil {
		return nil, Resolution{}, fmt.Errorf("ingest: resolve account: %w", err)
	}
	if resolution.AccountID == "" {
		resolution.OpportunityID = ""
	}
	return prepared, resolution, nil
}

func (s *Service) finalize(ctx context.Context, tx *sql.Tx, in FinalizeInput) (FinalizeResult, error) {
	if s.ext == nil {
		return FinalizeResult{}, nil
	}
	fin, err := s.ext.Finalize(ctx, tx, in)
	if err != nil {
		return FinalizeResult{}, fmt.Errorf("ingest: finalize: %w", err)
	}
	return fin, nil
}

// attribute parks the activity as unresolved or enqueues its account's recompute.
func (s *Service) attribute(ctx context.Context, tx *sql.Tx, eventID, activityID string, r Resolution, now time.Time) (Result, error) {
	res := Result{ActivityID: activityID, SourceEventID: eventID}
	if r.AccountID == "" {
		return res, recordUnresolved(ctx, tx, activityID, r.Reason)
	}
	res.AccountID = &r.AccountID
	var err error
	res.RecomputeDueAt, err = enqueueRecompute(ctx, tx, r.AccountID, activityID, now, s.debounce, s.maxWait)
	if err != nil || s.after == nil {
		return res, err
	}
	if err := s.after(ctx, tx, r.AccountID, activityID, now); err != nil {
		return res, fmt.Errorf("ingest: after attribute: %w", err)
	}
	return res, nil
}
