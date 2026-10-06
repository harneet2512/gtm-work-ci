package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Relay constants.
const (
	// RelayConsumer is the name this surface acknowledges core's outbox events under.
	RelayConsumer = "slack"
	// TopicStrategySetPublished is core's event for a committed StrategySet (contracts/schemas/outbox_event.v1.json).
	TopicStrategySetPublished = "strategy_set.published"
	// TopicBIUpdatePublished is core's event for a committed business-intelligence update (Message 1).
	TopicBIUpdatePublished = "bi_update.published"
	// DefaultRelayPoll is how often the relay asks core for unacknowledged events.
	DefaultRelayPoll = 2 * time.Second
	// maxRelayBackoff caps the wait before an event that failed is tried again.
	maxRelayBackoff = time.Minute
	relayBatch      = 50
)

// OutboxEvent is contracts/schemas/outbox_event.v1.json.
type OutboxEvent struct {
	ID                int64  `json:"id"`
	Topic             string `json:"topic"`
	AgentRunID        string `json:"agent_run_id,omitempty"`
	StrategySetID     string `json:"strategy_set_id,omitempty"`
	DecisionEpisodeID string `json:"decision_episode_id,omitempty"`
	// BIUpdateID is the update of a bi_update.published event, and on a strategy_set.published event the update
	// (Message 1) of the episode, which Message 2 must not be posted before.
	BIUpdateID string `json:"bi_update_id,omitempty"`
	AccountID  string `json:"account_id"`
}

// EventSource is core's outbox (GET /outbox/events and its acknowledgement); *CoreHTTP implements it.
type EventSource interface {
	PendingEvents(ctx context.Context, consumer string, limit int) ([]OutboxEvent, error)
	AckEvent(ctx context.Context, consumer string, id int64) error
}

// Relay posts Message 1 for every business-intelligence update and Message 2 for every StrategySet core publishes,
// with the real objects. Core writes an outbox event in the transaction that commits each; the relay lists its
// unacknowledged events, posts through the Publisher (the same path `slackbot --post-run` uses), and only then
// acknowledges. Delivery is at-least-once but the posts are exactly-once: the Publisher keeps core's create-only
// message refs (refs.go), so a crash between the post and the acknowledgement finds the ref on the redelivery and
// updates the message instead of posting again. Message 2 is never posted before its episode's Message 1: the
// relay makes sure Message 1 exists first, whatever order the events arrive in. An event that fails (core not
// ready, Slack down) stays unacknowledged and is retried with a growing wait, so one bad event never blocks
// the others.
type Relay struct {
	src  EventSource
	pub  *Publisher
	log  *slog.Logger
	poll time.Duration
	now  func() time.Time

	tries map[tryKey]retry // only touched by Drain, which Run calls from one goroutine
}

// tryKey names an event's retry state by its id AND its subject: a database restore starts the outbox ids over, so an id alone
// would let a failure from the world before the restore delay a different event of the restored one.
type tryKey struct {
	id      int64
	subject string
}

func tryKeyOf(ev OutboxEvent) tryKey {
	return tryKey{id: ev.ID, subject: ev.AgentRunID + "/" + ev.StrategySetID + "/" + ev.BIUpdateID}
}

type retry struct {
	attempts int
	notUntil time.Time
}

// NewRelay builds a relay over core's outbox and the publisher. poll 0 selects DefaultRelayPoll.
func NewRelay(src EventSource, pub *Publisher, log *slog.Logger, poll time.Duration) (*Relay, error) {
	switch {
	case src == nil:
		return nil, errors.New("slacksurface: a relay needs an event source")
	case pub == nil:
		return nil, errors.New("slacksurface: a relay needs a publisher")
	case poll < 0:
		return nil, errors.New("slacksurface: the relay poll interval must not be negative")
	}
	if poll == 0 {
		poll = DefaultRelayPoll
	}
	if log == nil {
		log = slog.Default()
	}
	return &Relay{src: src, pub: pub, log: log, poll: poll, now: time.Now, tries: map[tryKey]retry{}}, nil
}

// Run drains the outbox every poll until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("could not read core's outbox", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Drain handles every event that is due and returns how many it posted and acknowledged. The error is only a
// failure to read the outbox; a failing event is logged, kept unacknowledged and retried later.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	events, err := r.src.PendingEvents(ctx, RelayConsumer, relayBatch)
	if err != nil {
		return 0, err
	}
	posted := 0
	for _, ev := range events {
		if ctx.Err() != nil {
			break
		}
		if t, ok := r.tries[tryKeyOf(ev)]; ok && r.now().Before(t.notUntil) {
			continue
		}
		if err := r.handle(ctx, ev); err != nil {
			r.failed(ev, err)
			continue
		}
		delete(r.tries, tryKeyOf(ev))
		posted++
	}
	return posted, nil
}

func (r *Relay) handle(ctx context.Context, ev OutboxEvent) error {
	switch ev.Topic {
	case TopicBIUpdatePublished:
		if err := r.postBI(ctx, ev); err != nil {
			return err
		}
	case TopicStrategySetPublished:
		if ev.BIUpdateID != "" { // Message 1 first: Message 2 is never posted before its episode's update
			if err := r.postBI(ctx, ev); err != nil {
				return fmt.Errorf("message 1 of the episode is not posted yet: %w", err)
			}
		}
		if _, err := r.pub.PostChooser(ctx, ev.AgentRunID); err != nil {
			return err
		}
	} // an unknown topic is acknowledged: a newer core may publish what this surface does not render
	return r.src.AckEvent(ctx, RelayConsumer, ev.ID)
}

// postBI makes sure the event's business-intelligence update has its Message 1. An update a later one has
// superseded has no message and nothing waits for it.
func (r *Relay) postBI(ctx context.Context, ev OutboxEvent) error {
	if _, err := r.pub.PostBIUpdate(ctx, ev.AccountID, ev.BIUpdateID, ev.DecisionEpisodeID); err != nil && !errors.Is(err, ErrSuperseded) {
		return err
	}
	return nil
}

func (r *Relay) failed(ev OutboxEvent, err error) {
	t := r.tries[tryKeyOf(ev)]
	t.attempts++
	wait := min(r.poll<<min(t.attempts, 6), maxRelayBackoff)
	t.notUntil = r.now().Add(wait)
	r.tries[tryKeyOf(ev)] = t
	r.log.Error("could not post the message for an outbox event; it stays in the outbox and is retried",
		"event", ev.ID, "run", ev.AgentRunID, "attempt", t.attempts, "retry_in", wait.String(), "error", err)
}
