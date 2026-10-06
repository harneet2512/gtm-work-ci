package slacksurface

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Core API paths of the outbox (contracts/openapi/core.yaml).
const (
	pathOutbox    = "/outbox/events"        // contract (GET)
	pathOutboxAck = "/outbox/events/%d/ack" // contract (POST)
)

// PendingEvents lists the consumer's unacknowledged events of every topic, oldest id first.
func (c *CoreHTTP) PendingEvents(ctx context.Context, consumer string, limit int) ([]OutboxEvent, error) {
	q := url.Values{"consumer": {consumer}, "limit": {strconv.Itoa(limit)}}
	var page struct {
		Items []OutboxEvent `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, pathOutbox+"?"+q.Encode(), nil, &page); err != nil {
		return nil, err
	}
	return page.Items, nil
}

// AckEvent acknowledges one event for the consumer (idempotent).
func (c *CoreHTTP) AckEvent(ctx context.Context, consumer string, id int64) error {
	path := fmt.Sprintf(pathOutboxAck, id) + "?" + url.Values{"consumer": {consumer}}.Encode()
	return c.do(ctx, http.MethodPost, path, nil, nil)
}

var _ EventSource = (*CoreHTTP)(nil)
