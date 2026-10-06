package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Extension is the identity-resolution hook (WP5). Both calls run inside the ingest
// transaction, so everything they write commits or rolls back with the activity.
//
// Prepare runs before Resolver.Resolve. It may create the entities an event announces
// (accounts, opportunities, contacts) and their source mappings so the resolver can find them.
// The value it returns is handed to Finalize unchanged.
//
// Finalize runs after the activity and its participants are stored. It resolves participants
// and speakers onto people, writes graph edges and back-fills person links.
//
// Both must be idempotent: re-resolution calls them again for activities that were parked as
// unresolved.
type Extension interface {
	Prepare(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity) (Prepared, error)
	Finalize(ctx context.Context, tx *sql.Tx, in FinalizeInput) (FinalizeResult, error)
}

// Prepared is what Prepare hands to Finalize. Returning a nil Prepared means "nothing prepared".
type Prepared interface {
	// EntitiesChanged is true when Prepare created an account, opportunity or account-level
	// mapping, so activities parked as unresolved may now resolve.
	EntitiesChanged() bool
}

// FinalizeInput is everything Finalize may need about the stored activity.
type FinalizeInput struct {
	Event      normalize.SourceEvent
	Activity   normalize.Activity
	ActivityID string
	Resolution Resolution
	// Prepared is whatever Prepare returned for this event (nil if it returned nil).
	Prepared Prepared
	// EnqueueRecompute queues a state recompute for an account because of an activity, through
	// the same coalescing outbox as ingest itself.
	EnqueueRecompute func(ctx context.Context, accountID, activityID string) error
	// Reresolved is true when the activity had been parked as unresolved and is being
	// resolved late.
	Reresolved bool
}

// FinalizeResult reports what Finalize changed beyond the activity itself.
type FinalizeResult struct {
	// EntitiesChanged is true when Finalize itself created an account, opportunity or
	// account-level mapping (in addition to what Prepared reports).
	EntitiesChanged bool
}

// MaxReresolve bounds how many parked activities one re-resolution pass revisits.
const MaxReresolve = 1000

// Back-off for parked activities that keep failing: a row tried n times waits 2^n minutes (at
// most one day) before it is tried again, so newer rows are always reached.
const (
	maxBackoffExponent = 12
	maxBackoff         = "interval '1 day'"
)

// parked is one unresolved activity together with its stored source event.
type parked struct {
	activityID string
	event      normalize.SourceEvent
}

func (s *Service) enqueuer(tx *sql.Tx, now time.Time) func(context.Context, string, string) error {
	return func(ctx context.Context, accountID, activityID string) error {
		_, err := enqueueRecompute(ctx, tx, accountID, activityID, now, s.debounce, s.maxWait)
		return err
	}
}

