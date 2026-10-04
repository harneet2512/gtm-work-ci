// Package outbox is the feed surfaces react to instead of polling the read model (HAR-117, WP19, HAR-136). A
// database trigger writes one strategy_set.published event in the transaction that commits a StrategySet and one
// bi_update.published event in the transaction that commits a business-intelligence update (migration 0025), so
// an event exists if and only if its fact does. A consumer (Slack, later the web) lists its unacknowledged events,
// acts on them, then acknowledges. Delivery is at-least-once per consumer: a crash between acting and
// acknowledging delivers the event again, so consumers must be idempotent (the Slack publisher is, through the
// create-only message refs in package surfacemsg).
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Topics.
const (
	// TopicStrategySetPublished: the run's StrategySet and all its EvalBundles are committed (Slack Message 2).
	TopicStrategySetPublished = "strategy_set.published"
	// TopicBIUpdatePublished: a business-intelligence update is committed (Slack Message 1).
	TopicBIUpdatePublished = "bi_update.published"
)

// Limits of Pending.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

var (
	// ErrInvalid is a malformed consumer name, topic or limit.
	ErrInvalid = errors.New("outbox: invalid request")
	// ErrNotFound is an acknowledgement of an event that does not exist.
	ErrNotFound = errors.New("outbox: no such event")
)

var consumerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Event is contracts/schemas/outbox_event.v1.json.
// A strategy_set.published event has the run, set and episode ids (and the update of its episode, when it has
// one); a bi_update.published event has only the update id.
type Event struct {
	ID                int64     `json:"id"`
	Topic             string    `json:"topic"`
	AgentRunID        string    `json:"agent_run_id,omitempty"`
	StrategySetID     string    `json:"strategy_set_id,omitempty"`
	DecisionEpisodeID string    `json:"decision_episode_id,omitempty"`
	BIUpdateID        string    `json:"bi_update_id,omitempty"`
	AccountID         string    `json:"account_id"`
	CreatedAt         time.Time `json:"created_at"`
}

// Store reads and acknowledges events.
type Store struct{ db *sql.DB }

// New returns a Store over db.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("outbox: a database is required")
	}
	return &Store{db: db}, nil
}

// ValidConsumer reports whether name is an acceptable consumer name.
func ValidConsumer(name string) bool { return consumerPattern.MatchString(name) }

// Pending returns the consumer's unacknowledged events of topic, oldest id first (the id is allocated at insert, not
// at commit, so it orders listing and is never a cursor). An empty topic is every topic; a limit of 0 is DefaultLimit.
func (s *Store) Pending(ctx context.Context, consumer, topic string, limit int) ([]Event, error) {
	if !ValidConsumer(consumer) {
		return nil, fmt.Errorf("%w: consumer must match %s", ErrInvalid, consumerPattern)
	}
	if topic != "" && topic != TopicStrategySetPublished && topic != TopicBIUpdatePublished {
		return nil, fmt.Errorf("%w: unknown topic %q", ErrInvalid, topic)
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return nil, fmt.Errorf("%w: limit must be 1..%d", ErrInvalid, MaxLimit)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.topic, COALESCE(e.agent_run_id::text, ''), COALESCE(e.strategy_set_id::text, ''),
 COALESCE(e.decision_episode_id::text, ''), COALESCE(e.bi_update_id::text, d.business_intelligence_update_id::text, ''),
 e.account_id::text, e.created_at
 FROM outbox_events e LEFT JOIN decision_episodes d ON d.id = e.decision_episode_id
 WHERE ($2 = '' OR e.topic = $2) AND NOT EXISTS (SELECT 1 FROM outbox_acks a WHERE a.event_id = e.id AND a.consumer = $1)
 ORDER BY e.id LIMIT $3`, consumer, topic, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: list events: %w", err)
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Topic, &e.AgentRunID, &e.StrategySetID, &e.DecisionEpisodeID, &e.BIUpdateID, &e.AccountID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("outbox: scan event: %w", err)
		}
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ack records that the consumer handled the event. It is idempotent; ErrNotFound: no such event.
func (s *Store) Ack(ctx context.Context, consumer string, eventID int64) error {
	if !ValidConsumer(consumer) {
		return fmt.Errorf("%w: consumer must match %s", ErrInvalid, consumerPattern)
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_events WHERE id = $1)`, eventID).Scan(&exists); err != nil {
		return fmt.Errorf("outbox: check event %d: %w", eventID, err)
	}
	if !exists {
		return fmt.Errorf("%w: %d", ErrNotFound, eventID)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO outbox_acks (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, consumer, eventID); err != nil {
		return fmt.Errorf("outbox: acknowledge event %d: %w", eventID, err)
	}
	return nil
}
