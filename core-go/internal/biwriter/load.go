package biwriter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
)

// Errors that mean "not yet": the recompute or the graph projection of the event has not finished, so the
// caller waits and loads again. Anything else is a failure.
var (
	// ErrDiffPending: no state diff names the activity yet (the recompute has not run).
	ErrDiffPending = errors.New("biwriter: the recompute of the event has not written its state diff yet")
	// ErrGraphPending: no graph projection of the event has recorded its diff yet.
	ErrGraphPending = errors.New("biwriter: the graph projection of the event has not recorded its diff yet")
)

// Source names the stored objects of one released event.
type Source struct {
	ActivityID     string // the activity the event became
	SourceEventID  string // its source_events id (what GET /events/{id}/graph-diff takes)
	OpportunityID  string // the manifest's deal
	HeldOutEventID string // the replay dataset's id of the event (the manifest's)
}

// LoadFacts reads everything Build needs from the stores: the activity, the first state diff that names it,
// the signals and trigger evaluation of that diff, the transition the activity touched and the event's graph
// diff. It returns ErrDiffPending or ErrGraphPending while the pipeline is still working on the event.
func LoadFacts(ctx context.Context, db *sql.DB, src Source) (Facts, error) {
	f := Facts{OpportunityID: src.OpportunityID, HeldOutEventID: src.HeldOutEventID}
	if err := loadActivity(ctx, db, src.ActivityID, &f); err != nil {
		return Facts{}, err
	}
	if err := loadDiff(ctx, db, &f); err != nil {
		return Facts{}, err
	}
	if err := loadSignalsAndTrigger(ctx, db, &f); err != nil {
		return Facts{}, err
	}
	if err := loadTransition(ctx, db, &f); err != nil {
		return Facts{}, err
	}
	if err := loadGraph(ctx, db, src.SourceEventID, &f); err != nil {
		return Facts{}, err
	}
	return f, loadPeople(ctx, db, &f)
}

func loadActivity(ctx context.Context, db *sql.DB, activityID string, f *Facts) error {
	var accountID sql.NullString
	var summary sql.NullString
	err := db.QueryRowContext(ctx, `
SELECT a.activity_type, a.summary, a.occurred_at, a.account_id::text, ac.name
  FROM activities a LEFT JOIN accounts ac ON ac.id = a.account_id WHERE a.id = $1::uuid`, activityID).
		Scan(&f.Activity.Type, &summary, &f.Activity.OccurredAt, &accountID, &f.AccountName)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("biwriter: activity %s does not exist", activityID)
	}
	if err != nil {
		return fmt.Errorf("biwriter: read activity %s: %w", activityID, err)
	}
	if !accountID.Valid {
		return fmt.Errorf("biwriter: activity %s is not attributed to an account", activityID)
	}
	f.Activity.ID, f.Activity.Summary, f.AccountID = activityID, summary.String, accountID.String
	f.Activity.OccurredAt = f.Activity.OccurredAt.UTC()
	return nil
}

func loadDiff(ctx context.Context, db *sql.DB, f *Facts) error {
	var changes, activities string
	d := &f.Diff
	err := db.QueryRowContext(ctx, `
SELECT id::text, from_version, to_version, is_material, changes::text, to_jsonb(activity_ids)::text
  FROM state_diffs WHERE account_id = $1::uuid AND activity_ids @> ARRAY[$2]::uuid[]
 ORDER BY to_version LIMIT 1`, f.AccountID, f.Activity.ID).
		Scan(&d.ID, &d.FromVersion, &d.ToVersion, &d.IsMaterial, &changes, &activities)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDiffPending
	}
	if err != nil {
		return fmt.Errorf("biwriter: read the state diff of activity %s: %w", f.Activity.ID, err)
	}
	d.AccountID = f.AccountID
	if err := json.Unmarshal([]byte(changes), &d.Entries); err != nil {
		return fmt.Errorf("biwriter: decode state diff %s: %w", d.ID, err)
	}
	if err := json.Unmarshal([]byte(activities), &d.ActivityIDs); err != nil {
		return fmt.Errorf("biwriter: decode state diff %s activities: %w", d.ID, err)
	}
	return nil
}

