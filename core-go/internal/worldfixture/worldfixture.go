// Package worldfixture replays a five-event account history through the real pipeline (ingest, claim
// extraction by a scripted fake, coalesced recompute) so the world-time read tests (ADR-0019) can ingest
// E1..En and assert what an as-of read shows at T = occurred_at(Ek). It is test support only; nothing in
// cmd/ imports it, and no model is called.
//
// The story (Acme, distinct occurred_at per event, one state version per event):
//
//	E1 Priya: stage "Discovery"; the champion_for edge Priya -> deal opens
//	E2 Priya: blocker "Security review is pending"
//	E3 Marco: stage "Negotiation" (changes E1's value, supersedes it); the champion_for edge closes;
//	          Marco becomes the economic buyer (a person nothing world-timed touched before)
//	E4 Priya: blocker "Budget is frozen until Q4"; a signal is emitted
//	E5 Priya: stage "Closed Won" (changes E3's value, supersedes it)
package worldfixture

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
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Marker texts the tests look for: a value from an event must never appear at or before its own time.
const (
	StageDiscovery   = "Discovery"
	StageNegotiation = "Negotiation"
	StageClosedWon   = "Closed Won"
	BlockerSecurity  = "Security review is pending"
	BlockerBudget    = "Budget is frozen until Q4"
)

// Event is one replayed event and what the pipeline made of it.
type Event struct {
	N             int
	At            time.Time
	From          string // sender email
	Body          string
	ActivityID    string
	SourceEventID string
	Version       int // account state version the recompute after this event wrote
}

// World is the seeded account.
type World struct {
	DB          *sql.DB
	Account     string
	Opportunity string
	Dana        string // employee
	Priya       string // champion, from E1; her champion_for edge closes at E3
	Marco       string // economic buyer, first appears at E3
	Events      []Event
	SignalID    string // emitted at E4
	EdgeChamp   string // relationships.id of Priya's champion_for edge
}

// At returns the occurred_at of event n (1-based).
func (w *World) At(n int) time.Time { return w.Events[n-1].At }

// Event returns event n (1-based).
func (w *World) Event(n int) Event { return w.Events[n-1] }

type script struct {
	from, body string
	field      claims.FieldPath
	value      string
}

var events = []script{
	{"priya.shah@acme.com", "Discovery call is booked. We start the pilot soon.", claims.FieldStage, StageDiscovery},
	{"priya.shah@acme.com", "Security review is pending. Please send the SOC2 report.", claims.FieldBlockers, BlockerSecurity},
	{"marco.buyer@acme.com", "We moved to Negotiation. Legal needs the MSA.", claims.FieldStage, StageNegotiation},
	{"priya.shah@acme.com", "Budget is frozen until Q4. Pausing for now.", claims.FieldBlockers, BlockerBudget},
	{"priya.shah@acme.com", "We signed off on the pilot. Thanks all.", claims.FieldStage, StageClosedWon},
}

// replayNow is the wall clock of the replay: after every event, so computed-at differs from world time.
var replayNow = time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)

// Seed resets the database and replays the five events, draining the recompute after each so every
// event has its own state version.
func Seed(t testing.TB, db *sql.DB) *World {
	t.Helper()
	ctx := context.Background()
	must(t, storetest.Purge(ctx, db, `TRUNCATE accounts, people, products, source_events CASCADE`))
	w := &World{DB: db}
	w.seedEntities(t)
	svc, err := ingest.NewService(db, ingest.Options{Clock: clock.NewFixed(replayNow), Debounce: time.Second, MaxWait: time.Second,
		Extension: graph.NewExtension()})
	must(t, err)
	coalClock := clock.NewFixed(replayNow.Add(time.Hour))
	hook, err := pipeline.New(pipeline.Options{Clock: coalClock}) // state diffs, signals and trigger evaluations, as in production
	must(t, err)
	co, err := coalesce.New(db, coalesce.Options{Clock: coalClock, WorkerID: "worldfixture", Extractor: extractor(), Hook: hook})
	must(t, err)
	for i, s := range events {
		ev := w.ingestEvent(t, svc, i+1, s)
		res, err := co.Drain(ctx)
		must(t, err)
		if len(res.Recomputes) != 1 {
			t.Fatalf("worldfixture: event %d produced %d recomputes", i+1, len(res.Recomputes))
		}
		ev.Version = res.Recomputes[0].Version
		w.Events = append(w.Events, ev)
		w.afterEvent(t, ev)
	}
	return w
}

