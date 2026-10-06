package ingest_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

const (
	testDebounce = 3 * time.Second
	testMaxWait  = 15 * time.Second
)

var t0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// resetDB empties every table the ingest path touches.
func resetDB(t *testing.T) {
	t.Helper()
	err := storetest.Purge(context.Background(), env.DB, `TRUNCATE recompute_jobs, unresolved_activities, activity_participants, relationships, activities,
		source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset db: %v", err)
	}
}

func newService(t *testing.T, clk clock.Clock, opts ingest.Options) *ingest.Service {
	t.Helper()
	opts.Clock = clk
	if opts.Debounce == 0 {
		opts.Debounce = testDebounce
	}
	if opts.MaxWait == 0 {
		opts.MaxWait = testMaxWait
	}
	svc, err := ingest.NewService(env.DB, opts)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

func mustIngest(t *testing.T, svc *ingest.Service, ev normalize.SourceEvent) ingest.Result {
	t.Helper()
	res, err := svc.Ingest(context.Background(), ev)
	if err != nil {
		t.Fatalf("ingest %s/%s/%s: %v", ev.SourceSystem, ev.SourceObjectID, ev.SourceEventKey, err)
	}
	return res
}

// inboundEmail builds a distinct inbound email event from `from` at the given time.
func inboundEmail(t *testing.T, id, from, oppRef string, at time.Time) normalize.SourceEvent {
	t.Helper()
	payload := map[string]any{
		"kind": "email", "message_id": id, "thread_id": "thr-" + id, "direction": "inbound",
		"from":    map[string]any{"email": from, "name": "Sender"},
		"to":      []map[string]any{{"email": "dana@vendor.example"}},
		"cc":      []map[string]any{{"email": "priya.shah@acme.com"}},
		"date":    at.Format(time.RFC3339),
		"subject": "Subject " + id, "body_text": "Body of " + id,
	}
	if oppRef != "" {
		payload["crm_opportunity_ref"] = oppRef
	}
	return rawEvent(t, "email", id, "received", payload)
}

func rawEvent(t *testing.T, system, object, key string, payload map[string]any) normalize.SourceEvent {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return normalize.SourceEvent{
		SourceSystem: system, SourceObjectID: object, SourceEventKey: key,
		Connector: "test", ConnectorVersion: "1", Payload: raw,
	}
}

func seedAccount(t *testing.T, name, domain string) string {
	t.Helper()
	var id string
	err := env.DB.QueryRow(`INSERT INTO accounts (name, domain) VALUES ($1, NULLIF($2, '')) RETURNING id::text`, name, domain).Scan(&id)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return id
}

func seedPerson(t *testing.T, name, email string) string {
	t.Helper()
	var id string
	err := env.DB.QueryRow(`INSERT INTO people (kind, display_name, primary_email) VALUES ('contact', $1, $2) RETURNING id::text`, name, email).Scan(&id)
	if err != nil {
		t.Fatalf("seed person: %v", err)
	}
	return id
}

func seedOpportunity(t *testing.T, accountID, name string) string {
	t.Helper()
	var id string
	err := env.DB.QueryRow(`INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, $2, 'expansion') RETURNING id::text`, accountID, name).Scan(&id)
	if err != nil {
		t.Fatalf("seed opportunity: %v", err)
	}
	return id
}

func seedMapping(t *testing.T, entityType, entityID, system, key string) {
	t.Helper()
	_, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
		VALUES ($1, $2::uuid, $3, $4, 1, 'seed')`, entityType, entityID, system, key)
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
}

func count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// tableCounts snapshots the row counts of every table ingest writes to.
func tableCounts(t *testing.T) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"source_events", "activities", "activity_participants", "unresolved_activities", "recompute_jobs"} {
		out[table] = count(t, table)
	}
	return out
}

func queryString(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s.String
}
