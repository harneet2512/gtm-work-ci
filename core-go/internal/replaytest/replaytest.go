// Package replaytest builds the world the Play and business-intelligence tests run in: an account with its
// people and deal, the real ingest -> coalesce -> pipeline stack on fixed clocks with a fake extractor, a
// demo manifest whose held-out event is an email, and a stand-in for the Neo4j projector that completes the
// graph projection jobs and records their diffs (the diffs are Postgres rows; only the Cypher is missing).
// It is test support only; nothing in cmd/ imports it.
package replaytest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/payloadhash"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// T0 is the replay world's clock: history is before it, the held-out event after it.
var T0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// Debounce and MaxWait are the coalescing windows of the stack.
const (
	Debounce = 3 * time.Second
	MaxWait  = 15 * time.Second
)

// Reset empties every table the replay tests write.
func Reset(t testing.TB, db *sql.DB) {
	t.Helper()
	err := storetest.Purge(context.Background(), db, `TRUNCATE accounts, people, opportunities, source_events, products, graph_projection_jobs
		RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("replaytest: reset: %v", err)
	}
}

// World is the seeded company: Acme with a deal, our rep and Acme's champion.
type World struct{ Account, Opportunity, Dana, Priya string }

// One runs a query that returns one text column.
func One(t testing.TB, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("replaytest: %s: %v", q, err)
	}
	return s.String
}

// SeedWorld resets the database and creates Acme (acme.com), its expansion deal, Dana (employee) and Priya
// (contact), with the email mappings that attribute their mail to the account.
func SeedWorld(t testing.TB, db *sql.DB) World {
	t.Helper()
	Reset(t, db)
	w := World{}
	w.Account = One(t, db, `INSERT INTO accounts (name, domain) VALUES ('Acme Corp', 'acme.com') RETURNING id::text`)
	w.Opportunity = One(t, db, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Acme EU expansion', 'expansion') RETURNING id::text`, w.Account)
	w.Dana = One(t, db, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Dana Kim', 'dana@ghostvendor.com') RETURNING id::text`)
	w.Priya = One(t, db, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Priya Shah', 'priya.shah@acme.com', $1::uuid) RETURNING id::text`, w.Account)
	for _, m := range [][2]string{{w.Dana, "dana@ghostvendor.com"}, {w.Priya, "priya.shah@acme.com"}} {
		if _, err := db.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
			VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, m[0], m[1]); err != nil {
			t.Fatalf("replaytest: mapping: %v", err)
		}
	}
	return w
}

// Email is one Acme email event; direction is "inbound" (Priya to Dana) or "outbound".
func Email(n int, at time.Time, direction, body string) normalize.SourceEvent {
	from, to := "priya.shah@acme.com", "dana@ghostvendor.com"
	if direction == "outbound" {
		from, to = to, from
	}
	return EmailBetween(n, at, direction, from, to, body)
}

// EmailBetween is an email event from one address to another (direction names the event key).
func EmailBetween(n int, at time.Time, direction, from, to, body string) normalize.SourceEvent {
	payload, _ := json.Marshal(map[string]any{
		"kind": "email", "direction": direction, "message_id": fmt.Sprintf("<m%d@x.com>", n), "thread_id": "thr-1",
		"from": map[string]any{"email": from}, "to": []map[string]any{{"email": to}},
		"date": at.Format(time.RFC3339), "subject": "Re: rollout", "body_text": body,
	})
	key := "received"
	if direction == "outbound" {
		key = "sent"
	}
	return normalize.SourceEvent{SourceSystem: "email", SourceObjectID: fmt.Sprintf("<m%d@x.com>", n), SourceEventKey: key,
		Connector: "gmail", ConnectorVersion: "1", OccurredAt: &at, Payload: payload}
}

// BlockerExtractor emits one blocker per email whose text says "need", quoting its first sentence.
func BlockerExtractor() *claimstest.FakeExtractor {
	return &claimstest.FakeExtractor{Candidates: func(req claims.ExtractRequest) []claims.Candidate {
		if !strings.Contains(req.Text, "need") {
			return nil
		}
		quote := strings.SplitN(req.Text, ".", 2)[0]
		return []claims.Candidate{{FieldPath: claims.FieldBlockers, Value: json.RawMessage(`"` + quote + `"`), Confidence: 0.9, EvidenceQuote: quote}}
	}}
}

// Stack is the real ingest and recompute pipeline of core, minus the background loop: Drain runs it.
type Stack struct {
	DB         *sql.DB
	Ingest     *ingest.Service
	Coalescer  *coalesce.Service
	CoalClock  *clock.Fixed
	IngestTime *clock.Fixed
}

// NewStack wires ingest -> coalesce -> pipeline (dry-run) -> graph projection outbox, as cmd/core does with
// Neo4j configured, on fixed clocks.
func NewStack(t testing.TB, db *sql.DB) *Stack {
	t.Helper()
	s := &Stack{DB: db, CoalClock: clock.NewFixed(T0), IngestTime: clock.NewFixed(T0)}
	var err error
	s.Ingest, err = ingest.NewService(db, ingest.Options{Clock: s.IngestTime, Debounce: Debounce, MaxWait: MaxWait,
		AfterAttribute: ctxgraph.IngestHook()})
	if err != nil {
		t.Fatalf("replaytest: ingest: %v", err)
	}
	p, err := pipeline.New(pipeline.Options{RunMode: runs.DryRun, Clock: s.CoalClock})
	if err != nil {
		t.Fatalf("replaytest: pipeline: %v", err)
	}
	s.Coalescer, err = coalesce.New(db, coalesce.Options{Extractor: BlockerExtractor(), Clock: s.CoalClock, WorkerID: "replaytest",
		Hook: ctxgraph.WithProjection(p, s.CoalClock)})
	if err != nil {
		t.Fatalf("replaytest: coalesce: %v", err)
	}
	return s
}

// Drain moves the recompute clock past the debounce window and recomputes every due job.
func (s *Stack) Drain(ctx context.Context) error {
	s.CoalClock.Set(s.CoalClock.Now().Add(Debounce + time.Second))
	res, err := s.Coalescer.Drain(ctx)
	if err != nil || res.Failed != 0 {
		return fmt.Errorf("replaytest: drain: %+v: %v", res, err)
	}
	return nil
}

// History ingests n inbound/outbound emails before T0 (message numbers 1..n) and recomputes, so the account
// has state before the held-out event.
func (s *Stack) History(t testing.TB, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		direction := "inbound"
		if i%2 == 0 {
			direction = "outbound"
		}
		if _, err := s.Ingest.Ingest(context.Background(), Email(i, T0.Add(time.Duration(i-n-1)*time.Hour), direction, "Thanks for the update")); err != nil {
			t.Fatalf("replaytest: history ingest: %v", err)
		}
	}
	if err := s.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	FakeProjector{DB: s.DB}.Complete(t)
}

