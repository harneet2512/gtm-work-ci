package contracttest

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

type generationDoc struct {
	ID         string `json:"id"`
	Generation struct {
		Phase             string  `json:"phase"`
		Attempt           int     `json:"attempt"`
		Reason            *string `json:"reason"`
		StrategySetID     *string `json:"strategy_set_id"`
		DecisionEpisodeID *string `json:"decision_episode_id"`
	} `json:"generation"`
}

func decodeRun(t *testing.T, r reply) generationDoc {
	t.Helper()
	var d generationDoc
	if err := json.Unmarshal(r.body, &d); err != nil {
		t.Fatalf("not a run: %s", clip(r.body))
	}
	return d
}

type outboxPage struct {
	Items []struct {
		ID         int64  `json:"id"`
		Topic      string `json:"topic"`
		AgentRunID string `json:"agent_run_id"`
	} `json:"items"`
}

func eventsFor(t *testing.T, s *stack, consumer, runID string) (int64, bool) {
	t.Helper()
	r := s.get("/outbox/events?consumer="+consumer+"&limit=200", "/outbox/events")
	if r.status != 200 {
		t.Fatalf("list: %d %s", r.status, clip(r.body))
	}
	var page outboxPage
	if err := json.Unmarshal(r.body, &page); err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Items {
		if e.AgentRunID == runID {
			return e.ID, true
		}
	}
	return 0, false
}

// TestRunStatusAndOutboxConformToTheContract: GET /runs/{id} with its generation status, and the outbox feed
// surfaces react to (HAR-117, WP19).
func TestRunStatusAndOutboxConformToTheContract(t *testing.T) {
	s := newStack(t)
	run := "/runs/{run_id}"

	queued := ctxfixture.FreshRun(t, env.DB, s.world.AccountB, "pending")
	r := s.get("/runs/"+queued, run)
	if d := decodeRun(t, r); r.status != 200 || d.ID != queued || d.Generation.Phase != "queued" || d.Generation.StrategySetID != nil {
		t.Fatalf("queued run: %d %s", r.status, clip(r.body))
	}

	seeded := strategytest.Seed(t, env.DB, s.world.AccountA)
	r = s.get("/runs/"+seeded.RunID, run)
	d := decodeRun(t, r)
	if r.status != 200 || d.Generation.Phase != "published" || d.Generation.StrategySetID == nil || *d.Generation.StrategySetID != seeded.SetID ||
		d.Generation.DecisionEpisodeID == nil || *d.Generation.DecisionEpisodeID != seeded.EpisodeID {
		t.Fatalf("published run: %d %s", r.status, clip(r.body))
	}

	if r := s.get("/runs/"+missingID, run); r.status != 404 {
		t.Fatalf("unknown run: %d", r.status)
	}
	if r := s.get("/runs/not-a-uuid", run); r.status != 404 {
		t.Fatalf("malformed run id: %d", r.status)
	}
	if r := s.do("GET", "/runs/"+queued, run, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("DELETE", "/runs/"+queued, "", apiToken, nil); r.status != 405 {
		t.Fatalf("method: %d", r.status)
	}

	// The outbox: the published set is announced once, per consumer, until acknowledged.
	id, ok := eventsFor(t, s, "slack", seeded.RunID)
	if !ok {
		t.Fatalf("no strategy_set.published event for run %s", seeded.RunID)
	}
	ack := "/outbox/events/" + strconv.FormatInt(id, 10) + "/ack?consumer=slack"
	const ackTemplate = "/outbox/events/{event_id}/ack"
	for i := 0; i < 2; i++ {
		if r := s.do("POST", ack, ackTemplate, apiToken, nil); r.status != 204 {
			t.Fatalf("ack %d: %d %s", i, r.status, clip(r.body))
		}
	}
	if _, still := eventsFor(t, s, "slack", seeded.RunID); still {
		t.Fatal("an acknowledged event is still listed")
	}
	if _, other := eventsFor(t, s, "web", seeded.RunID); !other {
		t.Fatal("another consumer lost the event")
	}
	// the update (Message 1) has its own topic; the unfiltered feed carries both topics
	for topic, want := range map[string]bool{"": true, "bi_update.published": true, "strategy_set.published": false} {
		path := "/outbox/events?consumer=bi-check&limit=200"
		if topic != "" {
			path += "&topic=" + topic
		}
		r := s.get(path, "/outbox/events")
		if r.status != 200 || (want && !strings.Contains(string(r.body), `"bi_update_id":"`+seeded.BIID+`"`)) {
			t.Fatalf("topic %q: %d %s", topic, r.status, clip(r.body))
		}
		if topic == "strategy_set.published" && strings.Contains(string(r.body), "bi_update.published") {
			t.Fatalf("a set-only feed carries a bi event: %s", clip(r.body))
		}
	}
	for name, path := range map[string]string{
		"no consumer": "/outbox/events", "bad consumer": "/outbox/events?consumer=Slack%21", "bad limit": "/outbox/events?consumer=slack&limit=x",
		"limit range": "/outbox/events?consumer=slack&limit=0", "bad topic": "/outbox/events?consumer=slack&topic=nope",
	} {
		if r := s.get(path, "/outbox/events"); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, clip(r.body))
		}
	}
	for name, c := range map[string]struct {
		path   string
		status int
	}{
		"unknown event": {"/outbox/events/999999999/ack?consumer=slack", 404},
		"bad event id":  {"/outbox/events/abc/ack?consumer=slack", 400},
		"zero event id": {"/outbox/events/0/ack?consumer=slack", 400},
		"no consumer":   {"/outbox/events/" + strconv.FormatInt(id, 10) + "/ack", 400},
	} {
		if r := s.do("POST", c.path, ackTemplate, apiToken, nil); r.status != c.status {
			t.Errorf("%s: %d %s, want %d", name, r.status, clip(r.body), c.status)
		}
	}
	if r := s.do("GET", "/outbox/events?consumer=slack", "/outbox/events", "", nil); r.status != 401 {
		t.Fatalf("no token on the feed: %d", r.status)
	}
	if r := s.do("POST", ack, ackTemplate, "wrong-token", nil); r.status != 401 {
		t.Fatalf("no token on ack: %d", r.status)
	}
	if r := s.do("DELETE", "/outbox/events", "", apiToken, nil); r.status != 405 {
		t.Fatalf("method on the feed: %d", r.status)
	}
	if strings.Contains(string(s.logs.Bytes()), "outbox failed") {
		t.Fatalf("unexpected server errors: %s", s.logs.String())
	}
}
