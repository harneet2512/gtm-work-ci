package graph_test

import (
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
)

func person(id string) graph.EntityRef { return graph.EntityRef{Type: "person", ID: id} }
func opp(id string) graph.EntityRef    { return graph.EntityRef{Type: "opportunity", ID: id} }

type edgeFixture struct {
	acct, opp, priya, tom, dana string
}

func newEdgeFixture(t *testing.T) edgeFixture {
	t.Helper()
	resetDB(t)
	f := edgeFixture{}
	f.acct = newAccount(t, "Acme", "acme.com")
	f.opp = newOpp(t, f.acct, "EU rollout")
	f.priya = newPerson(t, "contact", "Priya Shah", "priya@acme.com")
	f.tom = newPerson(t, "contact", "Tom Becker", "tom@acme.com")
	f.dana = newPerson(t, "employee", "Dana Kim", "dana@vendor.example")
	return f
}

func spec(f edgeFixture, src, rel, standing string) graph.EdgeSpec {
	return graph.EdgeSpec{Src: person(src), Rel: rel, Dst: opp(f.opp), Standing: standing, Confidence: 0.9, ValidFrom: t0}
}

func openCount(t *testing.T, rel string) int {
	t.Helper()
	return num(t, `SELECT count(*) FROM relationships WHERE rel_type = $1 AND valid_to IS NULL`, rel)
}

func TestUpsertEdgeCreatesOnceAndIsIdempotent(t *testing.T) {
	f := newEdgeFixture(t)
	act := newActivity(t)
	s := spec(f, f.dana, "owns", "crm_explicit")
	s.SourceActivityID = act

	created, err := upsert(t, s)
	if err != nil || !created {
		t.Fatalf("first upsert created=%v err=%v", created, err)
	}
	created, err = upsert(t, s)
	if err != nil || created {
		t.Fatalf("second upsert created=%v err=%v, want a no-op", created, err)
	}
	if got := num(t, `SELECT count(*) FROM relationships`); got != 1 {
		t.Fatalf("%d edges, want 1", got)
	}
	if got := str(t, `SELECT source_activity_id::text FROM relationships`); got != act {
		t.Errorf("source_activity_id = %q, want %q", got, act)
	}
	if got := str(t, `SELECT standing || '/' || confidence::text FROM relationships`); got != "crm_explicit/0.900" {
		t.Errorf("standing/confidence = %s", got)
	}
}

func TestUpsertEdgeHigherStandingClosesTheLowerOne(t *testing.T) {
	f := newEdgeFixture(t)
	if _, err := upsert(t, spec(f, f.priya, "influences", "first_party_ai")); err != nil {
		t.Fatal(err)
	}
	up := spec(f, f.priya, "influences", "crm_explicit")
	up.ValidFrom = t0.Add(time.Hour)

	created, err := upsert(t, up)
	if err != nil || !created {
		t.Fatalf("upgrade created=%v err=%v", created, err)
	}

	if got := str(t, `SELECT standing FROM relationships WHERE valid_to IS NULL`); got != "crm_explicit" {
		t.Errorf("open edge standing = %s", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE standing = 'first_party_ai' AND valid_to IS NOT NULL`); got != 1 {
		t.Errorf("the first_party_ai edge must be closed, not deleted (closed=%d)", got)
	}
}

// A lower-standing edge is never silently dropped because a higher one exists for the same
// (src, type, dst): both stay open and the standing decides which counts.
func TestUpsertEdgeLowerStandingCoexistsWithAHigherOne(t *testing.T) {
	f := newEdgeFixture(t)
	if _, err := upsert(t, spec(f, f.priya, "influences", "crm_explicit")); err != nil {
		t.Fatal(err)
	}

	created, err := upsert(t, spec(f, f.priya, "influences", "first_party_ai"))

	if err != nil || !created {
		t.Fatalf("lower standing created=%v err=%v, want it written next to the higher one", created, err)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE valid_to IS NULL`); got != 2 {
		t.Errorf("%d open edges, want both standings open", got)
	}
	// The same standing twice is still idempotent.
	if created, _ := upsert(t, spec(f, f.priya, "influences", "first_party_ai")); created {
		t.Error("an identical edge was written twice")
	}
}

func TestUpsertEdgeStandingOrderIncludesFirstPartyRecord(t *testing.T) {
	f := newEdgeFixture(t)
	for _, st := range []string{"third_party", "first_party_ai", "first_party_record", "crm_explicit", "human_approved"} {
		if _, err := upsert(t, spec(f, f.priya, "influences", st)); err != nil {
			t.Fatalf("standing %s: %v", st, err)
		}
	}
	// Each stronger edge closed the weaker ones below it.
	if got := str(t, `SELECT string_agg(standing, ',' ORDER BY standing) FROM relationships WHERE valid_to IS NULL`); got != "human_approved" {
		t.Errorf("open standings after the climb = %q, want only human_approved", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE valid_to IS NOT NULL`); got != 4 {
		t.Errorf("closed edges = %d, want 4 (history kept)", got)
	}
}

func TestUpsertEdgeRuleEdgeDoesNotBlockALaterAIEdge(t *testing.T) {
	f := newEdgeFixture(t)
	if _, err := upsert(t, spec(f, f.priya, "influences", "first_party_record")); err != nil {
		t.Fatal(err)
	}
	created, err := upsert(t, spec(f, f.priya, "influences", "first_party_ai"))
	if err != nil || !created {
		t.Fatalf("AI edge created=%v err=%v: a rule edge must not silently drop it", created, err)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE valid_to IS NULL`); got != 2 {
		t.Errorf("%d open edges, want the rule edge and the AI edge", got)
	}
}

func TestConcurrentExclusiveUpsertsLeaveOneHolder(t *testing.T) {
	f := newEdgeFixture(t)
	holders := []string{f.priya, f.tom, f.dana}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := spec(f, holders[i%3], "champion_for", "first_party_ai")
			s.Exclusive = true
			s.ValidFrom = t0.Add(time.Duration(i) * time.Minute)
			_, err := upsert(t, s)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent exclusive upsert: %v", err)
		}
	}
	if got := openCount(t, "champion_for"); got != 1 {
		t.Errorf("%d open champion_for edges, want exactly one holder", got)
	}
}