// reresolve rebuilds each parked activity from its stored source event (stored hints lose
// their kind, so the payload is re-normalized), runs Prepare and the resolver again and, if the
// activity now belongs to an account, moves it there, re-links its participants, clears the
// unresolved row, queues a recompute and runs Finalize. It is a single pass in occurrence
// order, so a calendar event is placed before the call that refers to it. Rows that fail are
// counted and backed off.
func (s *Service) reresolve(ctx context.Context, tx *sql.Tx, now time.Time) error {
	items, err := loadParked(ctx, tx, now)
	if err != nil {
		return err
	}
	for _, it := range items {
		resolved, err := s.tryReresolve(ctx, tx, it, now)
		if err != nil {
			return err
		}
		if resolved {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE unresolved_activities SET attempts = attempts + 1, last_tried_at = $2 WHERE activity_id = $1::uuid`,
			it.activityID, now); err != nil {
			return fmt.Errorf("ingest: record re-resolution attempt: %w", err)
		}
	}
	return nil
}

// tryReresolve runs one parked row inside a savepoint. If the row's Prepare, resolver or Finalize
// fails, only that row is rolled back and counted as a failed attempt; one poisoned row must not
// abort the ingest that triggered the pass. A cancelled context, or a failure of the savepoint
// itself, still aborts.
func (s *Service) tryReresolve(ctx context.Context, tx *sql.Tx, it parked, now time.Time) (bool, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT reresolve_row`); err != nil {
		return false, fmt.Errorf("ingest: savepoint: %w", err)
	}
	resolved, rowErr := s.reresolveOne(ctx, tx, it, now)
	if rowErr != nil {
		if ctx.Err() != nil {
			return false, rowErr
		}
		s.log.WarnContext(ctx, "re-resolving a parked activity failed; skipping it", "activity_id", it.activityID, "error", rowErr.Error())
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT reresolve_row`); err != nil {
			return false, fmt.Errorf("ingest: roll back to savepoint: %w", err)
		}
		resolved = false
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT reresolve_row`); err != nil {
		return false, fmt.Errorf("ingest: release savepoint: %w", err)
	}
	return resolved, nil
}

func (s *Service) reresolveOne(ctx context.Context, tx *sql.Tx, it parked, now time.Time) (bool, error) {
	act, err := normalize.Normalize(it.event)
	if err != nil {
		// Stored events passed normalization once; if the rules tightened since, leave it parked.
		s.log.WarnContext(ctx, "parked activity no longer normalizes", "activity_id", it.activityID, "error", err.Error())
		return false, nil
	}
	prepared, err := s.ext.Prepare(ctx, tx, it.event, act)
	if err != nil {
		return false, fmt.Errorf("ingest: re-resolve prepare: %w", err)
	}
	resolution, err := s.resolver.Resolve(ctx, tx, act)
	if err != nil {
		return false, fmt.Errorf("ingest: re-resolve: %w", err)
	}
	if resolution.AccountID == "" {
		return false, nil
	}
	if err := moveToAccount(ctx, tx, it.activityID, resolution); err != nil {
		return false, err
	}
	if err := relinkParticipants(ctx, tx, it.activityID, act.Participants()); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unresolved_activities WHERE activity_id = $1::uuid`, it.activityID); err != nil {
		return false, fmt.Errorf("ingest: clear unresolved activity: %w", err)
	}
	enqueue := s.enqueuer(tx, now)
	if err := enqueue(ctx, resolution.AccountID, it.activityID); err != nil {
		return false, err
	}
	_, err = s.ext.Finalize(ctx, tx, FinalizeInput{Event: it.event, Activity: act, ActivityID: it.activityID,
		Resolution: resolution, Prepared: prepared, Reresolved: true, EnqueueRecompute: enqueue})
	if err != nil {
		return false, fmt.Errorf("ingest: re-resolve finalize: %w", err)
	}
	return true, nil
}

// loadParked returns the parked activities that are due for another try: least-tried first (so
// rows that keep failing cannot hide newer ones), then in occurrence order.
func loadParked(ctx context.Context, tx *sql.Tx, now time.Time) ([]parked, error) {
	query := `
SELECT a.id::text, se.source_system, se.source_object_id, se.source_event_key, se.occurred_at,
       coalesce(se.connector, ''), coalesce(se.connector_version, ''), se.payload::text,
       coalesce(se.origin, ''), coalesce(se.provenance, '')
  FROM unresolved_activities u
  JOIN activities a ON a.id = u.activity_id
  JOIN source_events se ON se.id = a.source_event_id
 WHERE u.last_tried_at IS NULL
    OR u.last_tried_at + LEAST(interval '1 minute' * power(2, LEAST(u.attempts, ` + fmt.Sprint(maxBackoffExponent) + `)), ` + maxBackoff + `) <= $2
 ORDER BY u.attempts, a.occurred_at, a.id
 LIMIT $1`
	rows, err := tx.QueryContext(ctx, query, MaxReresolve, now)
	if err != nil {
		return nil, fmt.Errorf("ingest: load unresolved activities: %w", err)
	}
	defer rows.Close()
	var out []parked
	for rows.Next() {
		var p parked
		var occurred sql.NullTime
		var payload string
		if err := rows.Scan(&p.activityID, &p.event.SourceSystem, &p.event.SourceObjectID, &p.event.SourceEventKey,
			&occurred, &p.event.Connector, &p.event.ConnectorVersion, &payload, &p.event.Origin, &p.event.Provenance); err != nil {
			return nil, fmt.Errorf("ingest: scan unresolved activity: %w", err)
		}
		if occurred.Valid {
			at := occurred.Time.UTC()
			p.event.OccurredAt = &at
		}
		p.event.Payload = []byte(payload)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ingest: read unresolved activities: %w", err)
	}
	// Selection was by attempts; processing is by occurrence (a calendar event before its call).
	sort.SliceStable(out, func(i, j int) bool { return occurredOf(out[i]).Before(occurredOf(out[j])) })
	return out, nil
}

func occurredOf(p parked) time.Time {
	if p.event.OccurredAt == nil {
		return time.Time{}
	}
	return *p.event.OccurredAt
}

func moveToAccount(ctx context.Context, tx *sql.Tx, activityID string, r Resolution) error {
	_, err := tx.ExecContext(ctx, `UPDATE activities SET account_id = $2::uuid, opportunity_id = $3::uuid WHERE id = $1::uuid`,
		activityID, r.AccountID, nullIfEmpty(r.OpportunityID))
	if err != nil {
		return fmt.Errorf("ingest: move activity to account: %w", err)
	}
	return nil
}

// relinkParticipants fills person_id for participants that had no current mapping when the
// activity was first stored, in one statement. Existing links are kept.
func relinkParticipants(ctx context.Context, tx *sql.Tx, activityID string, parts []normalize.Participant) error {
	var raws, systems, keys []string
	for _, p := range parts {
		system, key := MappingKey(p.RawIdentity)
		if system == "" {
			continue
		}
		raws, systems, keys = append(raws, p.RawIdentity), append(systems, system), append(keys, key)
	}
	if len(raws) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
UPDATE activity_participants ap SET person_id = m.entity_id
  FROM unnest($2::text[], $3::text[], $4::text[]) AS t(raw, sys, key)
  JOIN entity_source_mappings m ON m.entity_type = 'person' AND m.valid_to IS NULL AND m.source_system = t.sys AND m.source_key = t.key
 WHERE ap.activity_id = $1::uuid AND ap.raw_identity = t.raw AND ap.person_id IS NULL`, activityID, raws, systems, keys)
	if err != nil {
		return fmt.Errorf("ingest: relink participants: %w", err)
	}
	return nil
}
