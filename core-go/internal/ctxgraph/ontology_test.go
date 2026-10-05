package ctxgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type ontologyFile struct {
	Version int `json:"version"`
	Common  struct {
		Required       []string `json:"required"`
		ScopedRequired []string `json:"scoped_required"`
	} `json:"common_node_properties"`
	CommonEdge struct {
		Required []string `json:"required"`
		Optional []string `json:"optional"`
	} `json:"common_edge_properties"`
	Labels map[string]struct {
		Table    string   `json:"table"`
		Extends  string   `json:"extends"`
		Global   bool     `json:"global"`
		Required []string `json:"required"`
	} `json:"labels"`
	Relationships map[string]struct {
		From     []string `json:"from"`
		To       []string `json:"to"`
		Required []string `json:"required"`
	} `json:"relationships"`
}

func contractPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, "contracts", "graph", "ontology.v1.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts/graph/ontology.v1.json not found")
		}
		dir = parent
	}
}

func sorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	if len(out) == 0 {
		return []string{}
	}
	return out
}

// The contract is the source of truth; the Go tables must say exactly what it says.
func TestOntologyMatchesContract(t *testing.T) {
	raw, err := os.ReadFile(contractPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var c ontologyFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sorted(c.Common.Required), sorted(commonNodeRequired)) {
		t.Errorf("common node properties: contract %v, code %v", c.Common.Required, commonNodeRequired)
	}
	if !reflect.DeepEqual(sorted(c.CommonEdge.Required), sorted(commonEdgeRequired)) {
		t.Errorf("common edge properties: contract %v, code %v", c.CommonEdge.Required, commonEdgeRequired)
	}
	if len(c.Labels) != len(Labels) {
		t.Errorf("labels: contract has %d, code %d", len(c.Labels), len(Labels))
	}
	for name, want := range c.Labels {
		got, ok := Labels[name]
		if !ok {
			t.Errorf("label %s is in the contract but not in the code", name)
			continue
		}
		if got.Table != want.Table || got.Extends != want.Extends || got.Global != want.Global ||
			!reflect.DeepEqual(sorted(got.Required), sorted(want.Required)) {
			t.Errorf("label %s: contract %+v, code %+v", name, want, got)
		}
	}
	if len(c.Relationships) != len(Relationships) {
		t.Errorf("relationships: contract has %d, code %d", len(c.Relationships), len(Relationships))
	}
	for name, want := range c.Relationships {
		got, ok := Relationships[name]
		if !ok {
			t.Errorf("relationship %s is in the contract but not in the code", name)
			continue
		}
		if !reflect.DeepEqual(sorted(got.From), sorted(want.From)) || !reflect.DeepEqual(sorted(got.To), sorted(want.To)) ||
			!reflect.DeepEqual(sorted(got.Required), sorted(want.Required)) {
			t.Errorf("relationship %s: contract %+v, code %+v", name, want, got)
		}
	}
}

// HAR-96 lists these node labels and edges; the ontology must cover them at least.
func TestOntologyCoversTheHAR96List(t *testing.T) {
	for _, l := range []string{"Account", "Person", "Opportunity", "Conversation", "Activity", "Claim", "Commitment", "Document", "Signal", "DecisionEpisode", "Knowledge"} {
		if _, ok := Labels[l]; !ok {
			t.Errorf("label %s missing", l)
		}
	}
	for _, r := range []string{"WORKS_AT", "BELONGS_TO", "CHAMPION_FOR", "ECONOMIC_BUYER_FOR", "TECHNICAL_EVALUATOR_FOR", "INFLUENCES", "PARTICIPATED_IN",
		"INVOLVES", "ABOUT", "SUPPORTED_BY", "ABOUT_ACCOUNT", "ABOUT_PERSON", "ABOUT_OPPORTUNITY", "MADE_BY", "MADE_IN", "DERIVED_FROM",
		"TRIGGERED_BY", "USED_KNOWLEDGE", "APPLIES_TO"} {
		if _, ok := Relationships[r]; !ok {
			t.Errorf("relationship %s missing", r)
		}
	}
}

