package ctxgraph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// RebuildReport is the outcome of projecting every account from Postgres.
type RebuildReport struct {
	Accounts   int            `json:"accounts"`
	Wiped      bool           `json:"wiped"`
	Nodes      int64          `json:"nodes"` // as Neo4j counts them afterwards
	Edges      int64          `json:"edges"`
	Digest     string         `json:"digest"` // over every stored node and edge hash
	Skipped    map[string]int `json:"skipped"`
	DurationMS int64          `json:"duration_ms"`
}

// inFlightWait bounds how long a rebuild waits for running projections to finish.
const inFlightWait = 2 * time.Minute

// Rebuild projects every account from Postgres. With wipe it first empties Neo4j, which is how a deleted or
// corrupt graph is reconstructed: Postgres alone is enough.
//
// It is safe next to a running projector: a manual job per account is enqueued first, so the barrier stays
// closed (no agent reads the half-built graph) until the projector has drained them after the rebuild; the
// exclusive advisory lock stops every projector from claiming a job meanwhile, and running projections are
// waited out before the wipe.
func (p *Projector) Rebuild(ctx context.Context, wipe bool) (RebuildReport, error) {
	start := p.clk.Now()
	ids, err := p.accountIDs(ctx)
	if err != nil {
		return RebuildReport{}, err
	}
	for _, id := range ids {
		if err := Enqueue(ctx, p.db, id, nil, ReasonManual, start); err != nil {
			return RebuildReport{}, err
		}
	}
	rep, err := p.rebuildLocked(ctx, ids, wipe)
	if err != nil {
		return rep, err
	}
	if _, err := p.Drain(ctx); err != nil { // complete the manual jobs: the barrier opens
		return rep, err
	}
	rep.DurationMS = p.clk.Now().Sub(start).Milliseconds()
	return rep, nil
}

