package slacksurface

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// memEvents is core's outbox in memory: unacknowledged events, and a record of acknowledgements.
type memEvents struct {
	mu      sync.Mutex
	pending []OutboxEvent
	acked   []int64
	listErr error
	ackErr  error
}

func (m *memEvents) PendingEvents(_ context.Context, consumer string, _ int) ([]OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	if consumer != RelayConsumer {
		return nil, errors.New("wrong consumer " + consumer)
	}
	return append([]OutboxEvent(nil), m.pending...), nil
}

func (m *memEvents) AckEvent(_ context.Context, _ string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ackErr != nil {
		return m.ackErr
	}
	m.acked = append(m.acked, id)
	kept := m.pending[:0]
	for _, e := range m.pending {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	m.pending = kept
	return nil
}

func (m *memEvents) ackedIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.acked...)
}

func publishedEvent(id int64, run string) OutboxEvent {
	return OutboxEvent{ID: id, Topic: TopicStrategySetPublished, AgentRunID: run, StrategySetID: "s", DecisionEpisodeID: "e", AccountID: "a"}
}

// flakyCore fails GetStrategies with failWith until failures runs out (for onlyRun when it is set, else for any run).
type flakyCore struct {
	*memCore
	mu       sync.Mutex
	failures int
	failWith error
	onlyRun  string
	calls    int
}

func (c *flakyCore) GetStrategies(ctx context.Context, run string) (RunStrategies, error) {
	c.mu.Lock()
	c.calls++
	fail := c.failures > 0 && (c.onlyRun == "" || c.onlyRun == run)
	if fail {
		c.failures--
	}
	c.mu.Unlock()
	if fail {
		return RunStrategies{}, c.failWith
	}
	return c.memCore.GetStrategies(ctx, run)
}