// HeldOut is the held-out event: the email that moves the account, and the manifest's description of it.
type HeldOut struct {
	EventID string // the replay dataset's id of the event
	Event   normalize.SourceEvent
}

// NewHeldOut is the SOC2 email: it asks for something, so the fake extractor turns it into a blocker.
func NewHeldOut(n int) HeldOut {
	return HeldOut{EventID: "0e7e0000-0000-4000-8000-0000000000" + fmt.Sprintf("%02d", n),
		Event: Email(n, T0.Add(time.Hour), "inbound", "We need the SOC2 report before signing. Thanks")}
}

// HistoryEvents are the two emails History(t, 2) ingests, in replay order: the events 1 and 2 of the manifest
// InsertManifest writes by default.
func HistoryEvents() []normalize.SourceEvent {
	return []normalize.SourceEvent{
		Email(1, T0.Add(-2*time.Hour), "inbound", "Thanks for the update"),
		Email(2, T0.Add(-time.Hour), "outbound", "Thanks for the update"),
	}
}

// HistoryEventID is the replay dataset's id of history event i (1-based).
func HistoryEventID(i int) string { return fmt.Sprintf("0e7e0000-0000-4000-8000-0000000000a%d", i) }

// provenanceOf is the held_out_event provenance of a source event.
func provenanceOf(ev normalize.SourceEvent) map[string]any {
	return map[string]any{"origin": "dataset", "provenance": "crmarena-pro:b2b",
		"source": map[string]any{"source_system": ev.SourceSystem, "source_object_id": ev.SourceObjectID}, "source_event_key": ev.SourceEventKey}
}

