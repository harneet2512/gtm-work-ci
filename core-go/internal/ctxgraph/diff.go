package ctxgraph

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Change ops of a graph diff.
const (
	OpAdded    = "added"
	OpChanged  = "changed"
	OpRemoved  = "removed"
	OpRepaired = "repaired" // the graph differed from Postgres without a Postgres change (tampering or a partial write)
)

// PropChange is one property whose value differs.
type PropChange struct {
	Before any `json:"before"`
	After  any `json:"after"`
}

// Change is one node or edge a projection added, changed or removed (HAR-129 1B "show exact graph diff").
type Change struct {
	Kind       string                `json:"kind"` // node | edge
	Op         string                `json:"op"`
	Type       string                `json:"type"` // label or relationship type
	ID         string                `json:"id"`
	From       string                `json:"from,omitempty"` // edges: "<Label>:<id>"
	To         string                `json:"to,omitempty"`
	BeforeHash string                `json:"before_hash,omitempty"`
	AfterHash  string                `json:"after_hash,omitempty"`
	Props      map[string]any        `json:"props,omitempty"`   // all properties of an added or removed element
	Changed    map[string]PropChange `json:"changed,omitempty"` // changed and repaired: the properties that differ
	Events     []string              `json:"source_event_ids"`  // the Postgres events this element is evidence-linked to
}

// Diff is the set of changes of one projection.
type Diff struct {
	Changes []Change `json:"changes"`
}

// Summary counts changes by op.
func (d Diff) Summary() map[string]int {
	out := map[string]int{OpAdded: 0, OpChanged: 0, OpRemoved: 0, OpRepaired: 0}
	for _, c := range d.Changes {
		out[c.Op]++
	}
	return out
}

// Empty reports whether the projection changed nothing.
func (d Diff) Empty() bool { return len(d.Changes) == 0 }

// computeDiff compares the snapshot with what the graph holds for the account.
func computeDiff(snap Snapshot, nodes, edges map[string]Stored) Diff {
	var out []Change
	have := map[string]bool{}
	for _, n := range snap.Nodes {
		have[n.Key()] = true
		if c, ok := diffElement("node", n.Primary(), n.ID, "", "", n.Hash(), n.Props, nodes[n.Key()]); ok {
			out = append(out, c)
		}
	}
	for _, e := range snap.Edges {
		have[e.Key()] = true
		from, to := e.FromLabel+":"+e.FromID, e.ToLabel+":"+e.ToID
		if c, ok := diffElement("edge", e.Type, e.ID, from, to, e.Hash(), e.Props, edges[e.Key()]); ok {
			out = append(out, c)
		}
	}
	out = append(out, removals(have, nodes, "node")...)
	out = append(out, removals(have, edges, "edge")...)
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
	return Diff{Changes: out}
}

// removals are the stored elements Postgres no longer has. The scoped read only returns elements of
// the account, so a removal is always this account's.
func removals(have map[string]bool, stored map[string]Stored, kind string) []Change {
	var out []Change
	for _, k := range sortedKeys(stored) {
		if have[k] {
			continue
		}
		s := stored[k]
		c := Change{Kind: kind, Op: OpRemoved, BeforeHash: s.Hash, Props: normalizeProps(s.Props), Events: eventsOf(s.Props)}
		if kind == "node" {
			c.Type, c.ID = s.Labels[0], stringProp(s.Props, "id")
		} else {
			c.Type, c.ID = s.Type, stringProp(s.Props, "id")
			c.From, c.To = s.FromLabel+":"+s.FromID, s.ToLabel+":"+s.ToID
		}
		out = append(out, c)
	}
	return out
}

func diffElement(kind, typ, id, from, to, hash string, props map[string]any, stored Stored) (Change, bool) {
	c := Change{Kind: kind, Type: typ, ID: id, From: from, To: to, AfterHash: hash, Events: eventsOf(props)}
	switch {
	case stored.Key == "":
		c.Op, c.Props = OpAdded, normalizeProps(props)
		return c, true
	case stored.Hash != hash:
		c.Op, c.BeforeHash, c.Changed = OpChanged, stored.Hash, propDiff(stored.Props, props)
		return c, true
	case stored.Tampered():
		c.Op, c.BeforeHash, c.Changed = OpRepaired, stored.Hash, propDiff(stored.Props, props)
		return c, true
	}
	return Change{}, false
}

func stringProp(p map[string]any, k string) string { s, _ := p[k].(string); return s }

func eventsOf(p map[string]any) []string {
	switch v := p["source_event_ids"].(type) {
	case []string:
		return v
	case []any:
		return anyStrings(v)
	}
	return []string{}
}

