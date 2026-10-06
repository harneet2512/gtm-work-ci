package graph_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

type readFixture struct {
	edgeFixture
	otherAcct, otherOpp, other string
	doc                        graph.EntityRef
	activity                   string
}

func newReadFixture(t *testing.T) readFixture {
	t.Helper()
	f := readFixture{edgeFixture: newEdgeFixture(t)}
	f.otherAcct = newAccount(t, "Beta", "beta.io")
	f.otherOpp = newOpp(t, f.otherAcct, "Beta rollout")
	f.other = newPerson(t, "contact", "Hannah Lee", "hannah@beta.io")
	f.activity = newActivity(t)
	if _, err := env.DB.Exec(`UPDATE activities SET account_id = $1::uuid, summary = 'Document shared: Proposal', source_object_id = 'gdrive:proposal' WHERE id = $2::uuid`,
		f.acct, f.activity); err != nil {
		t.Fatal(err)
	}
	f.doc = graph.DocumentRef("gdrive:proposal")
	if _, err := env.DB.Exec(`UPDATE people SET account_id = $1::uuid WHERE id IN ($2::uuid, $3::uuid)`, f.acct, f.priya, f.tom); err != nil {
		t.Fatal(err)
	}
	for _, s := range []graph.EdgeSpec{
		{Src: graph.EntityRef{Type: "opportunity", ID: f.opp}, Rel: "belongs_to", Dst: graph.EntityRef{Type: "account", ID: f.acct}, Standing: "crm_explicit", Confidence: 1, ValidFrom: t0},
		{Src: person(f.dana), Rel: "owns", Dst: opp(f.opp), Standing: "crm_explicit", Confidence: 1, ValidFrom: t0},
		{Src: person(f.priya), Rel: "works_at", Dst: graph.EntityRef{Type: "account", ID: f.acct}, Standing: "crm_explicit", Confidence: 1, ValidFrom: t0},
		{Src: f.doc, Rel: "shared_with", Dst: person(f.priya), Standing: "first_party_record", Confidence: 0.9, ValidFrom: t0, SourceActivityID: f.activity},
		{Src: person(f.priya), Rel: "participated_in", Dst: graph.EntityRef{Type: "activity", ID: f.activity}, Standing: "first_party_ai", Confidence: 1, ValidFrom: t0},
		{Src: person(f.tom), Rel: "works_at", Dst: graph.EntityRef{Type: "account", ID: f.acct}, Standing: "first_party_ai", Confidence: 0.9, ValidFrom: t0},
		{Src: person(f.other), Rel: "works_at", Dst: graph.EntityRef{Type: "account", ID: f.otherAcct}, Standing: "crm_explicit", Confidence: 1, ValidFrom: t0},
		{Src: person(f.other), Rel: "influences", Dst: opp(f.otherOpp), Standing: "crm_explicit", Confidence: 1, ValidFrom: t0},
	} {
		if _, err := upsert(t, s); err != nil {
			t.Fatal(err)
		}
	}
	// Tom's works_at edge is closed: history, not part of the open graph.
	tomRef := person(f.tom)
	if _, err := graph.CloseEdges(ctx, env.DB, graph.EdgeFilter{Src: &tomRef, Rels: []string{"works_at"}}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestOpenEdgesReturnsOnlyOpenEdgesAroundTheAccount(t *testing.T) {
	f := newReadFixture(t)

	edges, err := graph.OpenEdges(ctx, env.DB, f.acct)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]int{}
	for _, e := range edges {
		got[e.Rel]++
		if e.ValidTo != nil {
			t.Errorf("closed edge %s returned", e.Rel)
		}
	}
	want := map[string]int{"belongs_to": 1, "owns": 1, "works_at": 1, "shared_with": 1, "participated_in": 1}
	for rel, n := range want {
		if got[rel] != n {
			t.Errorf("%s edges = %d, want %d (all: %v)", rel, got[rel], n, got)
		}
	}
	if got["influences"] != 0 {
		t.Error("another account's edge leaked into the result")
	}
	if len(edges) != 5 {
		t.Errorf("%d edges, want 5", len(edges))
	}
}

func TestPersonIdentitiesListsCurrentFirstThenHistory(t *testing.T) {
	resetDB(t)
	a, b := newPerson(t, "contact", "A", "a@x.com"), newPerson(t, "contact", "B", "b@x.com")
	for _, m := range []graph.Mapping{
		{EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "email", Key: "a@x.com"}, Confidence: 1, Method: "exact", ValidFrom: t0},
		{EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "crm", Key: "contact:1"}, Confidence: 1, Method: "exact", ValidFrom: t0},
		{EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "call", Key: "C:speaker_02"}, Confidence: 0.8, Method: "rule", ValidFrom: t0},
	} {
		if _, err := insertMapping(t, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := remap(t, graph.SourceKey{System: "call", Key: "C:speaker_02"}, b, t0.Add(time.Hour), nil); err != nil {
		t.Fatal(err)
	}

	got, err := graph.PersonIdentities(ctx, env.DB, a)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("%d identities, want 3 (2 current + 1 closed)", len(got))
	}
	if got[0].ValidTo != nil || got[1].ValidTo != nil || got[2].ValidTo == nil {
		t.Errorf("current identities must come first: %+v", got)
	}
	if got[0].System != "call" && got[0].System != "crm" && got[0].System != "email" {
		t.Errorf("unexpected system %q", got[0].System)
	}
	if empty, err := graph.PersonIdentities(ctx, env.DB, newPerson(t, "contact", "C", "c@x.com")); err != nil || len(empty) != 0 {
		t.Errorf("a person without identities returned %v, %v", empty, err)
	}
}

