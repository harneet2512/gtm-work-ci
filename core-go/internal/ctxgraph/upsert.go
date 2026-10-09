package ctxgraph

import (
	"context"
	"fmt"
	"sort"
)

// exec runs one parameterised statement inside the projection's single Neo4j transaction.
type exec func(ctx context.Context, cypher string, params map[string]any) ([]record, error)

// batchSize is how many nodes or edges one UNWIND statement upserts.
const batchSize = 500

// neoProps converts a property map to values the Bolt driver packs (string lists as []any).
func neoProps(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if list, ok := v.([]string); ok {
			items := make([]any, len(list))
			for i, s := range list {
				items[i] = s
			}
			out[k] = items
			continue
		}
		out[k] = v
	}
	return out
}

type nodeGroup struct {
	label string // MERGE label
	conv  bool   // Conversation (extra label) rather than a plain Activity
	rows  []any
}

// upsertNodes writes the given nodes: MERGE on (label, id), then replace every property, so the stored
// node is exactly the snapshot node (stale properties disappear).
func upsertNodes(ctx context.Context, run exec, nodes []Node) error {
	groups := map[string]*nodeGroup{}
	for _, n := range nodes {
		ml := mergeLabel(n.Primary())
		conv := n.Primary() == LabelConversation
		k := fmt.Sprintf("%s|%t", ml, conv)
		if groups[k] == nil {
			groups[k] = &nodeGroup{label: ml, conv: conv}
		}
		groups[k].rows = append(groups[k].rows, map[string]any{"id": n.ID, "props": neoProps(n.Props)})
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		gr := groups[k]
		q, err := ident(gr.label)
		if err != nil {
			return err
		}
		cypher := "UNWIND $rows AS r MERGE (n:" + q + " {id: r.id}) SET n = r.props"
		switch {
		case gr.conv:
			cypher += " SET n:`Conversation`"
		case gr.label == LabelActivity:
			cypher += " REMOVE n:`Conversation`"
		}
		for _, chunk := range chunks(gr.rows) {
			if _, err := run(ctx, cypher, map[string]any{"rows": chunk}); err != nil {
				return err
			}
		}
	}
	return nil
}

type edgeGroup struct {
	typ, from, to string
	rows          []any
}

// upsertEdges writes the given edges. Every endpoint must already exist (nodes are upserted first);
// an edge whose endpoints are missing is an error, never a silent skip.
func upsertEdges(ctx context.Context, run exec, edges []Edge) error {
	groups := map[string]*edgeGroup{}
	for _, e := range edges {
		from, to := mergeLabel(e.FromLabel), mergeLabel(e.ToLabel)
		k := e.Type + "|" + from + "|" + to
		if groups[k] == nil {
			groups[k] = &edgeGroup{typ: e.Type, from: from, to: to}
		}
		groups[k].rows = append(groups[k].rows, map[string]any{"id": e.ID, "from": e.FromID, "to": e.ToID, "props": neoProps(e.Props)})
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		gr := groups[k]
		qt, err := ident(gr.typ)
		if err != nil {
			return err
		}
		qf, err := ident(gr.from)
		if err != nil {
			return err
		}
		qto, err := ident(gr.to)
		if err != nil {
			return err
		}
		cypher := "UNWIND $rows AS r MATCH (a:" + qf + " {id: r.from}) MATCH (b:" + qto + " {id: r.to}) " +
			"MERGE (a)-[e:" + qt + " {id: r.id}]->(b) SET e = r.props RETURN count(e) AS c"
		for _, chunk := range chunks(gr.rows) {
			recs, err := run(ctx, cypher, map[string]any{"rows": chunk})
			if err != nil {
				return err
			}
			if got := recs[0]["c"].(int64); got != int64(len(chunk)) {
				return fmt.Errorf("ctxgraph: %s upsert wrote %d of %d edges: an endpoint is missing from the graph", gr.typ, got, len(chunk))
			}
		}
	}
	return nil
}

// deleteStale removes the given nodes and edges, which Postgres no longer has. Edges go first.
func deleteStale(ctx context.Context, run exec, nodeKeys, edgeKeys []string) error {
	byType := map[string][]any{}
	for _, k := range edgeKeys {
		typ, id := splitKey(k)
		byType[typ] = append(byType[typ], id)
	}
	for typ, ids := range byType {
		q, err := ident(typ)
		if err != nil {
			return err
		}
		for _, chunk := range chunks(ids) {
			if _, err := run(ctx, "MATCH ()-[e:"+q+"]->() WHERE e.id IN $ids DELETE e", map[string]any{"ids": chunk}); err != nil {
				return err
			}
		}
	}
	byLabel := map[string][]any{}
	for _, k := range nodeKeys {
		label, id := splitKey(k)
		ml := mergeLabel(label)
		byLabel[ml] = append(byLabel[ml], id)
	}
	for label, ids := range byLabel {
		q, err := ident(label)
		if err != nil {
			return err
		}
		for _, chunk := range chunks(ids) {
			if _, err := run(ctx, "MATCH (n:"+q+") WHERE n.id IN $ids DETACH DELETE n", map[string]any{"ids": chunk}); err != nil {
				return err
			}
		}
	}
	return nil
}

func chunks(rows []any) [][]any {
	var out [][]any
	for len(rows) > 0 {
		n := min(batchSize, len(rows))
		out = append(out, rows[:n])
		rows = rows[n:]
	}
	return out
}

// splitKey splits "N|Label|id" or "E|TYPE|id" into (Label|TYPE, id).
func splitKey(k string) (kind, id string) {
	rest := k[2:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '|' {
			return rest[:i], rest[i+1:]
		}
	}
	return rest, ""
}
