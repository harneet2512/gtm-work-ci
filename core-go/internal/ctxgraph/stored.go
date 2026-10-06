package ctxgraph

import (
	"context"
	"sort"
)

// Stored is a node or edge as Neo4j holds it.
type Stored struct {
	Key    string
	Hash   string // the stored h property
	Props  map[string]any
	Labels []string // node labels in canonical order
	// For edges: endpoint primary labels and ids.
	FromLabel, FromID, ToLabel, ToID string
	Type                             string
}

// Tampered reports whether the stored properties no longer hash to the stored h: someone changed the
// graph without going through the projector.
func (s Stored) Tampered() bool {
	p := make(map[string]any, len(s.Props))
	for k, v := range s.Props {
		if k != "h" {
			p[k] = normalizeValue(v)
		}
	}
	if s.Type == "" {
		return contentHash("node", s.Labels, p) != s.Hash
	}
	return contentHash("edge", s.Type, s.FromLabel, s.FromID, s.ToLabel, s.ToID, p) != s.Hash
}

// normalizeValue turns Neo4j list values ([]any) back into the []string the hash was computed over.
func normalizeValue(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]string, len(list))
	for i, e := range list {
		out[i], _ = e.(string)
	}
	return out
}

// canonLabels returns a node's labels in the order the projector writes them: Conversation, Activity
// for a conversation; the single ontology label otherwise.
func canonLabels(labels []string) []string {
	has := map[string]bool{}
	for _, l := range labels {
		has[l] = true
	}
	if has[LabelConversation] {
		return []string{LabelConversation, LabelActivity}
	}
	for _, l := range sortedLabels() {
		if has[l] {
			return []string{l}
		}
	}
	return nil
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (g *Graph) nodesQuery(ctx context.Context, label, where string, params map[string]any, into map[string]Stored) error {
	q, err := ident(label)
	if err != nil {
		return err
	}
	recs, err := g.read(ctx, "MATCH (n:"+q+") "+where+" RETURN labels(n) AS labels, properties(n) AS p", params)
	if err != nil {
		return err
	}
	for _, r := range recs {
		labels := canonLabels(anyStrings(r["labels"]))
		props, _ := r["p"].(map[string]any)
		id, _ := props["id"].(string)
		h, _ := props["h"].(string)
		if len(labels) == 0 || id == "" {
			continue // not ours: the whole-graph totals in the drift check count it
		}
		s := Stored{Key: NodeKey(labels[0], id), Hash: h, Props: props, Labels: labels}
		into[s.Key] = s
	}
	return nil
}

func (g *Graph) edgesQuery(ctx context.Context, typ, where string, params map[string]any, into map[string]Stored) error {
	q, err := ident(typ)
	if err != nil {
		return err
	}
	recs, err := g.read(ctx, "MATCH (a)-[e:"+q+"]->(b) "+where+
		" RETURN properties(e) AS p, labels(a) AS fl, a.id AS f, labels(b) AS tl, b.id AS t", params)
	if err != nil {
		return err
	}
	for _, r := range recs {
		props, _ := r["p"].(map[string]any)
		id, _ := props["id"].(string)
		h, _ := props["h"].(string)
		fl, tl := canonLabels(anyStrings(r["fl"])), canonLabels(anyStrings(r["tl"]))
		from, _ := r["f"].(string)
		to, _ := r["t"].(string)
		if len(fl) == 0 || len(tl) == 0 || id == "" {
			continue
		}
		s := Stored{Key: EdgeKey(typ, id), Hash: h, Props: props, Type: typ, FromLabel: fl[0], FromID: from, ToLabel: tl[0], ToID: to}
		into[s.Key] = s
	}
	return nil
}

// storedForAccount reads what the graph holds for one account: nodes and relationships scoped to it,
// plus the global nodes (employees, documents, knowledge) the snapshot refers to.
func (g *Graph) storedForAccount(ctx context.Context, accountID string, snap Snapshot) (map[string]Stored, map[string]Stored, error) {
	nodes, edges := map[string]Stored{}, map[string]Stored{}
	for _, l := range mergeLabels() {
		if err := g.nodesQuery(ctx, l, "WHERE n.account_id = $a", map[string]any{"a": accountID}, nodes); err != nil {
			return nil, nil, err
		}
	}
	globals := map[string][]any{}
	for _, n := range snap.Nodes {
		if n.Scope == "" {
			globals[mergeLabel(n.Primary())] = append(globals[mergeLabel(n.Primary())], n.ID)
		}
	}
	for l, ids := range globals {
		if err := g.nodesQuery(ctx, l, "WHERE n.id IN $ids", map[string]any{"ids": ids}, nodes); err != nil {
			return nil, nil, err
		}
	}
	for _, t := range sortedRelTypes() {
		if err := g.edgesQuery(ctx, t, "WHERE e.account_id = $a", map[string]any{"a": accountID}, edges); err != nil {
			return nil, nil, err
		}
	}
	return nodes, edges, nil
}

// storedAll reads the whole projection.
func (g *Graph) storedAll(ctx context.Context) (map[string]Stored, map[string]Stored, error) {
	nodes, edges := map[string]Stored{}, map[string]Stored{}
	for _, l := range mergeLabels() {
		if err := g.nodesQuery(ctx, l, "", nil, nodes); err != nil {
			return nil, nil, err
		}
	}
	for _, t := range sortedRelTypes() {
		if err := g.edgesQuery(ctx, t, "", nil, edges); err != nil {
			return nil, nil, err
		}
	}
	return nodes, edges, nil
}

func hashesOf(nodes, edges map[string]Stored) map[string]string {
	out := make(map[string]string, len(nodes)+len(edges))
	for k, s := range nodes {
		out[k] = s.Hash
	}
	for k, s := range edges {
		out[k] = s.Hash
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
