package ctxgraph

import (
	"sort"
	"strings"
	"time"
)

// memGraph answers the section queries of Neighborhood over an in-memory Snapshot instead of Neo4j: the
// world-time read (ADR-0019) builds the snapshot from Postgres at the cutoff and reads it here, so the
// response is shaped by the same viewBuilder as a current read. Each method mirrors one Cypher query of
// viewBuilder.build (same filter, same order, same limit), and the parity test compares the two.
type memGraph struct {
	nodes []Node
	edges []Edge
	now   string // the read time as a timestamp property, for the signal expiry filter
}

func newMemGraph(s Snapshot, now time.Time) *memGraph {
	return &memGraph{nodes: s.Nodes, edges: s.Edges, now: ts(now)}
}

// records returns the nodes of one section, ordered and limited like the Cypher of build().
func (m *memGraph) records(section string, b *viewBuilder) []record {
	lim := b.p.SectionLimit + 1
	var picked []Node
	switch section {
	case SectionAccount:
		picked = m.filter(LabelAccount, func(n Node) bool { return n.ID == b.account })
	case SectionOpportunities:
		picked = sortBy(m.filter(LabelOpportunity, m.inAccount(b)), "created_at")
	case SectionStakeholders:
		picked = m.stakeholders(b)
	case SectionActivities:
		picked = sortBy(m.filter(LabelActivity, func(n Node) bool { return m.inAccount(b)(n) && !b.hidden[n.ID] }), "occurred_at")
	case SectionClaims:
		picked = sortBy(m.filter(LabelClaim, m.holding(b)), "valid_from")
	case SectionCommitments:
		picked = sortBy(m.filter(LabelCommitment, m.holding(b)), "valid_from")
	case SectionSignals:
		picked = sortBy(m.filter(LabelSignal, func(n Node) bool {
			to := stringProp(n.Props, "valid_to")
			return m.inAccount(b)(n) && (b.p.IncludeClosed || to == "" || to >= m.now)
		}), "valid_from")
	case SectionDecisions:
		picked = sortBy(m.filter(LabelDecisionEpisode, m.inAccount(b)), "valid_from")
	case SectionKnowledge:
		picked = m.knowledge(b)
	}
	if len(picked) > lim {
		picked = picked[:lim]
	}
	out := make([]record, len(picked))
	for i, n := range picked {
		labels := make([]any, len(n.Labels))
		for j, l := range n.Labels {
			labels[j] = l
		}
		out[i] = record{"labels": labels, "p": n.Props}
	}
	return out
}

// filter returns the nodes carrying the label (a Conversation carries Activity too) that pass keep.
func (m *memGraph) filter(label string, keep func(Node) bool) []Node {
	var out []Node
	for _, n := range m.nodes {
		if hasLabel(n, label) && keep(n) {
			out = append(out, n)
		}
	}
	return out
}

func hasLabel(n Node, label string) bool {
	for _, l := range n.Labels {
		if l == label {
			return true
		}
	}
	return false
}

func (m *memGraph) inAccount(b *viewBuilder) func(Node) bool {
	return func(n Node) bool { return stringProp(n.Props, "account_id") == b.account }
}

// holding keeps the account's nodes that currently hold (all of them with include_closed).
func (m *memGraph) holding(b *viewBuilder) func(Node) bool {
	return func(n Node) bool {
		return m.inAccount(b)(n) && (b.p.IncludeClosed || stringProp(n.Props, "status") == "active")
	}
}

// sortBy orders newest first by the string property (fixed-width timestamps sort as text), then by id.
func sortBy(ns []Node, prop string) []Node {
	sort.SliceStable(ns, func(i, j int) bool {
		a, c := stringProp(ns[i].Props, prop), stringProp(ns[j].Props, prop)
		if a != c {
			return a > c
		}
		return ns[i].ID < ns[j].ID
	})
	return ns
}

// stakeholders are the people with a qualifying role edge of the account, ordered by their newest role.
func (m *memGraph) stakeholders(b *viewBuilder) []Node {
	return m.byEdge(b, LabelPerson, true, func(e Edge) bool { return strings.Contains("|"+roleRels+"|", "|"+e.Type+"|") },
		func(e Edge) bool {
			return (b.p.IncludeClosed || stringProp(e.Props, "valid_to") == "") && !touchesHidden(e.Props, b.hidden)
		})
}

// knowledge is the knowledge applying to the account, ordered by when it first applied.
func (m *memGraph) knowledge(b *viewBuilder) []Node {
	return m.byEdge(b, LabelKnowledge, false, func(e Edge) bool { return e.Type == RelAppliesTo }, func(Edge) bool { return true })
}

// byEdge collects the label's nodes that are the source of a qualifying edge of the account, ordered by
// their newest (newest=true) or oldest valid_from over those edges, newest first.
func (m *memGraph) byEdge(b *viewBuilder, label string, newest bool, typ, keep func(Edge) bool) []Node {
	byID := map[string]Node{}
	for _, n := range m.nodes {
		if hasLabel(n, label) {
			byID[n.ID] = n
		}
	}
	at := map[string]string{}
	for _, e := range m.edges {
		n, ok := byID[e.FromID]
		if !ok || e.Scope != b.account || !typ(e) || !keep(e) || (label == LabelKnowledge && !b.p.IncludeClosed && !applicable(n)) {
			continue
		}
		from := stringProp(e.Props, "valid_from")
		if cur, seen := at[e.FromID]; !seen || (newest && from > cur) || (!newest && from < cur) {
			at[e.FromID] = from
		}
	}
	out := make([]Node, 0, len(at))
	for id := range at {
		out = append(out, byID[id])
	}
	sort.Slice(out, func(i, j int) bool {
		if at[out[i].ID] != at[out[j].ID] {
			return at[out[i].ID] > at[out[j].ID]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func applicable(n Node) bool {
	s := stringProp(n.Props, "status")
	for _, a := range applicableKnowledge {
		if a == s {
			return true
		}
	}
	return false
}

// hasHiddenActivity reports whether the account has an activity the reader may not see.
func (m *memGraph) hasHiddenActivity(b *viewBuilder) bool {
	for _, n := range m.filter(LabelActivity, m.inAccount(b)) {
		if b.hidden[n.ID] {
			return true
		}
	}
	return false
}

// edgesAmong returns the account's edges whose source is a returned node (in the label's id list) and
// whose target is any returned node.
func (m *memGraph) edgesAmong(b *viewBuilder) map[string]Stored {
	from := map[string]map[string]bool{}
	for label, ids := range b.ids {
		from[label] = map[string]bool{}
		for _, id := range ids {
			from[label][stringOf(id)] = true
		}
	}
	out := map[string]Stored{}
	for _, e := range m.edges {
		if e.Scope != b.account || !b.seen[e.ToID] || !from[mergeLabel(e.FromLabel)][e.FromID] {
			continue
		}
		out[e.Key()] = Stored{Key: e.Key(), Props: e.Props, Type: e.Type, FromLabel: e.FromLabel, FromID: e.FromID, ToLabel: e.ToLabel, ToID: e.ToID}
	}
	return out
}

func stringOf(v any) string { s, _ := v.(string); return s }
