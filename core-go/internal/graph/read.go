package graph

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// GraphActivityLimit caps how many of an account's most recent activities appear in Graph.
const GraphActivityLimit = 50

const edgeColumns = `r.id::text, r.src_type, r.src_id::text, r.rel_type, r.dst_type, r.dst_id::text, r.standing,
 r.confidence::float8, coalesce(r.source_activity_id::text, ''), r.evidence, r.valid_from, r.valid_to`

func scanEdge(r rowScanner) (Edge, error) {
	var e Edge
	var to sql.NullTime
	var evidence []byte
	var srcType, dstType string
	err := r.Scan(&e.ID, &srcType, &e.Src.ID, &e.Rel, &dstType, &e.Dst.ID, &e.Standing, &e.Confidence, &e.SourceActivityID, &evidence, &e.ValidFrom, &to)
	e.Src.Type, e.Dst.Type, e.Evidence = EntityType(srcType), EntityType(dstType), evidence
	e.ValidFrom = e.ValidFrom.UTC()
	if to.Valid {
		t := to.Time.UTC()
		e.ValidTo = &t
	}
	return e, err
}

// OpenEdges returns the open edges that touch the account: the account itself, its
// opportunities, its contacts and its activities. Employees and documents appear as the other
// end of those edges.
func OpenEdges(ctx context.Context, q DBTX, accountID string) ([]Edge, error) {
	return openEdges(ctx, q, accountID, 0)
}

// openEdges is OpenEdges limited to the newest activityLimit activities when it is positive.
func openEdges(ctx context.Context, q DBTX, accountID string, activityLimit int) ([]Edge, error) {
	const query = `
WITH scope(t, id) AS (
    SELECT 'account', $1::uuid
    UNION ALL SELECT 'opportunity', id FROM opportunities WHERE account_id = $1::uuid
    UNION ALL SELECT 'person', id FROM people WHERE account_id = $1::uuid
    UNION ALL SELECT 'activity', id FROM (
        SELECT id FROM activities WHERE account_id = $1::uuid ORDER BY occurred_at DESC, id DESC
        LIMIT CASE WHEN $2::int > 0 THEN $2::int END) recent
)
SELECT ` + edgeColumns + `
  FROM relationships r
 WHERE r.valid_to IS NULL
   AND (EXISTS (SELECT 1 FROM scope s WHERE s.t = r.src_type AND s.id = r.src_id)
     OR EXISTS (SELECT 1 FROM scope s WHERE s.t = r.dst_type AND s.id = r.dst_id))
   AND (r.src_type <> 'activity' OR EXISTS (SELECT 1 FROM scope s WHERE s.t = 'activity' AND s.id = r.src_id))
   AND (r.dst_type <> 'activity' OR EXISTS (SELECT 1 FROM scope s WHERE s.t = 'activity' AND s.id = r.dst_id))
 ORDER BY r.valid_from, r.id`
	rows, err := q.QueryContext(ctx, query, accountID, activityLimit)
	if err != nil {
		return nil, fmt.Errorf("graph: open edges: %w", err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		e, err := scanEdge(rows)
		if err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read edges: %w", err)
	}
	return out, nil
}

// PersonIdentities lists every source identity ever mapped to the person: current mappings
// first, then closed ones (history), each group in a stable order.
func PersonIdentities(ctx context.Context, q DBTX, personID string) ([]Mapping, error) {
	rows, err := q.QueryContext(ctx, `
SELECT `+mappingColumns+` FROM entity_source_mappings
 WHERE entity_type = 'person' AND entity_id = $1::uuid
 ORDER BY (valid_to IS NOT NULL), source_system, source_key, valid_from`, personID)
	if err != nil {
		return nil, fmt.Errorf("graph: person identities: %w", err)
	}
	defer rows.Close()
	var out []Mapping
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("graph: scan mapping: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read identities: %w", err)
	}
	return out, nil
}

// GraphNode is core.yaml components.schemas.Graph.nodes[].
type GraphNode struct {
	ID    string         `json:"id"`
	Type  string         `json:"type"`
	Label string         `json:"label"`
	Data  map[string]any `json:"data,omitempty"`
}

// GraphEdge is core.yaml components.schemas.Graph.edges[].
type GraphEdge struct {
	ID               string  `json:"id"`
	Source           string  `json:"source"`
	Target           string  `json:"target"`
	RelType          string  `json:"rel_type"`
	Standing         string  `json:"standing"`
	Confidence       float64 `json:"confidence"`
	SourceActivityID *string `json:"source_activity_id"`
}