func (w *World) seedEntities(t testing.TB) {
	w.Account = scalar(t, w.DB, `INSERT INTO accounts (name, domain) VALUES ('Acme Corp', 'acme.com') RETURNING id::text`)
	w.Opportunity = scalar(t, w.DB, `INSERT INTO opportunities (account_id, name, motion) VALUES ($1::uuid, 'Acme EU pilot', 'new_business') RETURNING id::text`, w.Account)
	w.Dana = scalar(t, w.DB, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Dana Kim', 'dana@ghostvendor.com') RETURNING id::text`)
	w.Priya = w.contact(t, "Priya Shah", "priya.shah@acme.com")
	w.Marco = w.contact(t, "Marco Buyer", "marco.buyer@acme.com")
	mapPerson(t, w.DB, w.Dana, "dana@ghostvendor.com")
}

func (w *World) contact(t testing.TB, name, email string) string {
	id := scalar(t, w.DB, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', $1, $2, $3::uuid) RETURNING id::text`, name, email, w.Account)
	mapPerson(t, w.DB, id, email)
	return id
}

func mapPerson(t testing.TB, db *sql.DB, id, email string) {
	_, err := db.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
 VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, id, email)
	must(t, err)
}

func (w *World) ingestEvent(t testing.TB, svc *ingest.Service, n int, s script) Event {
	at := time.Date(2026, 9, n, 10, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{
		"kind": "email", "direction": "inbound", "message_id": fmt.Sprintf("<e%d@acme.com>", n), "thread_id": "thr-1",
		"from": map[string]any{"email": s.from, "name": "Sender"},
		"to":   []map[string]any{{"email": "dana@ghostvendor.com", "name": "Dana Kim"}},
		"date": at.Format(time.RFC3339), "subject": "Re: pilot", "body_text": s.body,
	})
	must(t, err)
	res, err := svc.Ingest(context.Background(), normalize.SourceEvent{SourceSystem: "email", SourceObjectID: fmt.Sprintf("<e%d@acme.com>", n),
		SourceEventKey: "received", Connector: "gmail", ConnectorVersion: "1", OccurredAt: &at, Payload: payload})
	must(t, err)
	return Event{N: n, At: at, From: s.from, Body: s.body, ActivityID: res.ActivityID, SourceEventID: res.SourceEventID}
}

// afterEvent writes what the scripted story adds besides claims: the champion edge and its closing, the
// buyer's role, the signal. They go through the same tables and helpers the pipeline uses.
func (w *World) afterEvent(t testing.TB, ev Event) {
	ctx := context.Background()
	switch ev.N {
	case 1:
		w.EdgeChamp = scalar(t, w.DB, `INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, source_activity_id, valid_from)
 VALUES ('person', $1::uuid, 'champion_for', 'opportunity', $2::uuid, 'first_party_ai', 0.8, $3::uuid, $4) RETURNING id::text`, w.Priya, w.Opportunity, ev.ActivityID, ev.At)
	case 3:
		src, dst := graph.EntityRef{Type: graph.EntityPerson, ID: w.Priya}, graph.EntityRef{Type: graph.EntityOpportunity, ID: w.Opportunity}
		n, err := graph.CloseEdges(ctx, w.DB, graph.EdgeFilter{Src: &src, Dst: &dst, Rels: []string{"champion_for"}}, ev.At)
		must(t, err)
		if n != 1 {
			t.Fatalf("worldfixture: closed %d champion edges", n)
		}
		_, err = w.DB.Exec(`INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, source_activity_id, valid_from)
 VALUES ('person', $1::uuid, 'economic_buyer_for', 'opportunity', $2::uuid, 'crm_explicit', 0.9, $3::uuid, $4)`, w.Marco, w.Opportunity, ev.ActivityID, ev.At)
		must(t, err)
	case 4:
		w.SignalID = scalar(t, w.DB, `INSERT INTO signals (account_id, signal_type, rule, details, evidence_refs, occurred_at)
 VALUES ($1::uuid, 'customer_replied', 'sig.reply@1', '{}', jsonb_build_array(jsonb_build_object('activity_id', $2::text)), $3) RETURNING id::text`, w.Account, ev.ActivityID, ev.At)
	}
}

// extractor answers each email with the one fact the script gives it, quoting the first sentence.
func extractor() *claimstest.FakeExtractor {
	byBody := map[string]script{}
	for _, s := range events {
		byBody[s.body] = s
	}
	return &claimstest.FakeExtractor{Candidates: func(req claims.ExtractRequest) []claims.Candidate {
		s, ok := byBody[strings.TrimSpace(req.Text)]
		if !ok {
			return nil
		}
		quote := strings.SplitN(s.body, ".", 2)[0]
		return []claims.Candidate{{FieldPath: s.field, Value: json.RawMessage(`"` + s.value + `"`), Confidence: 0.9, EvidenceQuote: quote}}
	}}
}

// NewRun inserts an open dry_run for the account triggered by event n, at that event's state version.
func (w *World) NewRun(t testing.TB, n int) string {
	t.Helper()
	ev := w.Event(n)
	_, err := w.DB.Exec(`UPDATE agent_runs SET status = 'cancelled' WHERE account_id = $1::uuid AND status IN ('pending', 'context_built', 'drafted')`, w.Account)
	must(t, err)
	eval := scalar(t, w.DB, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation)
 VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], 'worldfixture') RETURNING id::text`, w.Account)
	return scalar(t, w.DB, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids, state_version)
 VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', 'context_built', $2::uuid, ARRAY[$3::uuid], $4) RETURNING id::text`,
		w.Account, eval, ev.ActivityID, ev.Version)
}

func scalar(t testing.TB, db *sql.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("worldfixture: %.80s: %v", q, err)
	}
	return s
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("worldfixture: %v", err)
	}
}
