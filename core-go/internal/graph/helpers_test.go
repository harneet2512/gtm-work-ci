package graph_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var (
	ctx = context.Background()
	t0  = time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
)

func resetDB(t *testing.T) {
	t.Helper()
	err := storetest.Purge(context.Background(), env.DB, `TRUNCATE recompute_jobs, unresolved_activities, activity_participants, relationships,
		activities, source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset db: %v", err)
	}
}

func str(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query %q: %v", q, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

func num(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func newAccount(t *testing.T, name, domain string) string {
	t.Helper()
	return str(t, `INSERT INTO accounts (name, domain) VALUES ($1, NULLIF($2, '')) RETURNING id::text`, name, domain)
}

func newPerson(t *testing.T, kind, name, email string) string {
	t.Helper()
	return str(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ($1, $2, NULLIF($3, '')) RETURNING id::text`, kind, name, email)
}

func newOpp(t *testing.T, accountID, name string) string {
	t.Helper()
	return str(t, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, $2, 'expansion') RETURNING id::text`, accountID, name)
}

func newActivity(t *testing.T) string {
	t.Helper()
	n := num(t, `SELECT count(*) FROM source_events`)
	id := str(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('crm', $1, 'created', $2, '{}') RETURNING id::text`, fmt.Sprintf("o%d", n), fmt.Sprintf("%064x", n+1))
	return str(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, provenance)
		VALUES ($1::uuid, 'CRMFieldChanged', 'crm', $2, $3, '{}') RETURNING id::text`, id, fmt.Sprintf("o%d", n), t0)
}

// service builds an ingest service with the WP5 extension attached.
func service(t *testing.T) *ingest.Service {
	t.Helper()
	svc, err := ingest.NewService(env.DB, ingest.Options{
		Extension: graph.NewExtension(), Clock: clock.NewFixed(t0),
		Debounce: time.Second, MaxWait: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func ingestAll(t *testing.T, svc *ingest.Service, events ...normalize.SourceEvent) {
	t.Helper()
	for _, ev := range events {
		if _, err := svc.Ingest(ctx, ev); err != nil {
			t.Fatalf("ingest %s/%s/%s: %v", ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey, err)
		}
	}
}

func event(t *testing.T, system, object, key string, payload map[string]any) normalize.SourceEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return normalize.SourceEvent{SourceSystem: system, SourceObjectID: object, SourceEventKey: key, Payload: raw}
}

type fields map[string]any

func crmChange(t *testing.T, object, record, account string, created bool, at time.Time, f fields) normalize.SourceEvent {
	t.Helper()
	changes := map[string]any{}
	for k, v := range f {
		changes[k] = map[string]any{"new": v}
	}
	payload := map[string]any{
		"kind": "crm_change", "object_type": object, "record_id": record, "changed_at": at.Format(time.RFC3339),
		"changed_by": "dana@ghostvendor.com", "created": created, "fields": changes,
	}
	if account != "" {
		payload["account_record_id"] = account
	}
	key := "created"
	if object == "Opportunity" {
		key = fmt.Sprintf("field:StageName:%v", f["StageName"])
	}
	if !created {
		for k, v := range f { // single-field change events
			key = fmt.Sprintf("field:%s:%v", k, v)
		}
	}
	return event(t, "crm", record, key, payload)
}

func crmAccount(t *testing.T, record, name, website string) normalize.SourceEvent {
	return crmChange(t, "Account", record, "", true, t0, fields{"Name": name, "Website": website})
}

func crmOpp(t *testing.T, record, account, owner string) normalize.SourceEvent {
	return crmChange(t, "Opportunity", record, account, true, t0.Add(time.Minute),
		fields{"Name": "Expansion", "Type": "Expansion", "StageName": "Discovery", "OwnerEmail": owner})
}

func crmContact(t *testing.T, record, account, first, last, email string, at time.Time) normalize.SourceEvent {
	return crmChange(t, "Contact", record, account, true, at, fields{"FirstName": first, "LastName": last, "Email": email})
}

func email(t *testing.T, id, from string, to []string, at time.Time) normalize.SourceEvent {
	t.Helper()
	recipients := make([]map[string]any, len(to))
	for i, e := range to {
		recipients[i] = map[string]any{"email": e}
	}
	return event(t, "email", id, "received", map[string]any{
		"kind": "email", "message_id": id, "thread_id": "thr-" + id, "direction": "inbound",
		"from": map[string]any{"email": from, "name": "Sender " + id}, "to": recipients,
		"date": at.Format(time.RFC3339), "subject": "s", "body_text": "b",
	})
}

type attendee struct{ Email, Name string }

func calendar(t *testing.T, id, key string, at time.Time, opp string, who ...attendee) normalize.SourceEvent {
	t.Helper()
	list := make([]map[string]any, len(who))
	for i, a := range who {
		list[i] = map[string]any{"email": a.Email, "name": a.Name, "response": "accepted"}
	}
	payload := map[string]any{
		"kind": "calendar_event", "event_id": id, "title": "Meeting", "start": at.Format(time.RFC3339),
		"end": at.Add(time.Hour).Format(time.RFC3339), "status": "scheduled",
		"organizer": map[string]any{"email": who[0].Email, "name": who[0].Name}, "attendees": list,
	}
	if opp != "" {
		payload["crm_opportunity_ref"] = opp
	}
	ev := event(t, "calendar", id, key, payload)
	occurred := at.Add(-time.Hour)
	ev.OccurredAt = &occurred
	return ev
}

type speaker struct{ Label, Email, Name string }

type segment struct{ Label, Text string }

func call(t *testing.T, id, calendarID, key string, at time.Time, speakers []speaker, transcript []segment) normalize.SourceEvent {
	t.Helper()
	list := make([]map[string]any, len(speakers))
	for i, s := range speakers {
		m := map[string]any{"label": s.Label, "email": nil, "name": nil}
		if s.Email != "" {
			m["email"] = s.Email
		}
		if s.Name != "" {
			m["name"] = s.Name
		}
		list[i] = m
	}
	payload := map[string]any{
		"kind": "call", "call_id": id, "calendar_event_id": nil,
		"started_at": at.Format(time.RFC3339), "ended_at": at.Add(time.Hour).Format(time.RFC3339), "speakers": list,
	}
	if calendarID != "" {
		payload["calendar_event_id"] = calendarID
	}
	if key == "transcript_ready" {
		segs := make([]map[string]any, len(transcript))
		for i, s := range transcript {
			segs[i] = map[string]any{"speaker": s.Label, "offset_s": i * 5, "text": s.Text}
		}
		payload["transcript"] = segs
	}
	return event(t, "call", id, key, payload)
}

// inTx runs fn in a transaction and commits it: the graph writers need one (see graph.Txn).
func inTx(t *testing.T, fn func(tx *sql.Tx) error) error {
	t.Helper()
	tx, err := env.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func upsert(t *testing.T, s graph.EdgeSpec) (created bool, err error) {
	t.Helper()
	err = inTx(t, func(tx *sql.Tx) (e error) { created, e = graph.UpsertEdge(ctx, tx, s); return })
	return created, err
}

func insertMapping(t *testing.T, m graph.Mapping) (out graph.Mapping, err error) {
	t.Helper()
	err = inTx(t, func(tx *sql.Tx) (e error) { out, e = graph.InsertMapping(ctx, tx, m); return })
	return out, err
}

func ensureMapping(t *testing.T, m graph.Mapping) (out graph.Mapping, created bool, err error) {
	t.Helper()
	err = inTx(t, func(tx *sql.Tx) (e error) { out, created, e = graph.EnsureMapping(ctx, tx, m); return })
	return out, created, err
}

func remap(t *testing.T, k graph.SourceKey, id string, at time.Time, o *graph.MappingOverride) (out graph.Mapping, err error) {
	t.Helper()
	err = inTx(t, func(tx *sql.Tx) (e error) { out, e = graph.Remap(ctx, tx, k, id, at, o); return })
	return out, err
}
