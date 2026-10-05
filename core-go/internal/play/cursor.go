package play

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
)

// Kinds of leak that prove the world has not stopped at N-1 even though nothing carries event N's own ids.
const (
	kindLaterActivity = "activity_at_or_after_n" // an activity of the account or the deal dated at or after event N
	kindStateCursor   = "account_state_cursor"   // AccountState was last moved by something other than event N-1
)

// laterActivities are the activities of the manifest's account or deal that occurred at or after event N. Event N
// reaching the system under another source key, a later event, or the same real event loaded twice all end
// here, which a check by id cannot see.
func (c *Checker) laterActivities(ctx context.Context, m Manifest) ([]Leak, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id::text FROM activities
 WHERE (account_id = $1::uuid OR opportunity_id = $2::uuid) AND occurred_at >= $3 ORDER BY id`, m.AccountID, m.OpportunityID, m.Held.OccurredAt)
	if err != nil {
		return nil, fmt.Errorf("play: look for activities at or after event N: %w", err)
	}
	defer rows.Close()
	var out []Leak
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, Leak{Store: "postgres", Kind: kindLaterActivity, ID: id})
	}
	return out, rows.Err()
}

// stateCursor checks that AccountState was last moved by event N-1: its last_activity_id is the activity of the
// manifest's last history event. A missing state, or one moved by anything else, is reported (the id is the account).
func (c *Checker) stateCursor(ctx context.Context, m Manifest) ([]Leak, error) {
	var last sql.NullString
	err := c.db.QueryRowContext(ctx, `SELECT last_activity_id::text FROM account_state WHERE account_id = $1::uuid`, m.AccountID).Scan(&last)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return []Leak{{Store: "postgres", Kind: kindStateCursor, ID: m.AccountID}}, nil
	case err != nil:
		return nil, fmt.Errorf("play: read the account state cursor: %w", err)
	}
	if !last.Valid {
		return []Leak{{Store: "postgres", Kind: kindStateCursor, ID: m.AccountID}}, nil
	}
	var isLast bool
	err = c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM activities a JOIN source_events se ON se.id = a.source_event_id
 WHERE a.id = $1::uuid AND (se.id::text = $2 OR (se.source_system = $3 AND se.source_object_id = $4 AND se.source_event_key = $5)))`,
		last.String, m.LastEventID, m.LastSource.System, m.LastSource.ObjectID, m.LastSource.EventKey).Scan(&isLast)
	if err != nil {
		return nil, fmt.Errorf("play: check the account state cursor is event N-1: %w", err)
	}
	if !isLast {
		return []Leak{{Store: "postgres", Kind: kindStateCursor, ID: m.AccountID}}, nil
	}
	return nil, nil
}

// graphLater is the graph's side of laterActivities.
func (c *Checker) graphLater(ctx context.Context, m Manifest) ([]Leak, error) {
	hits, err := c.probe.ActivitiesFrom(ctx, m.AccountID, m.OpportunityID, m.Held.OccurredAt)
	if err != nil {
		return nil, graphError(err, m.Held.EventID)
	}
	return graphLeaks(hits), nil
}

func graphLeaks(hits []ctxgraph.Hit) []Leak {
	out := make([]Leak, 0, len(hits))
	for _, h := range hits {
		out = append(out, Leak{Store: "neo4j", Kind: "graph_" + h.Kind + ":" + h.Type, ID: h.ID})
	}
	return out
}

func graphError(err error, eventID string) error {
	if isGraphDown(err) {
		return fmt.Errorf("%w: %v", ErrGraphUnavailable, err)
	}
	return fmt.Errorf("play: check the graph for event %s: %w", eventID, err)
}
