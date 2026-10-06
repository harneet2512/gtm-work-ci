package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const defaultUnresolvedReason = "unresolved"

// Note on ON CONFLICT below: it deliberately has no conflict target. source_events has two
// unique indexes (the identity triple and idempotency_key, a function of the triple); with a
// target naming only the triple, a concurrent insert of the same event can trip the other
// index first and raise 23505 instead of being skipped.

// insertSourceEvent stores the raw event. On a redelivery of the same identity triple it only
// bumps delivery_count/last_delivered_at and reports inserted=false with the existing id;
// samePayload then says whether the redelivered payload and origin markers equal the stored
// ones (jsonb equality). The stored origin is never changed: a redelivery cannot relabel a base
// event as synthetic or the reverse (WP32).
func insertSourceEvent(ctx context.Context, tx *sql.Tx, ev normalize.SourceEvent, act normalize.Activity, now time.Time) (id string, inserted, samePayload bool, err error) {
	const insert = `
INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key,
                           connector, connector_version, occurred_at, received_at, last_delivered_at, payload,
                           origin, provenance)
VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $8, $9::jsonb, NULLIF($10, ''), NULLIF($11, ''))
ON CONFLICT DO NOTHING
RETURNING id::text`
	err = tx.QueryRowContext(ctx, insert, ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey, act.IdempotencyKey(),
		ev.Connector, ev.ConnectorVersion, ev.OccurredAt, now, string(ev.Payload), ev.Origin, ev.Provenance).Scan(&id)
	if err == nil {
		return id, true, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, false, fmt.Errorf("ingest: insert source event: %w", err)
	}

	const bump = `
UPDATE source_events SET delivery_count = delivery_count + 1, last_delivered_at = $4
 WHERE source_system = $1 AND source_object_id = $2 AND source_event_key = $3
RETURNING id::text, payload = $5::jsonb AND origin IS NOT DISTINCT FROM NULLIF($6, '')
                    AND provenance IS NOT DISTINCT FROM NULLIF($7, '')`
	err = tx.QueryRowContext(ctx, bump, ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey, now, string(ev.Payload),
		ev.Origin, ev.Provenance).Scan(&id, &samePayload)
	if err != nil {
		return "", false, false, fmt.Errorf("ingest: bump delivery count: %w", err)
	}
	return id, false, samePayload, nil
}

// existingResult returns the activity already stored for a redelivered source event.
func existingResult(ctx context.Context, tx *sql.Tx, eventID string) (Result, error) {
	var activityID string
	var account sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT id::text, account_id::text FROM activities WHERE source_event_id = $1::uuid`, eventID).Scan(&activityID, &account)
	if err != nil {
		return Result{}, fmt.Errorf("ingest: load existing activity for source event %s: %w", eventID, err)
	}
	res := Result{ActivityID: activityID, SourceEventID: eventID, Duplicate: true}
	if account.Valid {
		res.AccountID = &account.String
	}
	return res, nil
}

func insertActivity(ctx context.Context, tx *sql.Tx, eventID string, act normalize.Activity, r Resolution, now time.Time) (string, error) {
	permissions, err := json.Marshal(act.Permissions())
	if err != nil {
		return "", fmt.Errorf("ingest: encode permissions: %w", err)
	}
	provenance, err := json.Marshal(act.Provenance())
	if err != nil {
		return "", fmt.Errorf("ingest: encode provenance: %w", err)
	}
	const insert = `
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, ingested_at,
                        account_id, opportunity_id, account_hint, opportunity_hint, summary, body_text, permissions, provenance)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7::uuid, $8::uuid, NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
        $13::jsonb, $14::jsonb)
RETURNING id::text`
	var id string
	err = tx.QueryRowContext(ctx, insert, eventID, act.Type(), act.SourceSystem(), act.SourceObjectID(), act.OccurredAt(), now,
		nullIfEmpty(r.AccountID), nullIfEmpty(r.OpportunityID), act.AccountHint(), act.OpportunityHint(), act.Summary(), act.BodyText(),
		string(permissions), string(provenance)).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("ingest: insert activity: %w", err)
	}
	return id, nil
}

// insertParticipants writes all participants in one statement, linking person_id through the
// current entity_source_mappings row of each raw identity (at most one per identity).
func insertParticipants(ctx context.Context, tx *sql.Tx, activityID string, parts []normalize.Participant) error {
	if len(parts) == 0 {
		return nil
	}
	n := len(parts)
	raws, roles, names, systems, keys := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, p := range parts {
		raws[i], roles[i], names[i] = p.RawIdentity, p.Role, p.DisplayName
		systems[i], keys[i] = MappingKey(p.RawIdentity)
	}
	const insert = `
INSERT INTO activity_participants (activity_id, raw_identity, role, display_name, person_id)
SELECT $1::uuid, t.raw, t.role, NULLIF(t.name, ''), p.id
  FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[]) AS t(raw, role, name, sys, key)
  LEFT JOIN entity_source_mappings m
         ON m.entity_type = 'person' AND m.valid_to IS NULL AND m.source_system = t.sys AND m.source_key = t.key
  LEFT JOIN people p ON p.id = m.entity_id`
	if _, err := tx.ExecContext(ctx, insert, activityID, raws, roles, names, systems, keys); err != nil {
		return fmt.Errorf("ingest: insert participants: %w", err)
	}
	return nil
}

func recordUnresolved(ctx context.Context, tx *sql.Tx, activityID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = defaultUnresolvedReason
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO unresolved_activities (activity_id, reason) VALUES ($1::uuid, $2)`, activityID, reason)
	if err != nil {
		return fmt.Errorf("ingest: record unresolved activity: %w", err)
	}
	return nil
}

// enqueueRecompute upserts the account's single pending recompute job (see the
// recompute_jobs comments in migration 0003): the activity id is added to the distinct set
// and due_at moves to now+debounce but never beyond first_enqueued_at+maxWait, never backwards
// and never before first_enqueued_at even if the clock steps back. A job that is
// already in flight (claimed) is untouched; the unique index only covers pending rows, so the
// activity lands in a fresh pending job instead.
func enqueueRecompute(ctx context.Context, tx *sql.Tx, accountID, activityID string, now time.Time, debounce, maxWait time.Duration) (*time.Time, error) {
	const upsert = `
INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids)
VALUES ($1::uuid, $2::timestamptz + $3::bigint * interval '1 microsecond', $2::timestamptz, ARRAY[$4::uuid])
ON CONFLICT (account_id) WHERE claimed_at IS NULL DO UPDATE SET
    activity_ids = (SELECT array_agg(DISTINCT x ORDER BY x) FROM unnest(recompute_jobs.activity_ids || EXCLUDED.activity_ids) AS u(x)),
    due_at = GREATEST(recompute_jobs.due_at, recompute_jobs.first_enqueued_at,
                      LEAST(EXCLUDED.due_at, recompute_jobs.first_enqueued_at + $5::bigint * interval '1 microsecond'))
RETURNING due_at`
	var due time.Time
	err := tx.QueryRowContext(ctx, upsert, accountID, now, debounce.Microseconds(), activityID, maxWait.Microseconds()).Scan(&due)
	if err != nil {
		return nil, fmt.Errorf("ingest: enqueue recompute: %w", err)
	}
	due = due.UTC()
	return &due, nil
}
