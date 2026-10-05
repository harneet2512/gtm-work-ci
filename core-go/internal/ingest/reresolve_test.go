package ingest_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

// parkMany inserts n unresolvable email activities (valid stored payloads, no account anywhere)
// older than every real event, parked in unresolved_activities.
func parkMany(t *testing.T, n int) {
	t.Helper()
	_, err := env.DB.Exec(`
WITH ev AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, occurred_at, payload)
  SELECT 'email', 'bulk-' || g, 'received', lpad(to_hex(g), 64, '0'), $2::timestamptz + g * interval '1 second',
         jsonb_build_object('kind', 'email', 'message_id', 'bulk-' || g, 'thread_id', 't' || g, 'direction', 'inbound',
           'from', jsonb_build_object('email', 'u' || g || '@nowhere.example'),
           'to', jsonb_build_array(jsonb_build_object('email', 'dana@ghostvendor.com')),
           'date', to_char($2::timestamptz + g * interval '1 second', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
           'subject', 's', 'body_text', 'b')
  FROM generate_series(1, $1) g RETURNING id, occurred_at, source_object_id),
act AS (
  INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, provenance)
  SELECT id, 'EmailReceived', 'email', source_object_id, occurred_at, '{}' FROM ev RETURNING id)
INSERT INTO unresolved_activities (activity_id, reason) SELECT id, 'account_not_found' FROM act`, n, t0.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("park %d activities: %v", n, err)
	}
}

// More than MaxReresolve rows that can never resolve must not hide a newer row that now can.
func TestReresolutionBacksOffFailingRowsSoNewerOnesAreReached(t *testing.T) {
	changed := false
	ext := &fakeExtension{finalize: func(context.Context, *sql.Tx, ingest.FinalizeInput) (ingest.FinalizeResult, error) {
		return ingest.FinalizeResult{EntitiesChanged: changed}, nil
	}}
	svc := newExtService(t, ext)
	parkMany(t, ingest.MaxReresolve+100)
	target := mustIngest(t, svc, inboundEmail(t, "late", "buyer@late.example", "", t0))
	if target.AccountID != nil {
		t.Fatal("setup: the target must be parked first")
	}

	changed = true // first pass: only failures are tried, so they back off
	mustIngest(t, svc, inboundEmail(t, "trigger1", "x@nowhere.example", "", t0.Add(time.Minute)))
	if n := queryString(t, `SELECT count(*)::text FROM unresolved_activities WHERE attempts > 0`); n == "0" {
		t.Fatal("failed re-resolutions must be counted in unresolved_activities.attempts")
	}
	if got := queryString(t, `SELECT count(*)::text FROM unresolved_activities WHERE attempts > 1`); got != "0" {
		t.Errorf("%s rows tried twice in one pass", got)
	}

	late := seedAccount(t, "Late", "late.example") // the account the target was waiting for
	changed = true
	mustIngest(t, svc, inboundEmail(t, "trigger2", "y@nowhere.example", "", t0.Add(2*time.Minute)))

	if got := queryString(t, `SELECT account_id::text FROM activities WHERE id = $1::uuid`, target.ActivityID); got != late {
		t.Errorf("the newest parked activity stayed unresolved behind %d older failing rows", ingest.MaxReresolve)
	}
}

// Finalize can queue a recompute for any account, e.g. one whose activities it re-pointed to
// another person.
func TestFinalizeCanEnqueueARecomputeForAnotherAccount(t *testing.T) {
	var other string
	ext := &fakeExtension{finalize: func(ctx context.Context, tx *sql.Tx, in ingest.FinalizeInput) (ingest.FinalizeResult, error) {
		return ingest.FinalizeResult{}, in.EnqueueRecompute(ctx, other, in.ActivityID)
	}}
	svc := newExtService(t, ext)
	seedAccount(t, "Acme", "acme.com")
	other = seedAccount(t, "Beta", "beta.io")

	res := mustIngest(t, svc, inboundEmail(t, "m1", "priya@acme.com", "", t0))

	if got := queryString(t, `SELECT cardinality(activity_ids)::text FROM recompute_jobs WHERE account_id = $1::uuid`, other); got != "1" {
		t.Errorf("the other account's job covers %s activities, want 1", got)
	}
	if got := queryString(t, `SELECT $1::uuid = ANY(activity_ids) FROM recompute_jobs WHERE account_id = $2::uuid`, res.ActivityID, other); got != "true" {
		t.Error("the job does not name the activity")
	}
}