// PayloadSHA256 is the digest a manifest pins for an event's payload: the lowercase hex SHA-256 of the
// payload's canonical JSON (payloadhash.SHA256).
func PayloadSHA256(ev normalize.SourceEvent) string {
	sum, err := payloadhash.SHA256(ev.Payload)
	if err != nil {
		panic("replaytest: " + err.Error())
	}
	return sum
}

// InsertManifest writes a manifest for the world whose history is the given events (default HistoryEvents) and
// whose held-out event is h. It returns the manifest id.
func InsertManifest(t testing.TB, db *sql.DB, w World, h HeldOut, history ...normalize.SourceEvent) string {
	t.Helper()
	if len(history) == 0 {
		history = HistoryEvents()
	}
	events := make([]map[string]any, 0, len(history))
	for i, ev := range history {
		events = append(events, map[string]any{"event": map[string]any{"event_id": HistoryEventID(i + 1), "provenance": provenanceOf(ev),
			"occurred_at": ev.OccurredAt.UTC().Format(time.RFC3339), "replay_position": i + 1}})
	}
	held := map[string]any{
		"event_id": h.EventID, "provenance": provenanceOf(h.Event), "payload_sha256": PayloadSHA256(h.Event),
		"occurred_at": h.Event.OccurredAt.UTC().Format(time.RFC3339), "replay_position": len(history) + 1,
	}
	heldRaw, _ := json.Marshal(held)
	historyRaw, _ := json.Marshal(events)
	cutoff := history[len(history)-1].OccurredAt.UTC()
	return One(t, db, `INSERT INTO demo_manifests (account_id, opportunity_id, data_cutoff, events, held_out_event, why_selected, content_sha256)
		VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb, 'replaytest', repeat('a', 64)) RETURNING id::text`,
		w.Account, w.Opportunity, cutoff, string(historyRaw), string(heldRaw))
}

// FakeProjector stands in for the Neo4j projector: Complete finishes every unfinished projection job and
// records the diff the real projector would have (a Person node and a Claim node per job activity, evidence-linked
// to the activity and its source event).
type FakeProjector struct{ DB *sql.DB }

// Complete finishes the unfinished jobs; it returns how many it completed.
func (p FakeProjector) Complete(t testing.TB) int {
	t.Helper()
	rows, err := p.DB.Query(`UPDATE graph_projection_jobs SET claimed_at = now(), claimed_by = 'fake', lease_expires_at = now() + interval '1 minute',
		completed_at = now() WHERE completed_at IS NULL RETURNING id, account_id::text, to_jsonb(activity_ids)::text`)
	if err != nil {
		t.Fatalf("replaytest: complete jobs: %v", err)
	}
	type job struct {
		id         int64
		account    string
		activities []string
	}
	var jobs []job
	for rows.Next() {
		var j job
		var raw string
		if err := rows.Scan(&j.id, &j.account, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &j.activities); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	_ = rows.Close()
	for _, j := range jobs {
		for _, act := range j.activities {
			ev := One(t, p.DB, `SELECT source_event_id::text FROM activities WHERE id = $1::uuid`, act)
			changes, _ := json.Marshal([]map[string]any{
				{"kind": "node", "op": "added", "type": "Person", "id": "p-" + act, "after_hash": "h1", "source_event_ids": []string{ev},
					"props": map[string]any{"evidence_activity_ids": []string{act}, "source_event_ids": []string{ev}}},
				{"kind": "node", "op": "added", "type": "Claim", "id": "k-" + act, "after_hash": "h2", "source_event_ids": []string{ev},
					"props": map[string]any{"evidence_activity_ids": []string{act}, "source_event_ids": []string{ev}}},
			})
			if _, err := p.DB.Exec(`INSERT INTO graph_projection_diffs (job_id, account_id, activity_ids, source_event_ids, summary, changes)
				VALUES ($1, $2::uuid, ARRAY[$3]::uuid[], ARRAY[$4]::uuid[], '{"added":2,"changed":0,"removed":0,"repaired":0}'::jsonb, $5::jsonb)
				ON CONFLICT (job_id) DO UPDATE SET activity_ids = graph_projection_diffs.activity_ids || EXCLUDED.activity_ids,
				  source_event_ids = graph_projection_diffs.source_event_ids || EXCLUDED.source_event_ids,
				  changes = graph_projection_diffs.changes || EXCLUDED.changes`,
				j.id, j.account, act, ev, string(changes)); err != nil {
				t.Fatalf("replaytest: record diff: %v", err)
			}
		}
	}
	return len(jobs)
}
