package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// HAR-96 / HAR-129 1B measurement on the CRMArena sample: import -> recompute (WP8 pipeline) -> graph
// projection through the real outbox hooks, then drift = 0, wipe + rebuild equality and the ghostctl
// commands. The numbers are logged (go test -v) and pinned in docs/neo4j.md.
func TestCRMArenaSampleProjectsToNeo4jWithZeroDriftAndRebuildsIdentically(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	nenv := neo4jtest.Require(t)
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	t.Setenv("NEO4J_URI", nenv.URI)
	t.Setenv("NEO4J_USER", nenv.User)
	t.Setenv("NEO4J_PASSWORD", nenv.Password)
	t.Setenv("NEO4J_DATABASE", nenv.Database)
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	ctx := context.Background()

	cfg, _, _ := ctxgraph.ConfigFromEnv()
	g, err := ctxgraph.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close(ctx)
	proj, err := ctxgraph.NewProjector(env.DB, g, ctxgraph.Options{WorkerID: "measure"})
	if err != nil {
		t.Fatal(err)
	}
	if err := proj.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// A shared Neo4j (CI) may hold other packages' leftovers: start from an empty graph.
	if _, err := proj.Rebuild(ctx, true); err != nil {
		t.Fatalf("clear the graph: %v", err)
	}
	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--until", "2023-11-01T00:00:00Z", sample}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	clk := clock.NewFixed(time.Now().UTC().Add(24 * time.Hour))
	hook, err := pipeline.New(pipeline.Options{RunMode: runs.DryRun, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	co, err := coalesce.New(env.DB, coalesce.Options{Hook: ctxgraph.WithProjection(hook, clk), Clock: clk, WorkerID: "replay"})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := co.Drain(ctx); err != nil || res.Failed != 0 {
		t.Fatalf("drain: %+v %v", res, err)
	}

	if done, _ := ctxgraph.NewBarrier(env.DB).Complete(ctx, scalar(t, env, `SELECT id::text FROM accounts LIMIT 1`)); done {
		t.Fatal("the recompute enqueued no projection job: a run could fire before the graph is projected")
	}
	start := time.Now()
	jobs, err := proj.Drain(ctx)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	projectTime := time.Since(start)

	drift, err := proj.Drift(ctx, "")
	if err != nil || drift.Drift != 0 {
		t.Fatalf("drift = %+v %v", drift, err)
	}
	var skipped string
	if err := env.DB.QueryRow(`SELECT COALESCE(jsonb_object_agg(k, v), '{}')::text FROM (
 SELECT key AS k, sum(value::int) AS v FROM graph_projection_checkpoints, jsonb_each_text(skipped) GROUP BY key) x`).Scan(&skipped); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(skipped, "ontology") {
		t.Fatalf("relationship rows were skipped for lack of an ontology edge: %s", skipped)
	}
	first, err := proj.Rebuild(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proj.Rebuild(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Nodes != second.Nodes || first.Edges != second.Edges {
		t.Fatalf("rebuild differs: %+v vs %+v", first, second)
	}
	if first.Nodes != int64(drift.GraphNodes) || first.Edges != int64(drift.GraphEdges) {
		t.Errorf("rebuild counts %d/%d vs projected %d/%d", first.Nodes, first.Edges, drift.GraphNodes, drift.GraphEdges)
	}
	t.Logf("CRMArena sample -> Neo4j: %d jobs projected in %s; %d nodes, %d edges; drift %d; rebuild (wipe) %d nodes %d edges in %dms, digest equal %v; skipped %s",
		jobs, projectTime.Round(time.Millisecond), drift.GraphNodes, drift.GraphEdges, drift.Drift, first.Nodes, first.Edges, first.DurationMS, first.Digest == second.Digest, skipped)

	// The operator commands.
	var rep bytes.Buffer
	if err := run([]string{"graph", "drift"}, &rep); err != nil {
		t.Fatalf("ghostctl graph drift: %v\n%s", err, rep.String())
	}
	rep.Reset()
	if err := run([]string{"graph", "rebuild"}, &rep); err != nil || !strings.Contains(rep.String(), `"digest": "`+first.Digest+`"`) {
		t.Fatalf("ghostctl graph rebuild: %v\n%s", err, rep.String())
	}
	rep.Reset()
	if err := run([]string{"graph", "status"}, &rep); err != nil {
		t.Fatal(err)
	}
	var lag ctxgraph.Lag
	body := rep.String()[strings.Index(rep.String(), "{"):]
	if err := json.Unmarshal([]byte(body), &lag); err != nil || lag.PendingJobs != 0 {
		t.Fatalf("status = %s %v", body, err)
	}
	if err := run([]string{"graph", "bogus"}, &rep); err == nil {
		t.Error("an unknown subcommand must fail")
	}
}

func scalar(t *testing.T, env *storetest.Env, q string) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}
