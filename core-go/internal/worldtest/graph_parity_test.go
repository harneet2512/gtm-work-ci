package worldtest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
)

// firstDifference shows both strings around the first byte where they differ.
func firstDifference(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	window := func(s string) string {
		from, to := max(i-250, 0), min(i+250, len(s))
		return s[from:to]
	}
	return fmt.Sprintf("first difference at byte %d (lengths %d and %d)\nneo4j: ...%s...\nworld: ...%s...", i, len(a), len(b), window(a), window(b))
}

func asJSON(t *testing.T, v ctxgraph.View) string {
	t.Helper()
	v.Projection = ctxgraph.ProjectionStatus{} // the one field that differs by design
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// stripInPlace drops the attributes ADR-0019 deliberately omits from every world read: a person's current
// display name and title, and a decision episode's verdict and status. The current Neo4j projection carries
// them, so it is normalised to the world-timed projection before the parity check. Everything a world read
// does carry (node ids, sections, edges and world-timed data) is still compared exactly.
func stripInPlace(v *ctxgraph.View) {
	for i := range v.Nodes {
		n := &v.Nodes[i]
		switch n.Type {
		case ctxgraph.LabelPerson, ctxgraph.LabelDecisionEpisode:
			n.Label = n.Type
			if n.Data != nil {
				delete(n.Data, "title")
				delete(n.Data, "human_action")
			}
		}
	}
}

// Needs Neo4j (skipped without it; CI runs it against the service container). The world read of a time after
// every event is the current Neo4j view, which proves the in-memory section queries mirror the Cypher ones,
// and deleting and rebuilding the projection changes neither.
func TestWorldGraphAfterEveryEventEqualsTheNeo4jViewAndSurvivesARebuild(t *testing.T) {
	nenv := neo4jtest.Require(t)
	w := seed(t)
	ctx := context.Background()
	g, err := ctxgraph.Open(ctx, ctxgraph.Config{URI: nenv.URI, User: nenv.User, Password: nenv.Password, Database: nenv.Database})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(ctx) })
	proj, err := ctxgraph.NewProjector(env.DB, g, ctxgraph.Options{WorkerID: "worldtest"})
	if err != nil {
		t.Fatal(err)
	}
	reader := ctxgraph.NewReader(g, env.DB, nil)
	svc := ctxgraph.NewService(reader, env.DB)
	// Neo4j filters expired signals by the wall clock, so the world read is compared at the wall clock too.
	later := time.Now().UTC()

	for _, closed := range []bool{false, true} {
		p := ctxgraph.Params{SectionLimit: ctxgraph.MaxSectionLimit, IncludeClosed: closed}
		if _, err := env.DB.Exec(`DELETE FROM graph_projection_jobs`); err != nil {
			t.Fatal(err)
		}
		if _, err := proj.Rebuild(ctx, true); err != nil {
			t.Fatalf("rebuild: %v", err)
		}
		current, err := svc.Neighborhood(ctx, w.Account, p)
		if err != nil {
			t.Fatal(err)
		}
		world, err := svc.NeighborhoodAsOf(ctx, w.Account, p, later)
		if err != nil {
			t.Fatal(err)
		}
		stripInPlace(&current)
		stripInPlace(&world)
		if a, b := asJSON(t, current), asJSON(t, world); a != b {
			t.Fatalf("include_closed=%v: the world view after every event differs from the Neo4j view\n%s", closed, firstDifference(a, b))
		}

		// Delete the projection and rebuild it: the world read must not move.
		before, err := svc.NeighborhoodAsOf(ctx, w.Account, p, w.At(3))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proj.Rebuild(ctx, true); err != nil {
			t.Fatalf("second rebuild: %v", err)
		}
		after, err := svc.NeighborhoodAsOf(ctx, w.Account, p, w.At(3))
		if err != nil {
			t.Fatal(err)
		}
		if a, b := asJSON(t, before), asJSON(t, after); a != b {
			t.Fatalf("a world read changed across a delete-and-rebuild of the projection:\n%.800s\n%.800s", a, b)
		}
	}
}