func loadSignalsAndTrigger(ctx context.Context, db *sql.DB, f *Facts) error {
	rows, err := db.QueryContext(ctx, `SELECT id::text, signal_type FROM signals WHERE state_diff_id = $1::uuid ORDER BY signal_type, id`, f.Diff.ID)
	if err != nil {
		return fmt.Errorf("biwriter: read signals of diff %s: %w", f.Diff.ID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var s Signal
		if err := rows.Scan(&s.ID, &s.Type); err != nil {
			return fmt.Errorf("biwriter: scan signal: %w", err)
		}
		f.Signals = append(f.Signals, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("biwriter: read signals: %w", err)
	}
	var reasons string
	t := &Trigger{}
	err = db.QueryRowContext(ctx, `SELECT eligible, to_jsonb(reason_codes)::text FROM trigger_evaluations
 WHERE state_diff_id = $1::uuid ORDER BY evaluated_at, id LIMIT 1`, f.Diff.ID).Scan(&t.Eligible, &reasons)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("biwriter: read the trigger evaluation of diff %s: %w", f.Diff.ID, err)
	}
	if err := json.Unmarshal([]byte(reasons), &t.ReasonCodes); err != nil {
		return fmt.Errorf("biwriter: decode trigger reasons: %w", err)
	}
	f.Trigger = t
	return nil
}

func loadTransition(ctx context.Context, db *sql.DB, f *Facts) error {
	rec, err := transitionstore.ForActivity(ctx, db, f.AccountID, f.Activity.ID)
	if err != nil {
		return err
	}
	touched := rec != nil
	if !touched { // the event moved no transition: the account's open one, if any, is shown as unchanged
		if rec, err = transitionstore.Open(ctx, db, f.AccountID); err != nil || rec == nil {
			return err
		}
	}
	f.Transition = &Transition{ID: rec.ID, Status: rec.Status, FromState: rec.FromState, ToState: rec.ToStateCandidate,
		Supporting: factsOf(rec.SupportingFacts), Missing: factsOf(rec.MissingFacts), Touched: touched}
	return nil
}

func factsOf(in []transitions.FactResult) []Fact {
	out := make([]Fact, 0, len(in))
	for _, r := range in {
		fact := Fact{Key: r.Key, Description: r.Description, Required: r.Required}
		for _, ref := range r.EvidenceRefs {
			fact.Evidence = append(fact.Evidence, EvidenceRef{ActivityID: ref.ActivityID, ClaimID: ref.ClaimID, Quote: ref.Quote,
				SpeakerPersonID: ref.SpeakerPersonID, OccurredAt: ref.OccurredAt})
		}
		out = append(out, fact)
	}
	return out
}

func loadGraph(ctx context.Context, db *sql.DB, sourceEventID string, f *Facts) error {
	diff, err := ctxgraph.DiffForEvent(ctx, db, sourceEventID)
	if err != nil {
		return fmt.Errorf("biwriter: read the graph diff of event %s: %w", sourceEventID, err)
	}
	if !diff.Projected {
		return ErrGraphPending
	}
	f.Graph = GraphDiff{SourceEventID: sourceEventID, JobIDs: diff.JobIDs}
	if len(diff.JobIDs) > 0 {
		err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM graph_projection_jobs WHERE account_id = $1::uuid AND id < $2`,
			f.AccountID, slices.Min(diff.JobIDs)).Scan(&f.Graph.PrevJobID)
		if err != nil {
			return fmt.Errorf("biwriter: read the account's previous projection job: %w", err)
		}
	}
	for _, c := range diff.Changes {
		f.Graph.Items = append(f.Graph.Items, GraphItem{Kind: c.Kind, Type: c.Type, ID: c.ID, Op: c.Op,
			Attributed: c.AttributedToEvent, ActivityIDs: evidenceActivities(c.Change)})
	}
	return nil
}

// evidenceActivities are the activities a graph element is evidence-linked to: its properties when it was
// added or removed, the new value of that property when it changed.
func evidenceActivities(c ctxgraph.Change) []string {
	if ids := stringsOf(c.Props["evidence_activity_ids"]); len(ids) > 0 {
		return ids
	}
	if pc, ok := c.Changed["evidence_activity_ids"]; ok {
		return stringsOf(pc.After)
	}
	return nil
}

func stringsOf(v any) []string {
	switch x := v.(type) {
	case []string:
		return append([]string(nil), x...)
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// loadPeople names the people the diff mentions, so a statement can say "Marco Ruiz" and not an id.
func loadPeople(ctx context.Context, db *sql.DB, f *Facts) error {
	want := map[string]bool{}
	for _, e := range f.Diff.Entries {
		if e.Field != fieldBuyingGroup {
			continue
		}
		for _, v := range []any{e.Before, e.After} {
			for id := range decodeMembers(v) {
				want[id] = true
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	idsJSON, _ := json.Marshal(ids)
	rows, err := db.QueryContext(ctx, `SELECT id::text, display_name FROM people
 WHERE id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid)`, string(idsJSON))
	if err != nil {
		return fmt.Errorf("biwriter: read people: %w", err)
	}
	defer rows.Close()
	f.People = map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return fmt.Errorf("biwriter: scan person: %w", err)
		}
		f.People[id] = name
	}
	return rows.Err()
}