func normalizeProps(p map[string]any) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		if k != "h" {
			out[k] = normalizeValue(v)
		}
	}
	return out
}

func propDiff(before, after map[string]any) map[string]PropChange {
	b, a := normalizeProps(before), normalizeProps(after)
	out := map[string]PropChange{}
	for k, av := range a {
		if bv, ok := b[k]; !ok || !reflect.DeepEqual(bv, av) {
			out[k] = PropChange{Before: b[k], After: av}
		}
	}
	for k, bv := range b {
		if _, ok := a[k]; !ok {
			out[k] = PropChange{Before: bv}
		}
	}
	return out
}

// writeSet returns the snapshot elements a projection must write: the added, changed and repaired ones.
func writeSet(snap Snapshot, d Diff) (nodes []Node, edges []Edge) {
	touched := map[string]bool{}
	for _, c := range d.Changes {
		if c.Op == OpRemoved {
			continue
		}
		if c.Kind == "node" {
			touched[NodeKey(c.Type, c.ID)] = true
		} else {
			touched[EdgeKey(c.Type, c.ID)] = true
		}
	}
	for _, n := range snap.Nodes {
		if touched[n.Key()] {
			nodes = append(nodes, n)
		}
	}
	for _, e := range snap.Edges {
		if touched[e.Key()] {
			edges = append(edges, e)
		}
	}
	return nodes, edges
}

// removalKeys returns the node and edge keys the diff removes.
func removalKeys(d Diff) (nodeKeys, edgeKeys []string) {
	for _, c := range d.Changes {
		if c.Op != OpRemoved {
			continue
		}
		if c.Kind == "node" {
			nodeKeys = append(nodeKeys, NodeKey(c.Type, c.ID))
		} else {
			edgeKeys = append(edgeKeys, EdgeKey(c.Type, c.ID))
		}
	}
	return nodeKeys, edgeKeys
}

// MergeDiffs folds the diffs of several projections (oldest first) into the net change: an element added
// and then changed is added with its final properties; added then removed vanishes. Used to answer
// "what did event N change" when one event spanned several projections (ingest, then recompute).
func MergeDiffs(diffs ...Diff) Diff {
	type slot struct {
		first, last Change
	}
	slots := map[string]*slot{}
	var order []string
	for _, d := range diffs {
		for _, c := range d.Changes {
			k := c.Kind + "|" + c.Type + "|" + c.ID
			if s, ok := slots[k]; ok {
				s.last = c
			} else {
				slots[k] = &slot{first: c, last: c}
				order = append(order, k)
			}
		}
	}
	var out []Change
	for _, k := range order {
		s := slots[k]
		net, ok := netChange(s.first, s.last)
		if ok {
			out = append(out, net)
		}
	}
	return Diff{Changes: out}
}

func netChange(first, last Change) (Change, bool) {
	switch {
	case first.Op == OpAdded && last.Op == OpRemoved:
		return Change{}, false
	case first.Op == OpAdded:
		n := last
		n.Op, n.BeforeHash, n.Changed = OpAdded, "", nil
		if last.Op != OpAdded {
			n.Props = finalProps(first, last)
		}
		return n, true
	case last.Op == OpRemoved:
		n := last
		n.BeforeHash = first.BeforeHash
		return n, true
	}
	n := last
	n.BeforeHash = first.BeforeHash
	if first.BeforeHash == last.AfterHash && last.AfterHash != "" {
		return Change{}, false
	}
	n.Op = OpChanged
	n.Changed = mergePropChanges(first.Changed, last.Changed)
	return n, true
}

// finalProps applies a later change's property changes to an added element's properties.
func finalProps(added, later Change) map[string]any {
	out := map[string]any{}
	for k, v := range added.Props {
		out[k] = v
	}
	for k, pc := range later.Changed {
		if pc.After == nil {
			delete(out, k)
		} else {
			out[k] = pc.After
		}
	}
	return out
}

func mergePropChanges(a, b map[string]PropChange) map[string]PropChange {
	out := map[string]PropChange{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if prev, ok := out[k]; ok {
			v.Before = prev.Before
		}
		if reflect.DeepEqual(v.Before, v.After) {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

// MarshalChanges encodes a diff for the changes jsonb column.
func MarshalChanges(d Diff) ([]byte, error) {
	if d.Changes == nil {
		d.Changes = []Change{}
	}
	raw, err := json.Marshal(d.Changes)
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: encode diff: %w", err)
	}
	return raw, nil
}
