package worldtest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

func graphService(t *testing.T) *ctxgraph.Service {
	t.Helper()
	return ctxgraph.NewService(ctxgraph.NewReader(nil, env.DB, nil), env.DB) // no Neo4j: world reads need none
}

func viewAt(t *testing.T, w *worldfixture.World, k int, closed bool) (ctxgraph.View, string) {
	t.Helper()
	v, err := graphService(t).NeighborhoodAsOf(context.Background(), w.Account, ctxgraph.Params{SectionLimit: ctxgraph.MaxSectionLimit, IncludeClosed: closed}, w.At(k))
	if err != nil {
		t.Fatalf("graph at E%d: %v", k, err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return v, string(raw)
}

func nodeOf(v ctxgraph.View, id string) (ctxgraph.ViewNode, bool) {
	for _, n := range v.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return ctxgraph.ViewNode{}, false
}

func edgeOf(v ctxgraph.View, id string) (ctxgraph.ViewEdge, bool) {
	for _, e := range v.Edges {
		if e.ID == id {
			return e, true
		}
	}
	return ctxgraph.ViewEdge{}, false
}

// claimIDs lists the claims whose source activity is one of the events from..to.
func claimIDs(t *testing.T, from, to int, w *worldfixture.World) []string {
	t.Helper()
	rows, err := env.DB.Query(`SELECT id::text FROM claims WHERE source_activity_id = ANY($1::uuid[])`, "{"+strings.Join(ids(w, from, to), ",")+"}")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func stageClaim(t *testing.T, value string) string {
	t.Helper()
	var id string
	if err := env.DB.QueryRow(`SELECT id::text FROM claims WHERE field_path = 'stage' AND value = to_jsonb($1::text)`, value).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The core acceptance for the graph: at T = occurred_at(Ek) nothing derived from Ek..E5 is in the view:
// no activity, claim, signal, person or edge of theirs, no evidence ref and no source event id.
func TestGraphAtWorldTimeNeverShowsTheEventOrAnythingAfter(t *testing.T) {
	w := seed(t)
	for k := 1; k <= 5; k++ {
		for _, closed := range []bool{false, true} {
			v, body := viewAt(t, w, k, closed)
			what := fmt.Sprintf("graph at E%d (include_closed=%v)", k, closed)
			assertNoneOf(t, what, body, ids(w, k, 5)...)
			assertNoneOf(t, what, body, sourceEvents(w, k, 5)...)
			assertNoneOf(t, what, body, claimIDs(t, k, 5, w)...)
			if k <= 4 {
				assertNoneOf(t, what, body, w.SignalID)
			}
			if k <= 3 {
				assertNoneOf(t, what, body, w.Marco) // first world-timed evidence of Marco is E3
			}
			for _, n := range ids(w, 1, k-1) {
				if _, ok := nodeOf(v, n); !ok && k <= 5 {
					t.Errorf("%s: activity %s before T is missing", what, n)
				}
			}
			if v.Projection.Complete != true || v.Projection.ProjectedAt != nil {
				t.Errorf("%s: a world read does not depend on the projection: %+v", what, v.Projection)
			}
		}
	}
}

// A changed property: at E3 the stage is still the E1 claim, standing and unsuperseded; the E3 claim that
// replaced it does not exist yet. After E3 the old claim is superseded with valid_to = E3.
func TestGraphAtWorldTimeUndoesASupersession(t *testing.T) {
	w := seed(t)
	old, negotiation := stageClaim(t, worldfixture.StageDiscovery), stageClaim(t, worldfixture.StageNegotiation)

	v, _ := viewAt(t, w, 3, false)
	n, ok := nodeOf(v, old)
	if !ok || n.Status != "active" || n.ValidTo != "" {
		t.Fatalf("at E3 the E1 stage claim stands: %+v ok=%v", n, ok)
	}
	if _, ok := nodeOf(v, negotiation); ok {
		t.Fatal("the claim that supersedes it, made at E3, must not exist at E3")
	}

	after, _ := viewAt(t, w, 4, true)
	n, ok = nodeOf(after, old)
	if !ok || n.Status != "superseded" || n.ValidTo != ctxgraphStamp(w.At(3)) {
		t.Fatalf("after E3 the claim is superseded at E3: %+v ok=%v", n, ok)
	}
	if n, ok := nodeOf(after, negotiation); !ok || n.Status != "active" {
		t.Fatalf("after E3 the replacing claim stands: %+v ok=%v", n, ok)
	}
	current, _ := viewAt(t, w, 4, false)
	if _, ok := nodeOf(current, old); ok {
		t.Fatal("without include_closed a superseded claim is not shown")
	}
}

// A removed edge: Priya's champion_for edge is open (present) at E2 and at E3 (it closes at E3, which is
// not strictly before E3), and closed with valid_to = E3 afterwards.
func TestGraphAtWorldTimeKeepsAnEdgeThatIsRemovedLater(t *testing.T) {
	w := seed(t)
	edge := "rel:" + w.EdgeChamp
	for _, k := range []int{2, 3} {
		v, _ := viewAt(t, w, k, false)
		e, ok := edgeOf(v, edge)
		if !ok || e.Status != "open" || e.ValidTo != "" {
			t.Fatalf("at E%d the champion edge is open: %+v ok=%v", k, e, ok)
		}
	}
	closed, _ := viewAt(t, w, 4, true)
	e, ok := edgeOf(closed, edge)
	if !ok || e.Status != "closed" || e.ValidTo != ctxgraphStamp(w.At(3)) {
		t.Fatalf("after E3 the edge is closed at E3: %+v ok=%v", e, ok)
	}
	open, _ := viewAt(t, w, 4, false)
	if _, ok := edgeOf(open, edge); ok {
		t.Fatal("a closed edge is hidden without include_closed")
	}
	first, _ := viewAt(t, w, 1, true)
	if _, ok := edgeOf(first, edge); ok {
		t.Fatal("the edge opens at E1, which is not before E1")
	}
}

// Evidence lookup: every evidence ref and source event id in the view names an activity before T.
func TestGraphEvidenceRefsOnlyNameActivitiesBeforeT(t *testing.T) {
	w := seed(t)
	for k := 2; k <= 5; k++ {
		v, _ := viewAt(t, w, k, true)
		before := map[string]bool{}
		for _, id := range ids(w, 1, k-1) {
			before[id] = true
		}
		beforeEvents := map[string]bool{}
		for _, id := range sourceEvents(w, 1, k-1) {
			beforeEvents[id] = true
		}
		refs := 0
		for _, n := range v.Nodes {
			for _, r := range n.EvidenceRefs {
				refs++
				if !before[r.ActivityID] {
					t.Errorf("E%d: node %s cites activity %s, which is not before T", k, n.ID, r.ActivityID)
				}
			}
			for _, e := range n.SourceEventIDs {
				if !beforeEvents[e] {
					t.Errorf("E%d: node %s cites source event %s, which is not before T", k, n.ID, e)
				}
			}
		}
		for _, e := range v.Edges {
			for _, r := range e.EvidenceRefs {
				refs++
				if !before[r.ActivityID] {
					t.Errorf("E%d: edge %s cites activity %s, which is not before T", k, e.ID, r.ActivityID)
				}
			}
		}
		if refs == 0 {
			t.Errorf("E%d: the view carries no evidence refs at all, so this check proves nothing", k)
		}
	}
}

// The person nothing world-timed touched before E3 appears only after it; the signal only after E4.
func TestGraphEntitiesAndSignalsAppearOnlyAfterTheirFirstEvidence(t *testing.T) {
	w := seed(t)
	cases := []struct {
		name string
		id   string
		from int // the first T = occurred_at(E<from>) at which it is visible
	}{{"Priya (first evidence E1)", w.Priya, 2}, {"Marco (first evidence E3)", w.Marco, 4}, {"signal (emitted at E4)", w.SignalID, 5}}
	for _, c := range cases {
		for k := 1; k <= 5; k++ {
			v, _ := viewAt(t, w, k, true)
			if _, ok := nodeOf(v, c.id); ok != (k >= c.from) {
				t.Errorf("%s at T=E%d: visible=%v, want %v", c.name, k, ok, k >= c.from)
			}
		}
	}
}

// Deleting and rebuilding the projection cannot change a world read: it reads no projection state.
func TestGraphWorldReadIsIndependentOfTheProjection(t *testing.T) {
	w := seed(t)
	_, before := viewAt(t, w, 4, true)
	if _, err := env.DB.Exec(`DELETE FROM graph_projection_checkpoints`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`DELETE FROM graph_projection_jobs`); err != nil {
		t.Fatal(err)
	}
	_, after := viewAt(t, w, 4, true)
	if before != after {
		t.Fatalf("the world read changed when the projection bookkeeping was deleted:\n%.600s\n%.600s", before, after)
	}
}

func TestGraphWorldReadErrors(t *testing.T) {
	w := seed(t)
	svc := graphService(t)
	if _, err := svc.NeighborhoodAsOf(context.Background(), "99999999-9999-4999-8999-999999999999", ctxgraph.Params{}, w.At(3)); err != ctxgraph.ErrAccountUnknown {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := svc.NeighborhoodAsOf(context.Background(), "nope", ctxgraph.Params{}, w.At(3)); err != ctxgraph.ErrAccountUnknown {
		t.Errorf("malformed account: %v", err)
	}
	if _, err := svc.NeighborhoodAsOf(context.Background(), w.Account, ctxgraph.Params{SectionLimit: 99}, w.At(3)); err == nil {
		t.Error("an out-of-range section limit must be rejected")
	}
}

// ctxgraphStamp is the fixed-width UTC layout of every timestamp property of the graph.
func ctxgraphStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }
