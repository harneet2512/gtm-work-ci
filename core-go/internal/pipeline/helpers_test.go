package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

const (
	debounce = 3 * time.Second
	maxWait  = 15 * time.Second
)

var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func resetAll(t *testing.T) {
	t.Helper()
	err := storetest.Purge(context.Background(), env.DB, `TRUNCATE agent_runs, trigger_evaluations, signals, state_diffs, state_history, account_state, recompute_jobs, claims,
		extraction_cache, unresolved_activities, activity_participants, activities, source_events, entity_source_mappings, relationships,
		opportunities, people, accounts RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

func count(t *testing.T, table string) string {
	t.Helper()
	return scalar(t, `SELECT count(*)::text FROM `+table)
}

type world struct{ account, dana, priya string }

// seedWorld creates Acme (acme.com), Dana (employee) and Priya (contact) with email mappings.
func seedWorld(t *testing.T) world {
	t.Helper()
	resetAll(t)
	w := world{}
	w.account = scalar(t, `INSERT INTO accounts (name, domain) VALUES ('Acme Corp', 'acme.com') RETURNING id::text`)
	w.dana = scalar(t, `INSERT INTO people (kind, display_name, primary_email) VALUES ('employee', 'Dana Kim', 'dana@vendor.example') RETURNING id::text`)
	w.priya = scalar(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Priya Shah', 'priya.shah@acme.com', $1::uuid) RETURNING id::text`, w.account)
	for _, m := range [][2]string{{w.dana, "dana@vendor.example"}, {w.priya, "priya.shah@acme.com"}} {
		if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
			VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func email(t *testing.T, n int, at time.Time, direction, body string) normalize.SourceEvent {
	t.Helper()
	from, to := "priya.shah@acme.com", "dana@vendor.example"
	if direction == "outbound" {
		from, to = to, from
	}
	payload, _ := json.Marshal(map[string]any{
		"kind": "email", "direction": direction, "message_id": fmt.Sprintf("<m%d@x.com>", n), "thread_id": "thr-1",
		"from": map[string]any{"email": from}, "to": []map[string]any{{"email": to}},
		"date": at.Format(time.RFC3339), "subject": "Re: rollout", "body_text": body,
	})
	event := "received"
	if direction == "outbound" {
		event = "sent"
	}
	return normalize.SourceEvent{SourceSystem: "email", SourceObjectID: fmt.Sprintf("<m%d@x.com>", n), SourceEventKey: event,
		Connector: "gmail", ConnectorVersion: "1", OccurredAt: &at, Payload: payload}
}

func ingestAll(t *testing.T, svc *ingest.Service, evs ...normalize.SourceEvent) []string {
	t.Helper()
	var ids []string
	for _, ev := range evs {
		res, err := svc.Ingest(context.Background(), ev)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		ids = append(ids, res.ActivityID)
	}
	return ids
}

// blockerExtractor emits one blocker per email whose text asks for something ("need"), quoting its first sentence.
func blockerExtractor() *claimstest.FakeExtractor {
	return &claimstest.FakeExtractor{Candidates: func(req claims.ExtractRequest) []claims.Candidate {
		if !strings.Contains(req.Text, "need") {
			return nil
		}
		quote := strings.SplitN(req.Text, ".", 2)[0]
		return []claims.Candidate{{FieldPath: claims.FieldBlockers, Value: json.RawMessage(`"` + quote + `"`), Confidence: 0.9, EvidenceQuote: quote}}
	}}
}

// stack is ingest + coalesce with the WP8 pipeline hooked in, on fixed clocks.
type stack struct {
	ingestClock *clock.Fixed
	coalClock   *clock.Fixed
	svc         *ingest.Service
	co          *coalesce.Service
}

func newStack(t *testing.T, mode string) *stack {
	t.Helper()
	s := &stack{ingestClock: clock.NewFixed(t0), coalClock: clock.NewFixed(t0)}
	var err error
	if s.svc, err = ingest.NewService(env.DB, ingest.Options{Clock: s.ingestClock, Debounce: debounce, MaxWait: maxWait}); err != nil {
		t.Fatal(err)
	}
	p, err := pipeline.New(pipeline.Options{RunMode: mode, Clock: s.coalClock})
	if err != nil {
		t.Fatal(err)
	}
	if s.co, err = coalesce.New(env.DB, coalesce.Options{Extractor: blockerExtractor(), Hook: p, Clock: s.coalClock, WorkerID: "w1"}); err != nil {
		t.Fatal(err)
	}
	return s
}

// drain moves the coalesce clock past the debounce window and recomputes every due job.
func (s *stack) drain(t *testing.T, at time.Time) {
	t.Helper()
	s.coalClock.Set(at)
	res, err := s.co.Drain(context.Background())
	if err != nil || res.Failed != 0 {
		t.Fatalf("drain: %+v %v", res, err)
	}
}

func onlyRun(t *testing.T) string {
	t.Helper()
	if n := count(t, "agent_runs"); n != "1" {
		t.Fatalf("agent_runs = %s, want 1", n)
	}
	return scalar(t, `SELECT id::text FROM agent_runs`)
}

func newPipeline(t *testing.T, mode string) *pipeline.Pipeline {
	t.Helper()
	p, err := pipeline.New(pipeline.Options{RunMode: mode, Clock: clock.NewFixed(t0.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
