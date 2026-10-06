package runs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

func jsonStrings(raw []byte) ([]string, error) {
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("runs: decode id list: %w", err)
	}
	return out, nil
}

func loadDiff(ctx context.Context, db claimstore.DB, id string) (*TraceDiff, error) {
	d := &TraceDiff{ID: id}
	var changes, activities []byte
	err := db.QueryRowContext(ctx, `SELECT from_version, to_version, is_material, changes, to_jsonb(activity_ids) FROM state_diffs WHERE id = $1::uuid`, id).
		Scan(&d.FromVersion, &d.ToVersion, &d.IsMaterial, &changes, &activities)
	if err != nil {
		return nil, fmt.Errorf("runs: read diff %s: %w", id, err)
	}
	d.Changes = changes
	if d.ActivityIDs, err = jsonStrings(activities); err != nil {
		return nil, err
	}
	return d, nil
}

func loadSignals(ctx context.Context, db claimstore.DB, ids []string) ([]TraceSignal, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id::text, signal_type, rule, occurred_at, evidence_refs FROM signals WHERE id = ANY($1::uuid[]) ORDER BY occurred_at, id`, signalstore.UUIDArray(ids))
	if err != nil {
		return nil, fmt.Errorf("runs: read signals: %w", err)
	}
	defer rows.Close()
	var out []TraceSignal
	for rows.Next() {
		var s TraceSignal
		var refs []byte
		if err := rows.Scan(&s.ID, &s.Type, &s.Rule, &s.OccurredAt, &refs); err != nil {
			return nil, fmt.Errorf("runs: scan signal: %w", err)
		}
		if err := json.Unmarshal(refs, &s.EvidenceRefs); err != nil {
			return nil, fmt.Errorf("runs: decode evidence of signal %s: %w", s.ID, err)
		}
		s.OccurredAt = s.OccurredAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadActivities(ctx context.Context, db claimstore.DB, ids []string) ([]TraceActivity, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, activity_type, occurred_at FROM activities WHERE id = ANY($1::uuid[]) ORDER BY occurred_at, id`, signalstore.UUIDArray(ids))
	if err != nil {
		return nil, fmt.Errorf("runs: read trigger activities: %w", err)
	}
	defer rows.Close()
	var out []TraceActivity
	for rows.Next() {
		var a TraceActivity
		if err := rows.Scan(&a.ID, &a.Type, &a.OccurredAt); err != nil {
			return nil, fmt.Errorf("runs: scan activity: %w", err)
		}
		a.OccurredAt = a.OccurredAt.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func loadSteps(ctx context.Context, db claimstore.DB, runID string) ([]TraceStep, error) {
	rows, err := db.QueryContext(ctx, `SELECT seq, step, status, COALESCE(external_effect_id, '') FROM agent_run_steps WHERE agent_run_id = $1::uuid ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("runs: read steps: %w", err)
	}
	defer rows.Close()
	var out []TraceStep
	for rows.Next() {
		var s TraceStep
		if err := rows.Scan(&s.Seq, &s.Step, &s.Status, &s.ExternalEffectID); err != nil {
			return nil, fmt.Errorf("runs: scan step: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadContextRefs(ctx context.Context, db claimstore.DB, runID string) ([]ContextRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, tool, bytes FROM context_access_log WHERE agent_run_id = $1::uuid ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("runs: read context log: %w", err)
	}
	defer rows.Close()
	var out []ContextRef
	for rows.Next() {
		var c ContextRef
		if err := rows.Scan(&c.AccessID, &c.Tool, &c.Bytes); err != nil {
			return nil, fmt.Errorf("runs: scan context ref: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// collectEvidence gathers the evidence refs of the diff's changes and of the signals, once each
// (by activity and claim).
func collectEvidence(t Trace) ([]reducer.EvidenceRef, error) {
	var out []reducer.EvidenceRef
	seen := map[string]bool{}
	add := func(refs []reducer.EvidenceRef) {
		for _, r := range refs {
			if k := r.ActivityID + "/" + r.ClaimID; r.ActivityID != "" && !seen[k] {
				seen[k] = true
				out = append(out, r)
			}
		}
	}
	if t.Diff != nil {
		var changes []struct {
			Refs []reducer.EvidenceRef `json:"evidence_refs"`
		}
		if err := json.Unmarshal(t.Diff.Changes, &changes); err != nil {
			return nil, fmt.Errorf("runs: decode diff changes: %w", err)
		}
		for _, c := range changes {
			add(c.Refs)
		}
	}
	for _, s := range t.Signals {
		add(s.EvidenceRefs)
	}
	return out, nil
}
