package ctxgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// recordDiff stores the projection's diff in Postgres before the graph is written, once per job: a
// retry after a crash finds the row and keeps it, so the recorded diff is the one computed against the
// graph as it was before the first attempt.
func recordDiff(ctx context.Context, db *sql.DB, job Job, accountID string, d Diff) error {
	changes, err := MarshalChanges(d)
	if err != nil {
		return err
	}
	summary, err := json.Marshal(d.Summary())
	if err != nil {
		return fmt.Errorf("ctxgraph: encode diff summary: %w", err)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO graph_projection_diffs (job_id, account_id, activity_ids, source_event_ids, summary, changes)
VALUES ($1, $2::uuid, $3::uuid[],
        (SELECT COALESCE(array_agg(DISTINCT e), '{}') FROM (
            SELECT source_event_id AS e FROM activities WHERE id = ANY($3::uuid[])
            UNION SELECT x::uuid FROM unnest($6::text[]) AS u(x)) ev),
        $4::jsonb, $5::jsonb)
ON CONFLICT (job_id) DO NOTHING`,
		job.ID, accountID, signalstore.UUIDArray(job.ActivityIDs), string(summary), string(changes), signalstore.UUIDArray(changeEvents(d)))
	if err != nil {
		return fmt.Errorf("ctxgraph: record diff of job %d: %w", job.ID, err)
	}
	return nil
}

// changeEvents is the union of the events the diff's elements are evidence-linked to. It is stored with the
// diff (source_event_ids, GIN indexed), so a per-event query finds a diff through any event it touched even
// when a burst's activity list or a retry no longer names that event.
func changeEvents(d Diff) []string {
	var all []string
	for _, c := range d.Changes {
		all = append(all, c.Events...)
	}
	return sortedUnique(all...)
}

// EventChange is a graph change plus whether the event itself is evidence for the element. The other
// changes happened in the same projection (a coalesced burst) but cite other events.
type EventChange struct {
	Change
	AttributedToEvent bool `json:"attributed_to_event"`
}

// EventDiff is the exact graph diff of one source event (HAR-129 1B).
type EventDiff struct {
	EventID   string         `json:"event_id"`
	Projected bool           `json:"projected"` // false: no projection of this event has completed its diff yet
	JobIDs    []int64        `json:"job_ids"`
	Summary   map[string]int `json:"summary"`
	Changes   []EventChange  `json:"changes"`
}

// DiffForEvent returns the nodes and edges the projections of the event added, changed or removed,
// merged into the net change (ingest and recompute are separate projections of one event).
func DiffForEvent(ctx context.Context, db *sql.DB, eventID string) (EventDiff, error) {
	rows, err := db.QueryContext(ctx, `
SELECT job_id, changes::text FROM graph_projection_diffs WHERE $1::uuid = ANY(source_event_ids) ORDER BY job_id`, eventID)
	if err != nil {
		return EventDiff{}, fmt.Errorf("ctxgraph: read diffs of event %s: %w", eventID, err)
	}
	defer rows.Close()
	out := EventDiff{EventID: eventID, JobIDs: []int64{}, Changes: []EventChange{}}
	var diffs []Diff
	for rows.Next() {
		var job int64
		var raw string
		if err := rows.Scan(&job, &raw); err != nil {
			return EventDiff{}, err
		}
		var changes []Change
		if err := json.Unmarshal([]byte(raw), &changes); err != nil {
			return EventDiff{}, fmt.Errorf("ctxgraph: decode diff of job %d: %w", job, err)
		}
		out.JobIDs = append(out.JobIDs, job)
		diffs = append(diffs, Diff{Changes: changes})
	}
	if err := rows.Err(); err != nil {
		return EventDiff{}, err
	}
	out.Projected = len(diffs) > 0
	merged := MergeDiffs(diffs...)
	out.Summary = merged.Summary()
	for _, c := range merged.Changes {
		out.Changes = append(out.Changes, EventChange{Change: c, AttributedToEvent: contains(c.Events, eventID)})
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
