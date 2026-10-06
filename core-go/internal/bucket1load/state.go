package bucket1load

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
)

type diffChange struct {
	Field        string          `json:"field"`
	Material     bool            `json:"material"`
	Before       any             `json:"before"`
	After        any             `json:"after"`
	EvidenceRefs json.RawMessage `json:"evidence_refs"`
}

func show(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// loadStateDiff reads the episode's stored state diff, the open transition it recorded and the graph-diff of the
// trigger events. The graph-diff counts node and edge changes, not state fields, so it is compared by presence.
func loadStateDiff(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	if run.stateDiffID != "" {
		var raw []byte
		if err := db.QueryRowContext(ctx, `SELECT changes FROM state_diffs WHERE id = $1::uuid`, run.stateDiffID).Scan(&raw); err != nil {
			return fmt.Errorf("bucket1load: read state diff %s: %w", run.stateDiffID, err)
		}
		var changes []diffChange
		if err := json.Unmarshal(raw, &changes); err != nil {
			return fmt.Errorf("bucket1load: decode state diff %s: %w", run.stateDiffID, err)
		}
		for _, c := range changes {
			ep.StateDiff = append(ep.StateDiff, bucket1.FieldChange{Field: c.Field, Before: show(c.Before), After: show(c.After),
				Material: c.Material, Refs: jsonRefs(c.EvidenceRefs)})
		}
	}
	if err := loadTransition(ctx, db, run, ep); err != nil {
		return err
	}
	return loadGraphDiff(ctx, db, run, ep)
}

func loadTransition(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	var raw, supporting []byte
	var status sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT state_transition, transition_status, supporting_evidence FROM decision_episodes WHERE id = $1::uuid`,
		run.episodeID).Scan(&raw, &status, &supporting); err != nil {
		return fmt.Errorf("bucket1load: read transition of episode %s: %w", run.episodeID, err)
	}
	if len(raw) == 0 || !status.Valid {
		return nil
	}
	var t struct {
		ID      string  `json:"transition_id"`
		To      *string `json:"to_state_candidate"`
		Missing []any   `json:"missing_facts"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("bucket1load: decode transition of episode %s: %w", run.episodeID, err)
	}
	tr := &bucket1.Transition{ID: t.ID, Status: status.String, Support: jsonRefs(supporting)}
	if t.To != nil {
		tr.ToState = *t.To
	}
	ep.Transition = tr
	if status.String == "unresolved" || len(t.Missing) > 0 {
		ep.Unresolved = append(ep.Unresolved, "relationship_state")
	}
	return nil
}

// loadGraphDiff reads graph_projection_diffs for the trigger events. Not projected yet means the graph-diff is
// not known (B4 then reads that assertion as unknown), never an empty diff.
func loadGraphDiff(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	projected, changed := false, 0
	for _, id := range run.triggerIDs {
		var sourceEvent string
		if err := db.QueryRowContext(ctx, `SELECT source_event_id::text FROM activities WHERE id = $1::uuid`, id).Scan(&sourceEvent); err != nil {
			return fmt.Errorf("bucket1load: source event of activity %s: %w", id, err)
		}
		d, err := ctxgraph.DiffForEvent(ctx, db, sourceEvent)
		if err != nil {
			return err
		}
		projected = projected || d.Projected
		for _, c := range d.Changes {
			if c.AttributedToEvent {
				changed++
			}
		}
	}
	ep.GraphProjected, ep.GraphChanges = projected, changed
	return nil
}