func TestGraphMatchesTheCoreYamlShape(t *testing.T) {
	f := newReadFixture(t)

	g, err := graph.Graph(ctx, env.DB, f.acct)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Nodes []struct {
			ID, Type, Label string
			Data            map[string]any
		} `json:"nodes"`
		Edges []struct {
			ID               string  `json:"id"`
			Source           string  `json:"source"`
			Target           string  `json:"target"`
			RelType          string  `json:"rel_type"`
			Standing         string  `json:"standing"`
			Confidence       float64 `json:"confidence"`
			SourceActivityID *string `json:"source_activity_id"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	byID := map[string]struct{ Type, Label string }{}
	for _, n := range decoded.Nodes {
		if n.ID == "" || n.Type == "" || n.Label == "" {
			t.Errorf("node %+v is missing id/type/label", n)
		}
		byID[n.ID] = struct{ Type, Label string }{n.Type, n.Label}
	}
	if byID[f.acct].Type != "account" || byID[f.acct].Label != "Acme" {
		t.Errorf("account node = %+v", byID[f.acct])
	}
	if byID[f.opp].Type != "opportunity" || byID[f.priya].Label != "Priya Shah" {
		t.Errorf("opportunity/person nodes wrong: %+v / %+v", byID[f.opp], byID[f.priya])
	}
	if d := byID[f.doc.ID]; d.Type != "document" || d.Label != "gdrive:proposal" {
		t.Errorf("document node = %+v, want label from the source document id", d)
	}
	if a := byID[f.activity]; a.Type != "activity" || a.Label != "Document shared: Proposal" {
		t.Errorf("activity node = %+v", a)
	}
	for _, e := range decoded.Edges {
		if _, ok := byID[e.Source]; !ok {
			t.Errorf("edge %s source %s is not a node", e.RelType, e.Source)
		}
		if _, ok := byID[e.Target]; !ok {
			t.Errorf("edge %s target %s is not a node", e.RelType, e.Target)
		}
		if e.ID == "" || e.Standing == "" || e.Confidence == 0 {
			t.Errorf("incomplete edge %+v", e)
		}
	}
	if len(decoded.Edges) != 5 {
		t.Errorf("%d edges, want 5", len(decoded.Edges))
	}
}
