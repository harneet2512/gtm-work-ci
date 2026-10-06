package play

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// Statuses of an invisibility report.
const (
	StatusWithheld = "withheld" // nothing derived from event N exists: Play may run
	StatusLeaked   = "leaked"   // something does: Play refuses
	StatusReleased = "released" // Play has been called: the event is legitimately present
)

// Leak is one thing that exists although the event it derives from has not been released.
type Leak struct {
	Store string `json:"store"` // postgres | neo4j
	Kind  string `json:"kind"`
	ID    string `json:"id"`
}

// Report is the outcome of the event-N-invisible assertion (core.yaml InvisibilityReport).
type Report struct {
	ManifestID     string   `json:"manifest_id"`
	HeldOutEventID string   `json:"held_out_event_id"`
	Status         string   `json:"status"`
	Checked        []string `json:"checked"`
	Leaks          []Leak   `json:"leaks"`
}

// GraphProbe finds the nodes and relationships of Neo4j derived from ids; *ctxgraph.Reader implements it.
type GraphProbe interface {
	Referencing(ctx context.Context, ids []string) ([]ctxgraph.Hit, error)
	// ActivitiesFrom finds the Activity nodes of the account or its deal dated at or after cutoff.
	ActivitiesFrom(ctx context.Context, accountID, opportunityID string, cutoff time.Time) ([]ctxgraph.Hit, error)
}

// ErrGraphUnavailable: the graph cannot be checked or projected (not configured, or unreachable). An unchecked
// store is never reported as clean, and a release that cannot be projected is never started.
var ErrGraphUnavailable = errors.New("play: the graph database is not available")

// Checker runs the event-N-invisible assertion.
type Checker struct {
	db    *sql.DB
	probe GraphProbe
}

// NewChecker returns a Checker. A nil probe makes Check fail with ErrGraphUnavailable: Neo4j is one of the
// stores the assertion covers.
func NewChecker(db *sql.DB, probe GraphProbe) (*Checker, error) {
	if db == nil {
		return nil, errors.New("play: a database is required")
	}
	return &Checker{db: db, probe: probe}, nil
}

// pgCheck is one Postgres store the event must not have reached. query takes $1 uuid[] of every id the event
// could appear under (its dataset id, its source event ids, its activity ids), $2 text[] of the same ids as text
// (for documents that embed ids) and $3 the manifest's account, and returns the ids of what it found. A query
// names only the placeholders it uses.
type pgCheck struct{ kind, query string }

// embedsEvent is true when the document column mentions one of the ids ($2) and belongs to the manifest's
// account ($3): the scan of documents that embed ids (state, evidence) stays within the account.
const embedsEvent = `(account_id = $3::uuid AND EXISTS (SELECT 1 FROM unnest($2::text[]) AS x WHERE position(x in %s::text) > 0))`

var pgChecks = []pgCheck{
	{"claim", `SELECT id::text FROM claims WHERE source_activity_id = ANY($1::uuid[])`},
	{"account_state", `SELECT account_id::text FROM account_state WHERE last_activity_id = ANY($1::uuid[])
		OR ` + fmt.Sprintf(embedsEvent, "state")},
	{"state_history", `SELECT account_id::text || '@' || version::text FROM state_history WHERE trigger_activity_ids && $1::uuid[]
		OR ` + fmt.Sprintf(embedsEvent, "state")},
	{"opportunity_state", `SELECT opportunity_id::text FROM opportunity_state WHERE last_activity_id = ANY($1::uuid[])
		OR ` + fmt.Sprintf(embedsEvent, "state")},
	{"opportunity_state_history", `SELECT opportunity_id::text || '@' || version::text FROM opportunity_state_history
		WHERE trigger_activity_ids && $1::uuid[] OR ` + fmt.Sprintf(embedsEvent, "state")},
	{"state_diff", `SELECT id::text FROM state_diffs WHERE activity_ids && $1::uuid[]`},
	{"signal", `SELECT id::text FROM signals WHERE state_diff_id IN (SELECT id FROM state_diffs WHERE activity_ids && $1::uuid[])
		OR ` + fmt.Sprintf(embedsEvent, "evidence_refs")},
	{"trigger_evaluation", `SELECT id::text FROM trigger_evaluations WHERE state_diff_id IN (SELECT id FROM state_diffs WHERE activity_ids && $1::uuid[])`},
	{"agent_run", `SELECT id::text FROM agent_runs WHERE trigger_activity_ids && $1::uuid[]`},
	{"state_transition", `SELECT id::text FROM state_transitions WHERE trigger_activity_ids && $1::uuid[]
		OR ` + fmt.Sprintf(embedsEvent, "supporting_facts")},
	{"relationship", `SELECT id::text FROM relationships WHERE source_activity_id = ANY($1::uuid[])`},
	{"recompute_job", `SELECT id::text FROM recompute_jobs WHERE activity_ids && $1::uuid[]`},
	{"graph_projection_job", `SELECT id::text FROM graph_projection_jobs WHERE activity_ids && $1::uuid[]`},
	{"graph_projection_diff", `SELECT id::text FROM graph_projection_diffs WHERE source_event_ids && $1::uuid[] OR activity_ids && $1::uuid[]`},
	{"account_change", `SELECT id::text FROM account_changes WHERE trigger_activity_ids && $1::uuid[] OR held_out_event_id = ANY($1::uuid[])`},
	{"business_intelligence_update", `SELECT b.id::text FROM business_intelligence_updates b JOIN account_changes c ON c.id = b.account_change_id
		WHERE c.trigger_activity_ids && $1::uuid[] OR c.held_out_event_id = ANY($1::uuid[])`},
}

