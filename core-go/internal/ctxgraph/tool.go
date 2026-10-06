package ctxgraph

import "context"

// compactNode is a node of the ctx tool packet: ids, label, status, validity and the activity ids of the
// evidence. The packet is bounded to 12 KiB, so nothing else is carried.
type compactNode struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Label    string        `json:"label"`
	Status   string        `json:"status,omitempty"`
	From     string        `json:"valid_from,omitempty"`
	To       string        `json:"valid_to,omitempty"`
	Evidence []EvidenceRef `json:"evidence_refs,omitempty"`
	Withheld string        `json:"withheld,omitempty"`
}

type compactEdge struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	RelType string `json:"rel_type"`
	Status  string `json:"status"`
}

// NeighborhoodItems serves the graph_neighborhood ctx tool (corectx.GraphReader): one item per section
// in reading order, the first (account) carrying the projection status, the last the edges between the
// returned nodes. Evidence is a list of Postgres activity ids; the draft agent fetches the exact
// evidence from the other tools. hidden are the non-org activities the run may not see.
func (r *Reader) NeighborhoodItems(ctx context.Context, accountID string, limit int, hidden map[string]bool) ([]any, bool, error) {
	view, err := r.Neighborhood(ctx, accountID, Params{SectionLimit: toolSectionLimit(limit)}, hidden)
	if err != nil {
		return nil, false, err
	}
	items, truncated := toolItems(view)
	return items, truncated, nil
}

// toolItems turns a neighborhood view into the packet items of the ctx tool and says whether anything was
// left out or withheld.
func toolItems(view View) ([]any, bool) {
	byID := make(map[string]ViewNode, len(view.Nodes))
	for _, n := range view.Nodes {
		byID[n.ID] = n
	}
	var items []any
	for _, name := range SectionNames {
		ids := view.Sections[name]
		if len(ids) == 0 {
			continue
		}
		nodes := make([]compactNode, 0, len(ids))
		for _, id := range ids {
			nodes = append(nodes, compact(byID[id]))
		}
		item := map[string]any{"section": name, "nodes": nodes}
		if name == SectionAccount {
			item["projection"] = view.Projection
			item["withheld"] = view.Withheld
		}
		items = append(items, item)
	}
	edges := make([]compactEdge, 0, len(view.Edges))
	for _, e := range view.Edges {
		edges = append(edges, compactEdge{Source: e.Source, Target: e.Target, RelType: e.RelType, Status: e.Status})
	}
	if len(edges) > 0 {
		items = append(items, map[string]any{"section": SectionEdges, "edges": edges})
	}
	return items, view.Truncated || view.Withheld
}

func compact(n ViewNode) compactNode {
	c := compactNode{ID: n.ID, Type: n.Type, Label: n.Label, Status: n.Status, From: n.ValidFrom, To: n.ValidTo, Withheld: n.Withheld}
	for _, e := range n.EvidenceRefs {
		c.Evidence = append(c.Evidence, e)
	}
	return c
}