func (p *Projector) rebuildLocked(ctx context.Context, ids []string, wipe bool) (RebuildReport, error) {
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return RebuildReport{}, fmt.Errorf("ctxgraph: rebuild connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, rebuildLockKey); err != nil {
		return RebuildReport{}, fmt.Errorf("ctxgraph: take the rebuild lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, rebuildLockKey)
	}()
	if err := p.waitNoneInFlight(ctx); err != nil {
		return RebuildReport{}, err
	}
	if err := p.g.ensureSchema(ctx); err != nil {
		return RebuildReport{}, err
	}
	if wipe {
		if err := p.g.wipe(ctx); err != nil {
			return RebuildReport{}, err
		}
	}
	rep := RebuildReport{Wiped: wipe, Accounts: len(ids), Skipped: map[string]int{}}
	for _, id := range ids {
		res, err := p.ProjectAccount(ctx, id)
		if err != nil {
			return rep, fmt.Errorf("ctxgraph: rebuild account %s: %w", id, err)
		}
		for k, v := range res.Skipped {
			rep.Skipped[k] += v
		}
	}
	if rep.Nodes, rep.Edges, err = p.g.Counts(ctx); err != nil {
		return rep, err
	}
	nodes, edges, err := p.g.storedAll(ctx)
	if err != nil {
		return rep, err
	}
	rep.Digest = Digest(hashesOf(nodes, edges))
	return rep, nil
}

// waitNoneInFlight waits until no projection holds a live lease. With the rebuild lock held nothing new starts.
func (p *Projector) waitNoneInFlight(ctx context.Context) error {
	deadline := time.Now().Add(inFlightWait)
	for {
		var busy bool
		err := p.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM graph_projection_jobs
 WHERE claimed_at IS NOT NULL AND completed_at IS NULL AND lease_expires_at > now())`).Scan(&busy)
		if err != nil {
			return fmt.Errorf("ctxgraph: check in-flight projections: %w", err)
		}
		if !busy {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("ctxgraph: rebuild gave up waiting for running projections to finish")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (p *Projector) accountIDs(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id::text FROM accounts ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: list accounts: %w", err)
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

// DriftReport compares Postgres' canonical records with the graph. Drift is the number of elements
// that differ; zero means the graph is exactly what Postgres says it should be.
type DriftReport struct {
	AccountID     string   `json:"account_id,omitempty"` // empty: every account
	ExpectedNodes int      `json:"expected_nodes"`
	ExpectedEdges int      `json:"expected_edges"`
	GraphNodes    int      `json:"graph_nodes"`
	GraphEdges    int      `json:"graph_edges"`
	Missing       []string `json:"missing"`    // in Postgres, not in the graph
	Extra         []string `json:"extra"`      // in the graph, not in Postgres
	Mismatched    []string `json:"mismatched"` // in both, with different content
	Tampered      []string `json:"tampered"`   // stored properties do not match the stored hash
	// Whole-graph checks (every account only): Neo4j's own node and relationship totals against Postgres'
	// expectation, and any label or relationship type outside the ontology.
	TotalNodes      int64    `json:"total_nodes,omitempty"`
	TotalEdges      int64    `json:"total_edges,omitempty"`
	CountMismatch   bool     `json:"count_mismatch"`
	OutsideOntology []string `json:"outside_ontology"`
	Drift           int      `json:"drift"`
}

// Drift checks one account (accountID set) or every account (empty).
func (p *Projector) Drift(ctx context.Context, accountID string) (DriftReport, error) {
	ids := []string{accountID}
	if accountID == "" {
		var err error
		if ids, err = p.accountIDs(ctx); err != nil {
			return DriftReport{}, err
		}
	}
	expected := map[string]string{}
	var snaps []Snapshot
	conflicts := map[string]bool{}
	for _, id := range ids {
		snap, err := p.snapshot(ctx, id)
		if err != nil {
			return DriftReport{}, err
		}
		snaps = append(snaps, snap)
		for k, h := range snap.Hashes() {
			if prev, ok := expected[k]; ok && prev != h {
				conflicts[k] = true // two accounts project one global node differently: a projection bug
			}
			expected[k] = h
		}
	}
	var nodes, edges map[string]Stored
	var err error
	if accountID == "" {
		nodes, edges, err = p.g.storedAll(ctx)
	} else {
		nodes, edges, err = p.g.storedForAccount(ctx, accountID, snaps[0])
	}
	if err != nil {
		return DriftReport{}, err
	}
	rep := compareHashes(expected, hashesOf(nodes, edges), conflicts)
	rep.AccountID = accountID
	rep.GraphNodes, rep.GraphEdges = len(nodes), len(edges)
	for _, s := range snaps {
		rep.ExpectedNodes += len(s.Nodes)
		rep.ExpectedEdges += len(s.Edges)
	}
	if accountID == "" { // global nodes shared by several accounts count once
		rep.ExpectedNodes, rep.ExpectedEdges = countKinds(expected)
	}
	rep.Tampered = tampered(nodes, edges)
	rep.OutsideOntology = []string{}
	if accountID == "" {
		if err := p.wholeGraphChecks(ctx, &rep); err != nil {
			return DriftReport{}, err
		}
	}
	rep.Drift = distinct(rep.Missing, rep.Extra, rep.Mismatched, rep.Tampered, rep.OutsideOntology)
	if rep.CountMismatch {
		rep.Drift++
	}
	return rep, nil
}

// wholeGraphChecks compares Neo4j's raw totals with the expectation (this catches elements the keyed
// comparison cannot see, such as a node without an id) and lists labels and types outside the ontology.
func (p *Projector) wholeGraphChecks(ctx context.Context, rep *DriftReport) error {
	nodes, edges, err := p.g.Counts(ctx)
	if err != nil {
		return err
	}
	rep.TotalNodes, rep.TotalEdges = nodes, edges
	rep.CountMismatch = nodes != int64(rep.ExpectedNodes) || edges != int64(rep.ExpectedEdges)
	outside, err := p.g.outsideOntology(ctx)
	if err != nil {
		return err
	}
	rep.OutsideOntology = outside
	return nil
}

// distinct counts the elements named by any list once (an extra element can also be tampered).
func distinct(lists ...[]string) int {
	seen := map[string]bool{}
	for _, l := range lists {
		for _, k := range l {
			seen[k] = true
		}
	}
	return len(seen)
}

func countKinds(h map[string]string) (nodes, edges int) {
	for k := range h {
		if k[0] == 'N' {
			nodes++
		} else {
			edges++
		}
	}
	return nodes, edges
}

func compareHashes(expected, actual map[string]string, conflicts map[string]bool) DriftReport {
	rep := DriftReport{Missing: []string{}, Extra: []string{}, Mismatched: []string{}, Tampered: []string{}}
	for _, k := range sortedKeys(expected) {
		got, ok := actual[k]
		switch {
		case !ok:
			rep.Missing = append(rep.Missing, k)
		case got != expected[k] || conflicts[k]:
			rep.Mismatched = append(rep.Mismatched, k)
		}
	}
	for _, k := range sortedKeys(actual) {
		if _, ok := expected[k]; !ok {
			rep.Extra = append(rep.Extra, k)
		}
	}
	return rep
}

func tampered(nodes, edges map[string]Stored) []string {
	out := []string{}
	for _, m := range []map[string]Stored{nodes, edges} {
		for k, s := range m {
			if s.Tampered() {
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