// checkedStores names the stores Check inspects, in order.
func checkedStores() []string {
	out := []string{"source_event", "activity"}
	for _, c := range pgChecks {
		out = append(out, c.kind)
	}
	return append(out, kindLaterActivity, kindStateCursor, "neo4j")
}

// Check runs the assertion for the manifest. A manifest whose Play has started is reported released and not
// inspected: its event is legitimately present.
func (c *Checker) Check(ctx context.Context, m Manifest) (Report, error) {
	rep := Report{ManifestID: m.ID, HeldOutEventID: m.Held.EventID, Checked: []string{}, Leaks: []Leak{}}
	var played bool
	if err := c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM demo_plays WHERE manifest_id = $1::uuid)`, m.ID).Scan(&played); err != nil {
		return Report{}, fmt.Errorf("play: read the play record of manifest %s: %w", m.ID, err)
	}
	if played {
		rep.Status = StatusReleased
		return rep, nil
	}
	if c.probe == nil {
		return Report{}, fmt.Errorf("%w: it is not configured, so the graph cannot be checked", ErrGraphUnavailable)
	}
	rep.Checked = checkedStores()
	ids, leaks, err := c.eventObjects(ctx, m)
	if err != nil {
		return Report{}, err
	}
	rep.Leaks = append(rep.Leaks, leaks...)
	for _, chk := range pgChecks {
		found, err := c.find(ctx, chk, ids, m.AccountID)
		if err != nil {
			return Report{}, err
		}
		for _, id := range found {
			rep.Leaks = append(rep.Leaks, Leak{Store: "postgres", Kind: chk.kind, ID: id})
		}
	}
	more, err := c.worldLeaks(ctx, m, ids)
	if err != nil {
		return Report{}, err
	}
	rep.Leaks = append(rep.Leaks, more...)
	rep.Status = StatusWithheld
	if len(rep.Leaks) > 0 {
		rep.Status = StatusLeaked
	}
	return rep, nil
}

// eventObjects finds the source events and activities that already stand for the held-out event, by its
// idempotency key and by its dataset id, and returns every id the event could appear under.
func (c *Checker) eventObjects(ctx context.Context, m Manifest) (ids []string, leaks []Leak, err error) {
	ids = []string{m.Held.EventID}
	rows, err := c.db.QueryContext(ctx, `SELECT id::text FROM source_events
 WHERE id = $1::uuid OR (source_system = $2 AND source_object_id = $3 AND source_event_key = $4) ORDER BY id`,
		m.Held.EventID, m.Held.Source.System, m.Held.Source.ObjectID, m.Held.Source.EventKey)
	if err != nil {
		return nil, nil, fmt.Errorf("play: look for the source event of %s: %w", m.Held.EventID, err)
	}
	var events []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		events = append(events, id)
		leaks = append(leaks, Leak{Store: "postgres", Kind: "source_event", ID: id})
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	activities, err := c.activities(ctx, m.Held.EventID, events)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range activities {
		leaks = append(leaks, Leak{Store: "postgres", Kind: "activity", ID: id})
	}
	return unique(append(append(ids, events...), activities...)), leaks, nil
}

func (c *Checker) activities(ctx context.Context, heldID string, events []string) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id::text FROM activities WHERE id = $1::uuid OR source_event_id = ANY($2::uuid[]) ORDER BY id`,
		heldID, signalstore.UUIDArray(events))
	if err != nil {
		return nil, fmt.Errorf("play: look for the activity of %s: %w", heldID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (c *Checker) find(ctx context.Context, chk pgCheck, ids []string, accountID string) ([]string, error) {
	arr := signalstore.UUIDArray(ids)
	args := []any{arr}
	if strings.Contains(chk.query, "$2") {
		args = append(args, arr)
	}
	if strings.Contains(chk.query, "$3") {
		args = append(args, accountID)
	}
	rows, err := c.db.QueryContext(ctx, chk.query, args...)
	if err != nil {
		return nil, fmt.Errorf("play: check %s: %w", chk.kind, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// worldLeaks are the leaks found by what the world holds beyond event N's own ids: Postgres activities dated at
// or after N, AccountState not at N-1, and Neo4j's nodes that cite N's ids or are dated at or after N.
func (c *Checker) worldLeaks(ctx context.Context, m Manifest, ids []string) ([]Leak, error) {
	var out []Leak
	for _, check := range []func(context.Context, Manifest) ([]Leak, error){c.laterActivities, c.stateCursor} {
		leaks, err := check(ctx, m)
		if err != nil {
			return nil, err
		}
		out = append(out, leaks...)
	}
	hits, err := c.probe.Referencing(ctx, ids)
	if err != nil {
		return nil, graphError(err, m.Held.EventID)
	}
	out = append(out, graphLeaks(hits)...)
	later, err := c.graphLater(ctx, m)
	if err != nil {
		return nil, err
	}
	return append(out, later...), nil
}

func isGraphDown(err error) bool { return errors.Is(err, ctxgraph.ErrGraphUnavailable) }