func TestValidationRejectsWhatTheOntologyForbids(t *testing.T) {
	good := newNode([]string{LabelAccount}, "a1", "a1", nodeProps("accounts", []string{}, []string{}, tNow(), tNow(), map[string]any{"name": "x", "status": "active"}))
	if err := ValidateNode(good); err != nil {
		t.Fatalf("good node rejected: %v", err)
	}
	bad := []struct {
		name string
		n    Node
	}{
		{"unknown label", newNode([]string{"Widget"}, "w", "a1", map[string]any{})},
		{"missing required property", newNode([]string{LabelAccount}, "a1", "a1", nodeProps("accounts", []string{}, []string{}, tNow(), tNow(), map[string]any{"name": "x"}))},
		{"global label with a scope", newNode([]string{LabelKnowledge}, "k", "a1", nodeProps("knowledge", []string{}, []string{}, tNow(), tNow(), map[string]any{"key": "K1", "title": "t", "status": "candidate"}))},
		{"conversation without the activity label", newNode([]string{LabelConversation}, "c", "a1", nodeProps("activities", []string{}, []string{}, tNow(), tNow(),
			map[string]any{"activity_type": "x", "source_system": "x", "occurred_at": "x", "visibility": "org"}))},
	}
	for _, tc := range bad {
		if err := ValidateNode(tc.n); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	props := edgeProps(tNow(), nil, "open", []string{}, []string{}, tNow(), tNow(), map[string]any{"confidence": 1.0, "standing": "crm_explicit"})
	if err := ValidateEdge(newEdge(RelWorksAt, "e", LabelPerson, "p", LabelAccount, "a", "a", props)); err != nil {
		t.Errorf("good edge rejected: %v", err)
	}
	for name, e := range map[string]Edge{
		"unknown type":   newEdge("LOVES", "e", LabelPerson, "p", LabelAccount, "a", "a", props),
		"wrong endpoint": newEdge(RelWorksAt, "e", LabelAccount, "a", LabelPerson, "p", "a", props),
		"missing required property": newEdge(RelWorksAt, "e", LabelPerson, "p", LabelAccount, "a", "a",
			edgeProps(tNow(), nil, "open", []string{}, []string{}, tNow(), tNow(), nil)),
	} {
		if err := ValidateEdge(e); err == nil {
			t.Errorf("edge %s: accepted", name)
		}
	}
}

// Everything the projector wrote to Neo4j must be inside the ontology: labels, relationship types,
// endpoint labels and required properties.
func TestProjectorOnlyWritesWhatTheOntologyAllows(t *testing.T) {
	s := seedWorld(t)
	g := newGraph(t)
	projectAll(t, s, newProjector(t, g))

	labels, err := g.read(bg, "CALL db.labels() YIELD label RETURN label", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range labels {
		if l := r["label"].(string); Labels[l].Table == "" {
			t.Errorf("neo4j holds label %q, which is not in the ontology", l)
		}
	}
	types, err := g.read(bg, "CALL db.relationshipTypes() YIELD relationshipType AS t RETURN t", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range types {
		if rt := r["t"].(string); Relationships[rt].From == nil {
			t.Errorf("neo4j holds relationship type %q, which is not in the ontology", rt)
		}
	}
	nodes, edges, err := g.storedAll(bg)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := requireProps(n.Props, commonNodeRequired, Labels[n.Labels[0]].Required); err != nil {
			t.Errorf("%s: %v", n.Key, err)
		}
	}
	for _, e := range edges {
		spec := Relationships[e.Type]
		if !labelConforms(e.FromLabel, spec.From) || !labelConforms(e.ToLabel, spec.To) {
			t.Errorf("%s: %s -> %s violates the ontology", e.Key, e.FromLabel, e.ToLabel)
		}
		if err := requireProps(e.Props, commonEdgeRequired, spec.Required); err != nil {
			t.Errorf("%s: %v", e.Key, err)
		}
	}
}
