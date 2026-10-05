package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// ErrAccountNotFound means the account has no row in Postgres (a projection of it cannot exist).
var ErrAccountNotFound = errors.New("ctxgraph: account not found")

// BuildSnapshot derives the canonical projection of one account from Postgres. It reads through
// db only, so the caller chooses the isolation (the projector uses one REPEATABLE READ transaction
// so the snapshot is internally consistent). Same Postgres state, same snapshot, byte for byte.
func BuildSnapshot(ctx context.Context, db claimstore.DB, accountID string) (Snapshot, error) {
	return buildSnapshot(ctx, db, accountID, nil, nil)
}

// buildSnapshot is BuildSnapshot with an optional world-time cutoff (ADR-0019): with cut set, every loader
// keeps only what the world held strictly before it (see snapshot_asof.go).
func buildSnapshot(ctx context.Context, db claimstore.DB, accountID string, cut, episodesBefore *time.Time) (Snapshot, error) {
	b := &builder{db: db, accountID: accountID, cut: cut, episodesBefore: episodesBefore, nodes: map[string]Node{}, edges: map[string]Edge{},
		skipped: map[string]int{}, acts: map[string]actInfo{}, claimLabels: map[string]string{}, wantPeople: map[string]bool{},
		actSeen: map[string]bool{}}
	steps := []func(context.Context) error{
		b.loadClaimsAsOf, b.loadFirstEvidence, b.loadAccount, b.loadOpportunities, b.loadPeople, b.loadActivities, b.loadClaims,
		b.loadRelationships, b.loadOpportunityRoles, b.loadSignals, b.loadLearning, b.loadReferencedPeople,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	snap := b.finish()
	if err := ValidateSnapshot(snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// actInfo is what other tables need to know about an activity.
type actInfo struct {
	eventID      string
	label        string // Conversation or Activity
	visibility   string
	documentID   string // canonical Document node id for a docs activity, "" otherwise
	sourceDocRaw string
	occurred     time.Time
}

type builder struct {
	db        claimstore.DB
	accountID string
	cut       *time.Time                    // world-time cutoff (strictly before); nil builds the current projection
	claimsAt  map[string]claimstore.ClaimAt // claim standing at cut (cut mode only)
	actSeen   map[string]bool               // activity id -> visible at cut (cut mode only; a lookup cache)
	// episodesBefore, when set, keeps only the episodes whose newest trigger is earlier than it (a run's own
	// trigger: an earlier arm on the same trigger is not part of that run's world).
	episodesBefore *time.Time
	firstSeen      map[string]time.Time // "person:<id>" / "opportunity:<id>" -> first world-timed evidence (cut mode only)
	nodes          map[string]Node
	edges          map[string]Edge
	pending        []Edge
	skipped        map[string]int
	acts           map[string]actInfo
	claimLabels    map[string]string
	wantPeople     map[string]bool
	diffSignals    signalsByDiff
}

func (b *builder) addNode(n Node) { b.nodes[n.Key()] = n }

func (b *builder) skip(reason string) { b.skipped[reason]++ }

// addEdge queues an edge; endpoints are resolved in finish, once every node is loaded.
func (b *builder) addEdge(e Edge) {
	b.pending = append(b.pending, e)
	if e.FromLabel == LabelPerson {
		b.wantPeople[e.FromID] = true
	}
	if e.ToLabel == LabelPerson {
		b.wantPeople[e.ToID] = true
	}
}

func (b *builder) finish() Snapshot {
	for _, e := range b.pending {
		_, fromOK := b.nodes[NodeKey(e.FromLabel, e.FromID)]
		_, toOK := b.nodes[NodeKey(e.ToLabel, e.ToID)]
		if !fromOK || !toOK {
			b.skip(skipEndpointMissing)
			continue
		}
		b.edges[e.Key()] = e
	}
	snap := Snapshot{AccountID: b.accountID, Skipped: b.skipped}
	for _, n := range b.nodes {
		snap.Nodes = append(snap.Nodes, n)
	}
	for _, e := range b.edges {
		snap.Edges = append(snap.Edges, e)
	}
	sortSnapshot(&snap)
	return snap
}

// eventIDs maps activity ids to their source event ids (the Postgres evidence of the graph element).
func (b *builder) eventIDs(ctx context.Context, activityIDs []string) ([]string, error) {
	out := make([]string, 0, len(activityIDs))
	var unknown []string
	for _, id := range activityIDs {
		if a, ok := b.acts[id]; ok {
			out = append(out, a.eventID)
		} else {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		rows, err := b.db.QueryContext(ctx, `SELECT source_event_id::text FROM activities WHERE id = ANY($1::uuid[])`, signalstore.UUIDArray(unknown))
		if err != nil {
			return nil, fmt.Errorf("ctxgraph: look up source events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return sortedUnique(out...), nil
}

// evidence returns the evidence_activity_ids and source_event_ids properties for activity ids.
func (b *builder) evidence(ctx context.Context, activityIDs ...string) (acts, events []string, err error) {
	acts = sortedUnique(activityIDs...)
	events, err = b.eventIDs(ctx, acts)
	return acts, events, err
}

// edgeProps builds the common edge properties; extra carries confidence, standing, basis and the like.
func edgeProps(validFrom time.Time, validTo *time.Time, status string, acts, events []string, created, updated time.Time, extra map[string]any) map[string]any {
	p := map[string]any{
		"valid_from": ts(validFrom), "status": status,
		"evidence_activity_ids": acts, "source_event_ids": events,
		"created_at": ts(created), "updated_at": ts(updated),
	}
	if v, ok := tsPtr(validTo); ok {
		p["valid_to"] = v
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func nodeProps(table string, acts, events []string, created, updated time.Time, extra map[string]any) map[string]any {
	p := map[string]any{
		"pg_table": table, "evidence_activity_ids": acts, "source_event_ids": events,
		"created_at": ts(created), "updated_at": ts(updated),
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// round3 matches numeric(4,3): the graph and Postgres agree to the digit.
func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

func nullStr(s sql.NullString) string { return s.String }

func (b *builder) loadAccount(ctx context.Context) error {
	var name string
	var archived sql.NullTime
	var created, updated time.Time
	err := b.db.QueryRowContext(ctx, `SELECT name, archived_at, created_at, updated_at FROM accounts WHERE id = $1::uuid`, b.accountID).
		Scan(&name, &archived, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("ctxgraph: read account: %w", err)
	}
	status := "active"
	if archived.Valid {
		status = "archived"
	}
	b.addNode(newNode([]string{LabelAccount}, b.accountID, b.accountID,
		nodeProps("accounts", []string{}, []string{}, created, updated, map[string]any{"name": name, "status": status})))
	return nil
}

func (b *builder) loadOpportunities(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `SELECT id::text, name, motion, owner_person_id::text, created_at, updated_at
 FROM opportunities WHERE account_id = $1::uuid`, b.accountID)
	if err != nil {
		return fmt.Errorf("ctxgraph: read opportunities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, motion string
		var owner sql.NullString
		var created, updated time.Time
		if err := rows.Scan(&id, &name, &motion, &owner, &created, &updated); err != nil {
			return err
		}
		if !b.evidencedBeforeCut("opportunity", id) {
			continue // no world-timed evidence before the cutoff: the deal did not exist for the world yet
		}
		extra := map[string]any{"name": name, "motion": motion}
		if owner.Valid && b.cut == nil { // the owner is overwritten in place: no value of it at a past time
			extra["owner_person_id"] = owner.String
			b.wantPeople[owner.String] = true
		}
		b.addNode(newNode([]string{LabelOpportunity}, id, b.accountID, nodeProps("opportunities", []string{}, []string{}, created, updated, extra)))
	}
	return rows.Err()
}

func (b *builder) loadPeople(ctx context.Context) error {
	if b.cut != nil { // the account's people at the cutoff are the ones its evidence names, not people.account_id
		return b.readPeople(ctx, `WHERE id = ANY($1::uuid[])`, signalstore.UUIDArray(b.evidencedIDs("person")))
	}
	return b.readPeople(ctx, `WHERE account_id = $1::uuid`, b.accountID)
}

// loadReferencedPeople loads the people other tables pointed at that are not contacts of this account
// (employees, mostly). They are global nodes: the same row yields the same node from every account.
func (b *builder) loadReferencedPeople(ctx context.Context) error {
	var missing []string
	for id := range b.wantPeople {
		if _, ok := b.nodes[NodeKey(LabelPerson, id)]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return b.readPeople(ctx, `WHERE id = ANY($1::uuid[])`, signalstore.UUIDArray(missing))
}

func (b *builder) readPeople(ctx context.Context, where string, arg any) error {
	rows, err := b.db.QueryContext(ctx, `SELECT id::text, kind, display_name, title, account_id::text, merged_into::text, internal_only, created_at, updated_at
 FROM people `+where, arg)
	if err != nil {
		return fmt.Errorf("ctxgraph: read people: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, name string
		var title, acct, merged sql.NullString
		var internal bool
		var created, updated time.Time
		if err := rows.Scan(&id, &kind, &name, &title, &acct, &merged, &internal, &created, &updated); err != nil {
			return err
		}
		extra := map[string]any{"kind": kind, "internal_only": internal}
		scope := ""
		if b.cut != nil {
			// display_name, title, merged_into and people.account_id are overwritten in place (a re-attribution
			// moves a contact between accounts): none has a value at a past time. A contact belongs to the
			// account whose evidence before the cutoff names it.
			if !b.evidencedBeforeCut("person", id) {
				continue
			}
			if kind == "contact" {
				scope = b.accountID
			}
			b.addNode(newNode([]string{LabelPerson}, id, scope, nodeProps("people", []string{}, []string{}, created, updated, extra)))
			continue
		}
		extra["display_name"] = name
		if title.Valid {
			extra["title"] = title.String
		}
		if merged.Valid {
			extra["merged_into"] = merged.String
		}
		if acct.Valid {
			if acct.String != b.accountID { // a contact of another account belongs to that account's projection
				continue
			}
			scope = b.accountID
		}
		b.addNode(newNode([]string{LabelPerson}, id, scope, nodeProps("people", []string{}, []string{}, created, updated, extra)))
	}
	return rows.Err()
}
