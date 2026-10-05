// Package ctxgraph projects the Postgres activity graph into Neo4j (HAR-96 Architecture update, HAR-129 1B).
//
// Postgres stays authoritative. Neo4j is a rebuildable, materialized projection:
//
//	Postgres commit -> transactional outbox job -> snapshot of the account's canonical records
//	-> idempotent Neo4j upsert (only what changed) -> recorded graph diff -> projection checkpoint
//
// Only the Projector writes to Neo4j; agents, Slack and web handlers read it through Neighborhood.
// Everything the projector may write is listed in contracts/graph/ontology.v1.json (mirrored in
// ontology.go and conformance-tested).
package ctxgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// tsLayout is the fixed-width UTC layout of every timestamp property: it sorts as text and is
// byte-identical for the same Postgres value, which the content hash relies on.
const tsLayout = "2006-01-02T15:04:05.000000Z"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

func tsPtr(t *time.Time) (string, bool) {
	if t == nil {
		return "", false
	}
	return ts(*t), true
}

// Node is one projected vertex. Labels[0] is the primary label (the ontology label); a Conversation
// also carries the Activity label.
type Node struct {
	Labels []string
	ID     string
	Scope  string // account id; "" for a global node (employees, documents, knowledge)
	Props  map[string]any
}

// Primary returns the primary label.
func (n Node) Primary() string { return n.Labels[0] }

// Key identifies the node across Postgres-derived and Neo4j-read snapshots.
func (n Node) Key() string { return NodeKey(n.Primary(), n.ID) }

// NodeKey is "N|<Label>|<id>".
func NodeKey(label, id string) string { return "N|" + label + "|" + id }

// Hash returns the content hash property h.
func (n Node) Hash() string { s, _ := n.Props["h"].(string); return s }

// Edge is one projected relationship.
type Edge struct {
	Type      string
	ID        string
	FromLabel string // primary label of the source node
	FromID    string
	ToLabel   string // primary label of the target node
	ToID      string
	Scope     string // account whose projection owns the edge
	Props     map[string]any
}

// Key identifies the edge: "E|<TYPE>|<id>".
func (e Edge) Key() string { return EdgeKey(e.Type, e.ID) }

// EdgeKey is "E|<TYPE>|<id>".
func EdgeKey(typ, id string) string { return "E|" + typ + "|" + id }

// Hash returns the content hash property h.
func (e Edge) Hash() string { s, _ := e.Props["h"].(string); return s }

// Snapshot is the canonical projection of one account, derived from Postgres alone.
type Snapshot struct {
	AccountID string
	Nodes     []Node
	Edges     []Edge
	// Skipped counts Postgres records that have no place in the ontology, by reason. It is reported,
	// never silently dropped, and part of the checkpoint.
	Skipped map[string]int
}

// Hashes maps every node and edge key to its content hash.
func (s Snapshot) Hashes() map[string]string {
	out := make(map[string]string, len(s.Nodes)+len(s.Edges))
	for _, n := range s.Nodes {
		out[n.Key()] = n.Hash()
	}
	for _, e := range s.Edges {
		out[e.Key()] = e.Hash()
	}
	return out
}

// Digest is one hash over every node and edge hash: equal digests mean equal canonical meaning.
func Digest(hashes map[string]string) string {
	keys := make([]string, 0, len(hashes))
	for k := range hashes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, hashes[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// contentHash hashes the labels and every property except h itself. Property values are strings,
// numbers, booleans and string lists, so encoding/json (sorted map keys) is canonical.
func contentHash(parts ...any) string {
	raw, err := json.Marshal(parts)
	if err != nil { // unreachable for the value kinds the builders use
		panic(fmt.Sprintf("ctxgraph: hash: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func newNode(labels []string, id, scope string, props map[string]any) Node {
	p := cloneProps(props)
	p["id"] = id
	if scope != "" {
		p["account_id"] = scope
	}
	p["h"] = contentHash("node", labels, p)
	return Node{Labels: labels, ID: id, Scope: scope, Props: p}
}

func newEdge(typ, id, fromLabel, fromID, toLabel, toID, scope string, props map[string]any) Edge {
	p := cloneProps(props)
	p["id"] = id
	p["account_id"] = scope
	p["h"] = contentHash("edge", typ, fromLabel, fromID, toLabel, toID, p)
	return Edge{Type: typ, ID: id, FromLabel: fromLabel, FromID: fromID, ToLabel: toLabel, ToID: toID, Scope: scope, Props: p}
}

func cloneProps(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// sortedUnique returns the distinct non-empty strings of in, sorted.
func sortedUnique(in ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func sortSnapshot(s *Snapshot) {
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].Key() < s.Nodes[j].Key() })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].Key() < s.Edges[j].Key() })
}

// mergeLabel is the label a node is MERGEd on: a Conversation is an Activity with an extra label.
func mergeLabel(primary string) string {
	if primary == LabelConversation {
		return LabelActivity
	}
	return primary
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}
