package biwriter

import "sort"

// maxGraphItems is claims[].graph_diff_items maxItems.
const maxGraphItems = 10

// family is the graph elements that can carry a change dimension: the node labels and relationship types of
// contracts/graph/ontology.v1.json that a change of that dimension writes.
type family struct{ nodes, edges map[string]bool }

func setOf(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

var graphFamilies = map[string]family{
	dimStakeholders: {nodes: setOf("Person"), edges: setOf("WORKS_AT", "INFLUENCES", "REPORTS_TO", "PARTICIPATED_IN", "INVOLVES")},
	dimOwnership:    {edges: setOf("CHAMPION_FOR", "ECONOMIC_BUYER_FOR", "TECHNICAL_EVALUATOR_FOR", "OWNS", "DELEGATED_TO")},
	dimBlockers:     {nodes: setOf("Claim", "Signal")},
	dimNextStep:     {nodes: setOf("Commitment")},
	dimIntent:       {nodes: setOf("Signal")},
}

// graphItemsFor selects the graph-diff items a claim of the dimension also rests on: attributed to the event,
// of a kind that carries the dimension, and (when the diff says which activities they are evidence for)
// linked to one of the claim's activities. The result is sorted so the claim is reproducible.
func graphItemsFor(dimension string, claimActivities map[string]bool, items []GraphItem) []GraphDiffItem {
	fam, ok := graphFamilies[dimension]
	if !ok {
		return nil
	}
	var out []GraphDiffItem
	for _, it := range items {
		if !it.Attributed || !carries(fam, it) || !linked(claimActivities, it.ActivityIDs) {
			continue
		}
		out = append(out, GraphDiffItem{Kind: it.Kind, Type: it.Type, ID: it.ID})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	if len(out) > maxGraphItems {
		out = out[:maxGraphItems]
	}
	return out
}

func carries(f family, it GraphItem) bool {
	if it.Kind == "edge" {
		return f.edges[it.Type]
	}
	return f.nodes[it.Type]
}

// linked is true when the item names no activities, or one of them is among the claim's.
func linked(claim map[string]bool, item []string) bool {
	if len(item) == 0 {
		return true
	}
	for _, id := range item {
		if claim[id] {
			return true
		}
	}
	return false
}
