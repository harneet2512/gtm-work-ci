package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

// ErrAccountUnknown means Postgres has no such account.
var ErrAccountUnknown = errors.New("ctxgraph: account not found")

// Params bound a neighborhood read.
type Params struct {
	// SectionLimit caps the nodes of each section (default 10, max 20).
	SectionLimit int
	// IncludeClosed adds history: closed relationships, superseded claims, expired signals and all
	// knowledge. Off, a reader sees only what currently holds.
	IncludeClosed bool
}

// Reader reads the Neo4j projection. It cannot write: Graph's mutating methods are unexported.
type Reader struct {
	g   *Graph
	db  *sql.DB
	clk clock.Clock
}

// NewReader returns a Reader. db is Postgres, used for the account check and projection status.
func NewReader(g *Graph, db *sql.DB, clk clock.Clock) *Reader {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Reader{g: g, db: db, clk: clk}
}

var roleRels = "CHAMPION_FOR|ECONOMIC_BUYER_FOR|TECHNICAL_EVALUATOR_FOR|INFLUENCES|WORKS_AT"

var applicableKnowledge = []any{"provisional", "supported", "confirmed"}

const nodeCols = "RETURN labels(n) AS labels, properties(n) AS p"

// Neighborhood returns the bounded graph around an account: account -> opportunities -> stakeholders ->
// recent activities -> supported claims and commitments -> recent signals -> decisions -> applicable
// knowledge, as ids, short labels and evidence refs. hidden are activity ids the reader may not see
// (nil for the operator view): hidden activities are dropped, nodes and edges whose evidence includes
// one are withheld, and View.Withheld says so.
func (r *Reader) Neighborhood(ctx context.Context, accountID string, p Params, hidden map[string]bool) (View, error) {
	p, err := normalizeParams(p)
	if err != nil {
		return View{}, err
	}
	if r.g == nil {
		return View{}, ErrGraphUnavailable // a reader built without Neo4j serves world-time reads only
	}
	if err := r.checkAccount(ctx, accountID); err != nil {
		return View{}, err
	}
	b := &viewBuilder{r: r, ctx: ctx, account: accountID, p: p, hidden: hidden, view: View{AccountID: accountID, Sections: map[string][]string{}}}
	if err := b.build(); err != nil {
		return View{}, err
	}
	if b.view.Projection, err = r.projection(ctx, accountID); err != nil {
		return View{}, err
	}
	return b.view, nil
}

// normalizeParams applies the default section limit and rejects one outside 1..MaxSectionLimit.
func normalizeParams(p Params) (Params, error) {
	if p.SectionLimit == 0 {
		p.SectionLimit = DefaultSectionLimit
	}
	if p.SectionLimit < 1 || p.SectionLimit > MaxSectionLimit {
		return p, fmt.Errorf("ctxgraph: section limit must be between 1 and %d", MaxSectionLimit)
	}
	return p, nil
}

