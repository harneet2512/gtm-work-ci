package contracttest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// graphOperations are the operations that need Neo4j; without it they stay unexercised (and unwired).
var graphOperations = map[string]bool{
	"GET /accounts/{account_id}/graph":  true,
	"GET /events/{event_id}/graph-diff": true,
	"GET /graph/projection":             true,
}

// graphStack is the API with the Neo4j projection wired in, over the sample world already projected.
type graphStack struct {
	*stack
	proj *ctxgraph.Projector
}

func newGraphStack(t *testing.T) *graphStack {
	t.Helper()
	nenv := neo4jtest.Require(t)
	s := newStack(t) // loads the world and the spec
	ctx := context.Background()
	g, err := ctxgraph.Open(ctx, ctxgraph.Config{URI: nenv.URI, User: nenv.User, Password: nenv.Password, Database: nenv.Database})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(ctx) })
	proj, err := ctxgraph.NewProjector(env.DB, g, ctxgraph.Options{WorkerID: "contracttest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`DELETE FROM graph_projection_jobs`); err != nil {
		t.Fatal(err)
	}
	if _, err := proj.Rebuild(ctx, true); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	reader := ctxgraph.NewReader(g, env.DB, nil)
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension()})
	if err != nil {
		t.Fatal(err)
	}
	reads, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	pulls, err := corectx.New(env.DB, corectx.WithGraph(reader))
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(svc, apiToken, slog.New(slog.NewTextHandler(s.logs, nil)),
		api.WithReads(reads), api.WithGraph(ctxgraph.NewService(reader, env.DB)), api.WithContext(pulls, s.signer))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s.srv = srv
	return &graphStack{stack: s, proj: proj}
}

func TestGraphEndpointsConformToTheContract(t *testing.T) {
	s := newGraphStack(t)
	r := s.get("/accounts/"+s.world.AccountA+"/graph?limit=5", "/accounts/{account_id}/graph")
	if r.status != http.StatusOK {
		t.Fatalf("graph: %d %s", r.status, clip(r.body))
	}
	if !strings.Contains(string(r.body), `"complete":true`) || strings.Contains(string(r.body), s.world.AccountB) {
		t.Errorf("graph must be projected and scoped to the account: %s", clip(r.body))
	}
	if r := s.get("/accounts/"+s.world.AccountA+"/graph?include_closed=true", "/accounts/{account_id}/graph"); r.status != http.StatusOK {
		t.Errorf("include_closed: %d", r.status)
	}
	if r := s.get("/accounts/"+missingID+"/graph", "/accounts/{account_id}/graph"); r.status != http.StatusNotFound {
		t.Errorf("unknown account: %d", r.status)
	}
	if r := s.get("/accounts/not-a-uuid/graph", "/accounts/{account_id}/graph"); r.status != http.StatusNotFound {
		t.Errorf("malformed account id: %d", r.status)
	}
	if r := s.get("/accounts/"+s.world.AccountA+"/graph?limit=500", "/accounts/{account_id}/graph"); r.status != http.StatusBadRequest {
		t.Errorf("limit 500: %d", r.status)
	}
	if r := s.do("GET", "/accounts/"+s.world.AccountA+"/graph", "/accounts/{account_id}/graph", "", nil); r.status != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.status)
	}

	event := scalar(t, `SELECT source_event_id::text FROM activities WHERE account_id = $1::uuid ORDER BY occurred_at LIMIT 1`, s.world.AccountA)
	if r := s.get("/events/"+event+"/graph-diff", "/events/{event_id}/graph-diff"); r.status != http.StatusOK {
		t.Errorf("event diff: %d %s", r.status, clip(r.body))
	}
	if r := s.get("/events/"+missingID+"/graph-diff", "/events/{event_id}/graph-diff"); r.status != http.StatusNotFound {
		t.Errorf("unknown event: %d", r.status)
	}
	if r := s.get("/graph/projection", "/graph/projection"); r.status != http.StatusOK {
		t.Errorf("lag: %d", r.status)
	}
}

func TestGraphNeighborhoodToolIsScopedAndWaitsForTheProjection(t *testing.T) {
	s := newGraphStack(t)
	_, token := s.token(s.world.AccountA)
	p := decodePacket(t, s.pull(token, "graph_neighborhood", "limit=10"))
	if len(p.Items) == 0 {
		t.Fatal("the graph tool returned nothing for a projected account")
	}
	for _, raw := range p.Items {
		if strings.Contains(string(raw), s.world.AccountB) {
			t.Fatalf("the tool leaked another account: %.300s", raw)
		}
	}
	if !strings.Contains(string(p.Items[0]), `"section":"account"`) || !strings.Contains(string(p.Items[0]), `"projection"`) {
		t.Errorf("first item = %.300s", p.Items[0])
	}
	if p.Bytes > corectx.MaxPacketBytes {
		t.Errorf("packet of %d bytes exceeds the bound", p.Bytes)
	}

	// No barrier (ADR-0019): an unfinished projection job does not close the pulls of the account, which are
	// world-cut and read Postgres only.
	if err := ctxgraph.Enqueue(context.Background(), env.DB, s.world.AccountA, nil, ctxgraph.ReasonRecompute, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"graph_neighborhood", "state"} {
		if r := s.pull(token, tool, "limit=5"); r.status != http.StatusOK {
			t.Fatalf("%s during an unfinished projection: %d %s", tool, r.status, clip(r.body))
		}
	}
	if _, err := s.proj.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTheGraphToolDoesNotExistWithoutNeo4j(t *testing.T) {
	s := newStack(t)
	_, token := s.token(s.world.AccountA)
	if r := s.pull(token, "graph_neighborhood", ""); r.status != http.StatusNotFound {
		t.Fatalf("status %d: the tool must be unknown when core has no graph", r.status)
	}
}