func TestUpsertEdgeExclusiveClosesOtherSourcesOfNoHigherStanding(t *testing.T) {
	f := newEdgeFixture(t)
	if _, err := upsert(t, spec(f, f.priya, "champion_for", "first_party_ai")); err != nil {
		t.Fatal(err)
	}
	next := spec(f, f.tom, "champion_for", "first_party_ai")
	next.Exclusive = true
	next.ValidFrom = t0.Add(time.Hour)

	if _, err := upsert(t, next); err != nil {
		t.Fatal(err)
	}

	if got := str(t, `SELECT src_id::text FROM relationships WHERE rel_type = 'champion_for' AND valid_to IS NULL`); got != f.tom {
		t.Errorf("open champion = %s, want Tom", got)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE src_id = $1::uuid AND valid_to IS NOT NULL`, f.priya); got != 1 {
		t.Errorf("Priya's edge must be closed with history kept (closed=%d)", got)
	}

	// A higher-standing champion is never closed by a lower-standing exclusive upsert.
	crm := spec(f, f.priya, "champion_for", "crm_explicit")
	if _, err := upsert(t, crm); err != nil {
		t.Fatal(err)
	}
	ai := spec(f, f.tom, "champion_for", "first_party_ai")
	ai.Exclusive = true
	ai.ValidFrom = t0.Add(2 * time.Hour)
	if _, err := upsert(t, ai); err != nil {
		t.Fatal(err)
	}
	if got := num(t, `SELECT count(*) FROM relationships WHERE standing = 'crm_explicit' AND valid_to IS NULL`); got != 1 {
		t.Error("an exclusive first_party_ai edge closed a crm_explicit edge")
	}
}

func TestUpsertEdgeClosingNeverProducesAnEmptyInterval(t *testing.T) {
	f := newEdgeFixture(t)
	if _, err := upsert(t, spec(f, f.priya, "influences", "first_party_ai")); err != nil {
		t.Fatal(err)
	}
	earlier := spec(f, f.priya, "influences", "crm_explicit")
	earlier.ValidFrom = t0.Add(-24 * time.Hour) // out-of-order evidence

	if _, err := upsert(t, earlier); err != nil {
		t.Fatalf("out-of-order upgrade failed: %v", err)
	}
}

func TestUpsertEdgeValidation(t *testing.T) {
	f := newEdgeFixture(t)
	bad := map[string]func(*graph.EdgeSpec){
		"unknown standing":    func(s *graph.EdgeSpec) { s.Standing = "rumour" },
		"confidence above 1":  func(s *graph.EdgeSpec) { s.Confidence = 1.5 },
		"negative confidence": func(s *graph.EdgeSpec) { s.Confidence = -0.1 },
		"self edge":           func(s *graph.EdgeSpec) { s.Dst = s.Src },
		"no valid_from":       func(s *graph.EdgeSpec) { s.ValidFrom = time.Time{} },
		"no rel type":         func(s *graph.EdgeSpec) { s.Rel = "" },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			s := spec(f, f.priya, "influences", "crm_explicit")
			mutate(&s)
			if _, err := upsert(t, s); err == nil {
				t.Error("invalid edge accepted")
			}
		})
	}
}

func TestCloseEdgesByFilter(t *testing.T) {
	f := newEdgeFixture(t)
	for _, s := range []graph.EdgeSpec{
		spec(f, f.priya, "influences", "crm_explicit"),
		spec(f, f.priya, "economic_buyer_for", "crm_explicit"),
		spec(f, f.priya, "influences", "first_party_ai"), // coexists with the crm_explicit edge
		spec(f, f.tom, "influences", "crm_explicit"),
	} {
		if _, err := upsert(t, s); err != nil {
			t.Fatal(err)
		}
	}
	dst := opp(f.opp)
	src := person(f.priya)

	n, err := graph.CloseEdges(ctx, env.DB, graph.EdgeFilter{
		Src: &src, Dst: &dst, Rels: []string{"influences", "economic_buyer_for"}, Standings: []string{"crm_explicit"},
	}, t0.Add(time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("closed %d edges (err=%v), want 2", n, err)
	}
	if got := openCount(t, "influences"); got != 2 {
		t.Errorf("open influences = %d, want Tom's CRM edge and Priya's surviving AI edge", got)
	}

	other := person(f.tom)
	n, err = graph.CloseEdges(ctx, env.DB, graph.EdgeFilter{Src: &src, NotDst: &other, Rels: []string{"works_at"}}, t0)
	if err != nil || n != 0 {
		t.Errorf("NotDst filter closed %d (err=%v), want 0", n, err)
	}
}