func (r *Reader) checkAccount(ctx context.Context, accountID string) error {
	var one int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = $1::uuid`, accountID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountUnknown
	}
	if err != nil {
		return fmt.Errorf("ctxgraph: check account: %w", err)
	}
	return nil
}

func (r *Reader) projection(ctx context.Context, accountID string) (ProjectionStatus, error) {
	done, err := NewBarrier(r.db).Complete(ctx, accountID)
	if err != nil {
		return ProjectionStatus{}, err
	}
	st := ProjectionStatus{Complete: done}
	var at sql.NullTime
	err = r.db.QueryRowContext(ctx, `SELECT projected_at FROM graph_projection_checkpoints WHERE account_id = $1::uuid`, accountID).Scan(&at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, fmt.Errorf("ctxgraph: read checkpoint: %w", err)
	}
	if at.Valid {
		s := ts(at.Time)
		st.ProjectedAt = &s
	}
	return st, nil
}

type viewBuilder struct {
	r       *Reader
	ctx     context.Context
	account string
	p       Params
	hidden  map[string]bool
	mem     *memGraph // set for a world-time read: the sections come from this snapshot, not from Neo4j
	view    View
	seen    map[string]bool
	ids     map[string][]any // merge label -> node ids, for the edge query
}

func (b *viewBuilder) params(extra map[string]any) map[string]any {
	hidden := make([]any, 0, len(b.hidden))
	for id := range b.hidden {
		hidden = append(hidden, id)
	}
	m := map[string]any{"a": b.account, "lim": int64(b.p.SectionLimit + 1), "closed": b.p.IncludeClosed,
		"hidden": hidden, "now": ts(b.r.clk.Now())}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func (b *viewBuilder) build() error {
	b.seen, b.ids = map[string]bool{}, map[string][]any{}
	b.view.Nodes, b.view.Edges = []ViewNode{}, []ViewEdge{} // arrays in the contract, never null, even for a graph of one node
	sections := []struct {
		name, cypher string
		extra        map[string]any
		stubbed      bool // claims, commitments, signals, episodes are withheld whole when their evidence is hidden
	}{
		{SectionAccount, "MATCH (n:Account {id: $a}) " + nodeCols, nil, false},
		{SectionOpportunities, "MATCH (n:Opportunity) WHERE n.account_id = $a WITH n ORDER BY n.created_at DESC, n.id LIMIT $lim " + nodeCols, nil, false},
		{SectionStakeholders, "MATCH (n:Person)-[e:" + roleRels + "]->() WHERE e.account_id = $a AND ($closed OR e.valid_to IS NULL) " +
			"AND NOT any(x IN coalesce(e.evidence_activity_ids, []) WHERE x IN $hidden) WITH n, max(e.valid_from) AS last ORDER BY last DESC, n.id LIMIT $lim " + nodeCols, nil, false},
		{SectionActivities, "MATCH (n:Activity) WHERE n.account_id = $a AND NOT n.id IN $hidden WITH n ORDER BY n.occurred_at DESC, n.id LIMIT $lim " + nodeCols, nil, false},
		{SectionClaims, "MATCH (n:Claim) WHERE n.account_id = $a AND ($closed OR n.status = 'active') WITH n ORDER BY n.valid_from DESC, n.id LIMIT $lim " + nodeCols, nil, true},
		{SectionCommitments, "MATCH (n:Commitment) WHERE n.account_id = $a AND ($closed OR n.status = 'active') WITH n ORDER BY n.valid_from DESC, n.id LIMIT $lim " + nodeCols, nil, true},
		{SectionSignals, "MATCH (n:Signal) WHERE n.account_id = $a AND ($closed OR n.valid_to IS NULL OR n.valid_to >= $now) WITH n ORDER BY n.valid_from DESC, n.id LIMIT $lim " + nodeCols, nil, true},
		{SectionDecisions, "MATCH (n:DecisionEpisode) WHERE n.account_id = $a WITH n ORDER BY n.valid_from DESC, n.id LIMIT $lim " + nodeCols, nil, true},
		{SectionKnowledge, "MATCH (n:Knowledge)-[e:APPLIES_TO]->() WHERE e.account_id = $a AND ($closed OR n.status IN $applicable) " +
			"WITH n, min(e.valid_from) AS first ORDER BY first DESC, n.id LIMIT $lim " + nodeCols, map[string]any{"applicable": applicableKnowledge}, false},
	}
	for _, s := range sections {
		recs, err := b.sectionRecords(s.name, s.cypher, s.extra)
		if err != nil {
			return err
		}
		b.addSection(s.name, recs, s.stubbed)
	}
	if err := b.markHiddenActivities(); err != nil {
		return err
	}
	return b.loadEdges()
}

// sectionRecords reads one section: from Neo4j, or, for a world-time read, from the in-memory snapshot.
func (b *viewBuilder) sectionRecords(name, cypher string, extra map[string]any) ([]record, error) {
	if b.mem != nil {
		return b.mem.records(name, b), nil
	}
	return b.r.g.read(b.ctx, cypher, b.params(extra))
}

// addSection converts records to nodes, keeping at most SectionLimit and flagging truncation.
func (b *viewBuilder) addSection(name string, recs []record, stubbed bool) {
	ids := []string{}
	for i, rec := range recs {
		if i >= b.p.SectionLimit {
			b.view.Truncated = true
			break
		}
		s := storedFromRecord(rec)
		if s.Key == "" {
			continue
		}
		n := viewNode(s)
		if stubbed && touchesHidden(s.Props, b.hidden) {
			n = stub(n)
			b.view.Withheld = true
		}
		if b.seen[n.ID] {
			continue
		}
		b.seen[n.ID] = true
		b.view.Nodes = append(b.view.Nodes, n)
		ids = append(ids, n.ID)
		ml := mergeLabel(s.Labels[0])
		b.ids[ml] = append(b.ids[ml], n.ID)
	}
	b.view.Sections[name] = ids
}

func storedFromRecord(rec record) Stored {
	labels := canonLabels(anyStrings(rec["labels"]))
	props, _ := rec["p"].(map[string]any)
	id := stringProp(props, "id")
	if len(labels) == 0 || id == "" {
		return Stored{}
	}
	return Stored{Key: NodeKey(labels[0], id), Labels: labels, Props: props}
}

// markHiddenActivities sets Withheld when this account has activities the reader may not see.
func (b *viewBuilder) markHiddenActivities() error {
	if len(b.hidden) == 0 {
		return nil
	}
	if b.mem != nil {
		b.view.Withheld = b.mem.hasHiddenActivity(b)
		return nil
	}
	recs, err := b.r.g.read(b.ctx, "MATCH (n:Activity) WHERE n.account_id = $a AND n.id IN $hidden RETURN count(n) AS c", b.params(nil))
	if err != nil {
		return err
	}
	if c, _ := recs[0]["c"].(int64); c > 0 {
		b.view.Withheld = true
	}
	return nil
}

// loadEdges adds the edges whose both endpoints are in the neighborhood.
func (b *viewBuilder) loadEdges() error {
	all := make([]any, 0, len(b.seen))
	for id := range b.seen {
		all = append(all, id)
	}
	edges := map[string]Stored{}
	if b.mem != nil {
		edges = b.mem.edgesAmong(b)
	}
	for _, label := range mergeLabels() {
		if b.mem != nil || len(b.ids[label]) == 0 {
			continue
		}
		q, err := ident(label)
		if err != nil {
			return err
		}
		recs, err := b.r.g.read(b.ctx, "MATCH (a:"+q+") WHERE a.id IN $from MATCH (a)-[e]->(t) WHERE t.id IN $all AND e.account_id = $a "+
			"RETURN type(e) AS t, properties(e) AS p, labels(a) AS fl, a.id AS f, labels(t) AS tl, t.id AS to",
			map[string]any{"from": b.ids[label], "all": all, "a": b.account})
		if err != nil {
			return err
		}
		for _, rec := range recs {
			s := edgeFromRecord(rec)
			if s.Key != "" {
				edges[s.Key] = s
			}
		}
	}
	for _, k := range sortedKeys(edges) {
		s := edges[k]
		if touchesHidden(s.Props, b.hidden) {
			b.view.Withheld = true
			continue
		}
		if !b.p.IncludeClosed && !openStatuses[stringProp(s.Props, "status")] {
			continue
		}
		b.view.Edges = append(b.view.Edges, viewEdge(s))
	}
	sortEdges(b.view.Edges)
	return nil
}

func edgeFromRecord(rec record) Stored {
	props, _ := rec["p"].(map[string]any)
	typ, _ := rec["t"].(string)
	fl, tl := canonLabels(anyStrings(rec["fl"])), canonLabels(anyStrings(rec["tl"]))
	id := stringProp(props, "id")
	if typ == "" || id == "" || len(fl) == 0 || len(tl) == 0 {
		return Stored{}
	}
	from, _ := rec["f"].(string)
	to, _ := rec["to"].(string)
	return Stored{Key: EdgeKey(typ, id), Props: props, Type: typ, FromLabel: fl[0], FromID: from, ToLabel: tl[0], ToID: to}
}

// toolSectionLimit maps the ctx tool's item limit to the nodes per section: the packet is 12 KiB, so
// a section holds at most 5 nodes unless the caller asked for more than 10 items.
func toolSectionLimit(limit int) int {
	if limit > DefaultSectionLimit {
		return min(limit/2, MaxSectionLimit)
	}
	return 5
}
