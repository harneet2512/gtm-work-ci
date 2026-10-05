package graph_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

func TestInsertAndCurrentMapping(t *testing.T) {
	resetDB(t)
	p := newPerson(t, "contact", "Priya Shah", "priya.shah@acme.com")
	act := newActivity(t)

	got, err := insertMapping(t, graph.Mapping{
		EntityType: "person", EntityID: p, SourceKey: graph.SourceKey{System: "email", Key: "priya.shah@acme.com"},
		Confidence: 0.9, Method: graph.MethodRule, EvidenceActivityID: act, ValidFrom: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	cur, ok, err := graph.CurrentMapping(ctx, env.DB, graph.SourceKey{System: "email", Key: "priya.shah@acme.com"})
	if err != nil || !ok {
		t.Fatalf("CurrentMapping = %v, %v", ok, err)
	}
	if cur.ID != got.ID || cur.EntityID != p || cur.Method != "rule" || cur.Confidence != 0.9 || cur.EvidenceActivityID != act || cur.ValidTo != nil {
		t.Errorf("unexpected mapping %+v", cur)
	}
	if _, ok, _ := graph.CurrentMapping(ctx, env.DB, graph.SourceKey{System: "email", Key: "nobody@acme.com"}); ok {
		t.Error("an unmapped identity must report ok=false")
	}
}

func TestEnsureMappingIsIdempotentAndReportsConflicts(t *testing.T) {
	resetDB(t)
	a, b := newPerson(t, "contact", "A", "a@x.com"), newPerson(t, "contact", "B", "b@x.com")
	m := graph.Mapping{EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "crm", Key: "contact:1"}, Confidence: 1, Method: graph.MethodExact, ValidFrom: t0}

	first, created, err := ensureMapping(t, m)
	if err != nil || !created {
		t.Fatalf("first EnsureMapping created=%v err=%v", created, err)
	}
	again, created, err := ensureMapping(t, m)
	if err != nil || created || again.ID != first.ID {
		t.Errorf("second EnsureMapping created=%v id=%s err=%v, want the existing row", created, again.ID, err)
	}
	m.EntityID = b
	other, created, err := ensureMapping(t, m)
	if err != nil || created || other.EntityID != a {
		t.Errorf("a conflicting claim must return the existing owner untouched, got %+v created=%v err=%v", other, created, err)
	}
	if n := num(t, `SELECT count(*) FROM entity_source_mappings`); n != 1 {
		t.Errorf("%d mappings, want 1", n)
	}
}

func TestRemapClosesTheOldMappingAndKeepsMethodConfidenceAndEvidence(t *testing.T) {
	resetDB(t)
	old, neu := newPerson(t, "contact", "Old", "old@x.com"), newPerson(t, "contact", "New", "new@x.com")
	act := newActivity(t)
	if _, err := insertMapping(t, graph.Mapping{
		EntityType: "person", EntityID: old, SourceKey: graph.SourceKey{System: "call", Key: "C1:speaker_02"},
		Confidence: 0.8, Method: graph.MethodRule, EvidenceActivityID: act, ValidFrom: t0,
	}); err != nil {
		t.Fatal(err)
	}

	moved, err := remap(t, graph.SourceKey{System: "call", Key: "C1:speaker_02"}, neu, t0.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}

	if moved.EntityID != neu || moved.Method != "rule" || moved.Confidence != 0.8 || moved.EvidenceActivityID != act {
		t.Errorf("remapped mapping = %+v, want method/confidence/evidence carried over", moved)
	}
	if n := num(t, `SELECT count(*) FROM entity_source_mappings WHERE source_system='call' AND source_key='C1:speaker_02'`); n != 2 {
		t.Errorf("history has %d rows, want old (closed) + new", n)
	}
	closedTo := str(t, `SELECT to_char(valid_to AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS') FROM entity_source_mappings WHERE entity_id = $1::uuid`, old)
	if closedTo != "2026-08-18T15:00:00" {
		t.Errorf("old mapping closed at %s, want the remap time", closedTo)
	}
	if cur, _, _ := graph.CurrentMapping(ctx, env.DB, graph.SourceKey{System: "call", Key: "C1:speaker_02"}); cur.EntityID != neu {
		t.Errorf("current mapping points at %s, want the new person", cur.EntityID)
	}
}

func TestRemapOverridesAndNoOps(t *testing.T) {
	resetDB(t)
	a, b := newPerson(t, "contact", "A", "a@x.com"), newPerson(t, "contact", "B", "b@x.com")
	if _, err := insertMapping(t, graph.Mapping{
		EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "email", Key: "k@x.com"}, Confidence: 0.9, Method: graph.MethodRule, ValidFrom: t0,
	}); err != nil {
		t.Fatal(err)
	}

	same, err := remap(t, graph.SourceKey{System: "email", Key: "k@x.com"}, a, t0.Add(time.Hour), nil)
	if err != nil || num(t, `SELECT count(*) FROM entity_source_mappings`) != 1 {
		t.Fatalf("remapping onto the same entity must be a no-op (err=%v)", err)
	}
	if same.EntityID != a {
		t.Errorf("no-op remap returned %+v", same)
	}

	// A remap at a time not after the old valid_from must still produce a valid interval.
	human := graph.MethodHuman
	conf := 1.0
	moved, err := remap(t, graph.SourceKey{System: "email", Key: "k@x.com"}, b, t0.Add(-time.Hour), &graph.MappingOverride{Method: human, Confidence: &conf})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Method != "human" || moved.Confidence != 1 {
		t.Errorf("override ignored: %+v", moved)
	}

	if _, err := remap(t, graph.SourceKey{System: "email", Key: "unknown@x.com"}, b, t0, nil); err == nil {
		t.Error("remapping an identity without a current mapping must fail")
	}
}

func TestInsertMappingRejectsASecondCurrentMapping(t *testing.T) {
	resetDB(t)
	a, b := newPerson(t, "contact", "A", "a@x.com"), newPerson(t, "contact", "B", "b@x.com")
	m := graph.Mapping{EntityType: "person", EntityID: a, SourceKey: graph.SourceKey{System: "crm", Key: "contact:1"}, Confidence: 1, Method: graph.MethodExact, ValidFrom: t0}
	if _, err := insertMapping(t, m); err != nil {
		t.Fatal(err)
	}
	m.EntityID = b
	if _, err := insertMapping(t, m); err == nil {
		t.Error("two current mappings for one identity must violate the unique index")
	}
}