func newRelay(t *testing.T, core Core, src EventSource) (*Relay, *fakePoster) {
	t.Helper()
	poster := &fakePoster{}
	r, err := NewRelay(src, NewPublisher(core, poster, "C0TEST", ""), quietLog(), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return r, poster
}

func TestARelayPostsMessage2WithTheRealSetAndThenAcknowledges(t *testing.T) {
	core, src := newMemCore(), &memEvents{pending: []OutboxEvent{publishedEvent(7, "run-1")}}
	r, poster := newRelay(t, core, src)

	n, err := r.Drain(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("drain = %d, %v", n, err)
	}
	if len(poster.posts) != 1 || !strings.Contains(poster.posts[0].Text, "I see 3 reasonable paths.") {
		t.Fatalf("posts = %+v", poster.posts)
	}
	if got := src.ackedIDs(); len(got) != 1 || got[0] != 7 {
		t.Fatalf("acked = %v, want [7]", got)
	}
	if n, _ := r.Drain(context.Background()); n != 0 || len(poster.posts) != 1 {
		t.Fatalf("an acknowledged event was handled again (%d, %d posts)", n, len(poster.posts))
	}
}

func TestAnEventThatFailsStaysUnacknowledgedAndIsRetriedWithBackoff(t *testing.T) {
	core := &flakyCore{memCore: newMemCore(), failures: 2, failWith: ErrNotReady, onlyRun: "run-1"}
	src := &memEvents{pending: []OutboxEvent{publishedEvent(1, "run-1"), publishedEvent(2, "run-2")}}
	r, poster := newRelay(t, core, src)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	// pass 1: event 1 fails (not ready), event 2 still goes out: one bad event never blocks the others
	if n, err := r.Drain(context.Background()); err != nil || n != 1 {
		t.Fatalf("pass 1 = %d, %v", n, err)
	}
	if got := src.ackedIDs(); len(got) != 1 || got[0] != 2 || len(poster.posts) != 1 {
		t.Fatalf("after pass 1: acked %v, %d posts", got, len(poster.posts))
	}
	calls := core.calls
	// pass 2 immediately: inside event 1's backoff, so core is not asked again
	if n, _ := r.Drain(context.Background()); n != 0 || core.calls != calls {
		t.Fatalf("pass 2 retried inside the backoff: posted %d, core calls %d -> %d", n, calls, core.calls)
	}
	// pass 3 after the backoff: fails once more (second scripted failure), the wait grows
	now = now.Add(time.Minute)
	if n, _ := r.Drain(context.Background()); n != 0 {
		t.Fatalf("pass 3 posted %d", n)
	}
	if first := r.tries[tryKeyOf(publishedEvent(1, "run-1"))]; first.attempts != 2 {
		t.Fatalf("attempts = %d, want 2", first.attempts)
	}
	// pass 4 after that backoff: core is ready, the event posts and is acknowledged
	now = now.Add(time.Minute)
	if n, _ := r.Drain(context.Background()); n != 1 {
		t.Fatalf("pass 4 posted %d", n)
	}
	if got := src.ackedIDs(); len(got) != 2 || len(poster.posts) != 2 {
		t.Fatalf("acked %v, %d posts", got, len(poster.posts))
	}
	if _, left := r.tries[tryKeyOf(publishedEvent(1, "run-1"))]; left {
		t.Fatal("a handled event keeps its retry state")
	}
}

func TestAChooserPostFailureIsNotAcknowledged(t *testing.T) {
	core, src := newMemCore(), &memEvents{pending: []OutboxEvent{publishedEvent(3, "run-1")}}
	r, poster := newRelay(t, core, src)
	r.pub.core = &flakyCore{memCore: core, failures: 1, failWith: errors.New("core 502")}
	if n, _ := r.Drain(context.Background()); n != 0 || len(src.ackedIDs()) != 0 || len(poster.posts) != 0 {
		t.Fatalf("a failed post must not be acknowledged: posted %d, acked %v", n, src.ackedIDs())
	}
}

func TestADeliveryRepeatedAfterALostAckPostsOnceAndAcknowledgesAgain(t *testing.T) {
	core, src := newMemCore(), &memEvents{pending: []OutboxEvent{publishedEvent(4, "run-1")}}
	r, poster := newRelay(t, core, src)
	src.ackErr = errors.New("core unreachable") // the post succeeds, the acknowledgement is lost
	if n, _ := r.Drain(context.Background()); n != 0 || len(poster.posts) != 1 {
		t.Fatalf("first delivery: posted %d acks, %d messages", n, len(poster.posts))
	}
	src.ackErr = nil
	r.now = func() time.Time { return time.Now().Add(time.Hour) } // past the backoff
	if n, _ := r.Drain(context.Background()); n != 1 || len(poster.posts) != 1 {
		t.Fatalf("redelivery: %d acks, %d messages; the publisher's guard must stop a second Message 2", n, len(poster.posts))
	}
	if got := src.ackedIDs(); len(got) != 1 || got[0] != 4 {
		t.Fatalf("acked = %v", got)
	}
}

func TestAnUnknownTopicIsAcknowledgedWithoutPosting(t *testing.T) {
	core, src := newMemCore(), &memEvents{pending: []OutboxEvent{{ID: 9, Topic: "run.archived", AgentRunID: "run-1"}}}
	r, poster := newRelay(t, core, src)
	if n, _ := r.Drain(context.Background()); n != 1 || len(poster.posts) != 0 || len(src.ackedIDs()) != 1 {
		t.Fatalf("posted %d, messages %d, acked %v", n, len(poster.posts), src.ackedIDs())
	}
}

func TestAnOutboxReadFailureIsReturned(t *testing.T) {
	src := &memEvents{listErr: errors.New("core down")}
	r, _ := newRelay(t, newMemCore(), src)
	if _, err := r.Drain(context.Background()); err == nil {
		t.Fatal("a failed read of the outbox was swallowed")
	}
}

func TestRunPostsAsEventsArriveAndStopsWhenCancelled(t *testing.T) {
	core, src := newMemCore(), &memEvents{}
	r, poster := newRelay(t, core, src)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	src.mu.Lock()
	src.pending = append(src.pending, publishedEvent(11, "run-1"))
	src.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	for len(src.ackedIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
	if len(poster.posts) != 1 || len(src.ackedIDs()) != 1 {
		t.Fatalf("%d messages, acked %v", len(poster.posts), src.ackedIDs())
	}
}

func TestTheRelaySettingsComeFromTheEnvironment(t *testing.T) {
	cfg, err := LoadConfig(env(goodEnv()))
	if err != nil || cfg.RelayOff || cfg.RelayPoll != 0 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	m := goodEnv()
	m[EnvOutbox], m[EnvOutboxPollMS] = " OFF ", "250"
	cfg, err = LoadConfig(env(m))
	if err != nil || !cfg.RelayOff || cfg.RelayPoll != 250*time.Millisecond {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
	for _, bad := range []string{"0", "-5", "soon"} {
		m[EnvOutboxPollMS] = bad
		if _, err := LoadConfig(env(m)); err == nil || !strings.Contains(err.Error(), EnvOutboxPollMS) {
			t.Errorf("%s: err = %v, want a refusal naming %s", bad, err, EnvOutboxPollMS)
		}
	}
}

func TestNewRelayValidates(t *testing.T) {
	pub := NewPublisher(newMemCore(), &fakePoster{}, "C", "")
	src := &memEvents{}
	if _, err := NewRelay(nil, pub, nil, 0); err == nil {
		t.Error("nil source accepted")
	}
	if _, err := NewRelay(src, nil, nil, 0); err == nil {
		t.Error("nil publisher accepted")
	}
	if _, err := NewRelay(src, pub, nil, -time.Second); err == nil {
		t.Error("negative poll accepted")
	}
	r, err := NewRelay(src, pub, nil, 0)
	if err != nil || r.poll != DefaultRelayPoll || r.log == nil {
		t.Fatalf("defaults: %+v, %v", r, err)
	}
}

func TestCoreHTTPReadsAndAcknowledgesTheOutbox(t *testing.T) {
	var gotAuth, gotQuery, ackPath, ackQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/outbox/events":
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"items":[{"id":5,"topic":"strategy_set.published","agent_run_id":"r","strategy_set_id":"s","decision_episode_id":"e","account_id":"a","created_at":"2026-10-03T10:00:00Z"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ack"):
			ackPath, ackQuery = r.URL.Path, r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewCoreHTTP(srv.URL, Secret(testToken), nil)

	events, err := c.PendingEvents(context.Background(), "slack", 25)
	if err != nil || len(events) != 1 || events[0].ID != 5 || events[0].AgentRunID != "r" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if gotAuth != "Bearer "+testToken || !strings.Contains(gotQuery, "consumer=slack") || !strings.Contains(gotQuery, "limit=25") ||
		strings.Contains(gotQuery, "topic=") { // no topic: every topic, so Message 1 and Message 2 events come in id order
		t.Fatalf("auth %q query %q", gotAuth, gotQuery)
	}
	if err := c.AckEvent(context.Background(), "slack", 5); err != nil || ackPath != "/outbox/events/5/ack" || ackQuery != "consumer=slack" {
		t.Fatalf("ack: %v path %q query %q", err, ackPath, ackQuery)
	}
}

func TestCoreHTTPOutboxErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal error"}}`))
	}))
	defer srv.Close()
	c := NewCoreHTTP(srv.URL, Secret(testToken), nil)
	if _, err := c.PendingEvents(context.Background(), "slack", 5); err == nil {
		t.Error("a 500 on the feed was swallowed")
	}
	if err := c.AckEvent(context.Background(), "slack", 5); err == nil {
		t.Error("a 500 on the ack was swallowed")
	}
}

// A restore reuses outbox ids: an event that failed in the world before the restore must not delay the new world's event
// that happens to have the same id (the retry state is keyed by the event's own subject too).
func TestRetryStateIsNotSharedBetweenWorldsThatReuseAnOutboxID(t *testing.T) {
	core := &flakyCore{memCore: newMemCore(), failures: 1, failWith: ErrNotReady, onlyRun: "run-old"}
	src := &memEvents{pending: []OutboxEvent{publishedEvent(1, "run-old")}}
	r, poster := newRelay(t, core, src)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	if n, _ := r.Drain(context.Background()); n != 0 {
		t.Fatalf("the old world's event fails: posted %d", n)
	}
	src.mu.Lock()
	src.pending = []OutboxEvent{publishedEvent(1, "run-new")} // the restored world's first event, same id, inside the old backoff
	src.mu.Unlock()
	if n, _ := r.Drain(context.Background()); n != 1 || len(poster.posts) != 1 {
		t.Fatalf("the new world's event must not wait out the old one's backoff: posted %d, %d messages", n, len(poster.posts))
	}
}
