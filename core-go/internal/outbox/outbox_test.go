package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/outbox"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

func store(t *testing.T) *outbox.Store {
	t.Helper()
	s, err := outbox.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// seeded publishes a StrategySet through the same insert the orchestrator's publish transaction does.
func seeded(t *testing.T) strategytest.Seeded {
	t.Helper()
	return strategytest.Seed(t, env.DB, ctxfixture.Get(t, env.DB).AccountA)
}

func pendingFor(t *testing.T, consumer string) []outbox.Event {
	t.Helper()
	ev, err := store(t).Pending(bg, consumer, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func find(events []outbox.Event, runID string) (outbox.Event, bool) {
	for _, e := range events {
		if e.AgentRunID == runID {
			return e, true
		}
	}
	return outbox.Event{}, false
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := outbox.New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}

func TestInsertingAStrategySetWritesExactlyOneEventInTheSameTransaction(t *testing.T) {
	s := seeded(t)
	e, ok := find(pendingFor(t, "slack"), s.RunID)
	if !ok {
		t.Fatalf("no event for run %s", s.RunID)
	}
	if e.Topic != outbox.TopicStrategySetPublished || e.StrategySetID != s.SetID || e.DecisionEpisodeID != s.EpisodeID || e.AccountID != s.AccountID {
		t.Fatalf("event = %+v, seeded = %+v", e, s)
	}
	var n int
	if err := env.DB.QueryRow(`SELECT count(*) FROM outbox_events WHERE agent_run_id = $1::uuid`, s.RunID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events for the run = %d (%v), want 1", n, err)
	}
}

func TestARolledBackPublishLeavesNoEvent(t *testing.T) {
	// The orchestrator's publish transaction and the trigger are one transaction: the event is as atomic as the set.
	tx, err := env.DB.BeginTx(bg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var before int
	_ = env.DB.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&before)
	if _, err := tx.Exec(`INSERT INTO outbox_events (topic, agent_run_id, strategy_set_id, decision_episode_id, account_id)
 VALUES ('strategy_set.published', gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), gen_random_uuid())`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = env.DB.QueryRow(`SELECT count(*) FROM outbox_events`).Scan(&after)
	if after != before {
		t.Fatalf("events %d -> %d after a rollback", before, after)
	}
}

func TestEventsConformToTheContract(t *testing.T) {
	s := seeded(t)
	all := pendingFor(t, "slack")
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	set, _ := find(all, s.RunID)
	bi, ok := findBI(all, s.BIID)
	if !ok {
		t.Fatalf("no bi_update.published event for update %s", s.BIID)
	}
	for _, e := range []outbox.Event{set, bi} {
		doc, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Validate("outbox_event", doc); err != nil {
			t.Fatalf("%s: %v %s", e.Topic, err, doc)
		}
	}
	// the two shapes are exclusive: a bi event has no run, a set event is not a bi event
	for name, doc := range map[string]string{
		"a bi event with a run":     `{"id":1,"topic":"bi_update.published","account_id":"` + s.AccountID + `","created_at":"2026-10-04T10:00:00Z","bi_update_id":"` + s.BIID + `","agent_run_id":"` + s.RunID + `"}`,
		"a bi event without update": `{"id":1,"topic":"bi_update.published","account_id":"` + s.AccountID + `","created_at":"2026-10-04T10:00:00Z"}`,
		"a set event without a run": `{"id":1,"topic":"strategy_set.published","account_id":"` + s.AccountID + `","created_at":"2026-10-04T10:00:00Z","strategy_set_id":"` + s.SetID + `","decision_episode_id":"` + s.EpisodeID + `"}`,
		"an unknown topic":          `{"id":1,"topic":"run.archived","account_id":"` + s.AccountID + `","created_at":"2026-10-04T10:00:00Z"}`,
	} {
		if err := v.Validate("outbox_event", []byte(doc)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func findBI(events []outbox.Event, biID string) (outbox.Event, bool) {
	for _, e := range events {
		if e.Topic == outbox.TopicBIUpdatePublished && e.BIUpdateID == biID {
			return e, true
		}
	}
	return outbox.Event{}, false
}

func TestInsertingABIUpdateWritesExactlyOneEventForItAndTheSetEventNamesIt(t *testing.T) {
	s := seeded(t)
	all := pendingFor(t, "slack")
	bi, ok := findBI(all, s.BIID)
	if !ok || bi.AccountID != s.AccountID || bi.AgentRunID != "" || bi.StrategySetID != "" || bi.DecisionEpisodeID != "" {
		t.Fatalf("bi event = %+v (found %v)", bi, ok)
	}
	var n int
	if err := env.DB.QueryRow(`SELECT count(*) FROM outbox_events WHERE bi_update_id = $1::uuid`, s.BIID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("events for the update = %d (%v), want 1", n, err)
	}
	set, _ := find(all, s.RunID)
	if set.BIUpdateID != s.BIID {
		t.Fatalf("the set event names update %q, want its episode's update %s", set.BIUpdateID, s.BIID)
	}
	if bi.ID >= set.ID {
		t.Fatalf("the update (event %d) is announced after the set (event %d)", bi.ID, set.ID)
	}
}

func TestTopicsAreFilteredAndNoTopicMeansEveryTopicInIDOrder(t *testing.T) {
	s := seeded(t)
	only, err := store(t).Pending(bg, "topics", outbox.TopicBIUpdatePublished, outbox.MaxLimit)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range only {
		if e.Topic != outbox.TopicBIUpdatePublished {
			t.Fatalf("a bi-only listing returned %s", e.Topic)
		}
	}
	if _, ok := findBI(only, s.BIID); !ok {
		t.Fatal("the update's event is missing from its topic")
	}
	both := pendingFor(t, "topics")
	last, topics := int64(0), map[string]bool{}
	for _, e := range both {
		if e.ID <= last {
			t.Fatalf("not in id order: %d after %d", e.ID, last)
		}
		last, topics[e.Topic] = e.ID, true
	}
	if !topics[outbox.TopicBIUpdatePublished] || !topics[outbox.TopicStrategySetPublished] {
		t.Fatalf("an unfiltered listing has topics %v", topics)
	}
}

func TestTheDatabaseRefusesAnEventOfTheWrongShape(t *testing.T) {
	for name, q := range map[string]string{
		"a bi event with a run":     `INSERT INTO outbox_events (topic, bi_update_id, agent_run_id, account_id) VALUES ('bi_update.published', gen_random_uuid(), gen_random_uuid(), gen_random_uuid())`,
		"a bi event without update": `INSERT INTO outbox_events (topic, account_id) VALUES ('bi_update.published', gen_random_uuid())`,
		"a set event without a set": `INSERT INTO outbox_events (topic, agent_run_id, account_id) VALUES ('strategy_set.published', gen_random_uuid(), gen_random_uuid())`,
		"an unknown topic":          `INSERT INTO outbox_events (topic, account_id) VALUES ('run.archived', gen_random_uuid())`,
	} {
		if _, err := env.DB.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAnAcknowledgementIsPerConsumerAndIdempotent(t *testing.T) {
	s := seeded(t)
	e, _ := find(pendingFor(t, "slack"), s.RunID)
	for i := 0; i < 2; i++ { // twice: idempotent
		if err := store(t).Ack(bg, "slack", e.ID); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	if _, ok := find(pendingFor(t, "slack"), s.RunID); ok {
		t.Fatal("an acknowledged event is still pending for its consumer")
	}
	if _, ok := find(pendingFor(t, "web"), s.RunID); !ok {
		t.Fatal("one consumer's acknowledgement hid the event from another consumer")
	}
}

func TestPendingIsOldestFirstAndHonoursTheLimit(t *testing.T) {
	first, second := seeded(t), seeded(t)
	all := pendingFor(t, "ordering")
	var iFirst, iSecond = -1, -1
	for i, e := range all {
		switch e.AgentRunID {
		case first.RunID:
			iFirst = i
		case second.RunID:
			iSecond = i
		}
	}
	if iFirst < 0 || iSecond < 0 || iFirst > iSecond {
		t.Fatalf("order: first at %d, second at %d", iFirst, iSecond)
	}
	one, err := store(t).Pending(bg, "ordering", outbox.TopicStrategySetPublished, 1)
	if err != nil || len(one) != 1 || one[0].Topic != outbox.TopicStrategySetPublished {
		t.Fatalf("limit 1 = %+v (%v)", one, err)
	}
	for _, e := range all {
		if e.Topic == outbox.TopicStrategySetPublished {
			if one[0].ID != e.ID {
				t.Fatalf("limit 1 returned event %d, the oldest set event is %d", one[0].ID, e.ID)
			}
			break
		}
	}
	if two, err := store(t).Pending(bg, "ordering", "", 2); err != nil || len(two) != 2 || two[0].ID != all[0].ID || two[1].ID != all[1].ID {
		t.Fatalf("limit 2 over every topic = %+v (%v)", two, err)
	}
}

func TestInvalidRequestsAreRefusedWithErrInvalid(t *testing.T) {
	s := store(t)
	for name, call := range map[string]func() error{
		"empty consumer":      func() error { _, err := s.Pending(bg, "", "", 0); return err },
		"upper-case consumer": func() error { _, err := s.Pending(bg, "Slack", "", 0); return err },
		"sql in consumer":     func() error { _, err := s.Pending(bg, "x'; drop table outbox_events;--", "", 0); return err },
		"long consumer":       func() error { _, err := s.Pending(bg, strings.Repeat("a", 33), "", 0); return err },
		"unknown topic":       func() error { _, err := s.Pending(bg, "slack", "run.deleted", 0); return err },
		"negative limit":      func() error { _, err := s.Pending(bg, "slack", "", -1); return err },
		"limit above max":     func() error { _, err := s.Pending(bg, "slack", "", outbox.MaxLimit+1); return err },
		"ack bad consumer":    func() error { return s.Ack(bg, "", 1) },
	} {
		if err := call(); !errors.Is(err, outbox.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestAcknowledgingAnUnknownEventIsNotFound(t *testing.T) {
	if err := store(t).Ack(bg, "slack", 9_999_999_999); !errors.Is(err, outbox.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestADatabaseErrorIsReportedNotSwallowed(t *testing.T) {
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := store(t).Pending(ctx, "slack", "", 0); err == nil {
		t.Fatal("a cancelled context must fail the read")
	}
	if err := store(t).Ack(ctx, "slack", 1); err == nil {
		t.Fatal("a cancelled context must fail the ack")
	}
}