// GraphView is core.yaml components.schemas.Graph.
type GraphView struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Graph returns the nodes and open edges around an account for GET /accounts/{id}/graph. Only
// the GraphActivityLimit most recent activities are included. Edge endpoints are always nodes.
// Nodes are loaded with one query per node type.
func Graph(ctx context.Context, q DBTX, accountID string) (GraphView, error) {
	edges, err := openEdges(ctx, q, accountID, GraphActivityLimit)
	if err != nil {
		return GraphView{}, err
	}
	ids := map[EntityType]map[string]bool{EntityAccount: {accountID: true}}
	view := GraphView{Nodes: []GraphNode{}, Edges: make([]GraphEdge, 0, len(edges))}
	for _, e := range edges {
		for _, ref := range []EntityRef{e.Src, e.Dst} {
			if ids[ref.Type] == nil {
				ids[ref.Type] = map[string]bool{}
			}
			ids[ref.Type][ref.ID] = true
		}
		ge := GraphEdge{ID: e.ID, Source: e.Src.ID, Target: e.Dst.ID, RelType: e.Rel, Standing: e.Standing, Confidence: e.Confidence}
		if e.SourceActivityID != "" {
			id := e.SourceActivityID
			ge.SourceActivityID = &id
		}
		view.Edges = append(view.Edges, ge)
	}
	for _, typ := range []EntityType{EntityAccount, EntityPerson, EntityOpportunity, EntityActivity, EntityDocument} {
		if len(ids[typ]) == 0 {
			continue
		}
		nodes, err := loadNodes(ctx, q, typ, keys(ids[typ]))
		if err != nil {
			return GraphView{}, err
		}
		view.Nodes = append(view.Nodes, nodes...)
	}
	sort.Slice(view.Nodes, func(i, j int) bool {
		if view.Nodes[i].Type != view.Nodes[j].Type {
			return view.Nodes[i].Type < view.Nodes[j].Type
		}
		return view.Nodes[i].ID < view.Nodes[j].ID
	})
	return view, nil
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadNodes resolves the labels and a little data of all nodes of one type in one query.
// Documents have no table: their label is the source document id of the activity that shared
// them, and a document with no source activity is labelled by its node id.
func loadNodes(ctx context.Context, q DBTX, typ EntityType, ids []string) ([]GraphNode, error) {
	var query string
	switch typ {
	case EntityAccount:
		query = `SELECT id::text, name, jsonb_build_object('domain', coalesce(domain, '')) FROM accounts WHERE id = ANY($1::uuid[])`
	case EntityPerson:
		query = `SELECT id::text, display_name, jsonb_build_object('kind', kind, 'title', coalesce(title, ''), 'email', coalesce(primary_email, ''))
		           FROM people WHERE id = ANY($1::uuid[])`
	case EntityOpportunity:
		query = `SELECT id::text, name, jsonb_build_object('motion', motion) FROM opportunities WHERE id = ANY($1::uuid[])`
	case EntityActivity:
		query = `SELECT id::text, coalesce(summary, activity_type), jsonb_build_object('activity_type', activity_type)
		           FROM activities WHERE id = ANY($1::uuid[])`
	case EntityDocument:
		query = `SELECT d.id, coalesce(min(a.source_object_id), 'document ' || d.id), '{}'::jsonb
		           FROM unnest($1::text[]) AS d(id)
		           LEFT JOIN relationships r ON (r.src_type = 'document' AND r.src_id::text = d.id) OR (r.dst_type = 'document' AND r.dst_id::text = d.id)
		           LEFT JOIN activities a ON a.id = r.source_activity_id
		          GROUP BY d.id`
	default:
		return nil, nil
	}
	rows, err := q.QueryContext(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("graph: load %s nodes: %w", typ, err)
	}
	defer rows.Close()
	var out []GraphNode
	for rows.Next() {
		n := GraphNode{Type: string(typ)}
		var data []byte
		if err := rows.Scan(&n.ID, &n.Label, &data); err != nil {
			return nil, fmt.Errorf("graph: scan %s node: %w", typ, err)
		}
		if n.Data, err = decodeData(data); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read %s nodes: %w", typ, err)
	}
	return out, nil
}
