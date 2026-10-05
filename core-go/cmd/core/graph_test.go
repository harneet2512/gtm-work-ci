package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func resetAccounts(t *testing.T) {
	t.Helper()
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE accounts, people, source_events CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestOpenGraphIsOffWithoutNeo4jURI(t *testing.T) {
	t.Setenv("NEO4J_URI", "")
	gr, err := openGraph(context.Background(), env.DB, discardLogger())
	if err != nil || gr != nil {
		t.Fatalf("openGraph = %v %v; core must run unchanged without Neo4j", gr, err)
	}
}

func TestOpenGraphRejectsABadConfigurationAndAnUnreachableServer(t *testing.T) {
	t.Setenv("NEO4J_URI", "bolt://127.0.0.1:1")
	t.Setenv("NEO4J_USER", "neo4j")
	t.Setenv("NEO4J_PASSWORD", "")
	if _, err := openGraph(context.Background(), env.DB, discardLogger()); err == nil {
		t.Error("a user without a password was accepted")
	}
	t.Setenv("NEO4J_PASSWORD", "x")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if _, err := openGraph(ctx, env.DB, discardLogger()); err == nil {
		t.Error("an unreachable Neo4j did not fail startup")
	}
}

func setGraphEnv(t *testing.T) {
	t.Helper()
	n := neo4jtest.Require(t)
	t.Setenv("NEO4J_URI", n.URI)
	t.Setenv("NEO4J_USER", n.User)
	t.Setenv("NEO4J_PASSWORD", n.Password)
	t.Setenv("NEO4J_DATABASE", n.Database)
}

func TestRuntimeStartsTheProjectorAndStopsItCleanly(t *testing.T) {
	setGraphEnv(t)
	resetAccounts(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gr, err := openGraph(ctx, env.DB, discardLogger())
	if err != nil || gr == nil {
		t.Fatalf("openGraph = %v %v", gr, err)
	}
	stop := gr.start(ctx, discardLogger())
	var id string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name, domain) VALUES ('Graph Co', 'graph-test.example') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO graph_projection_jobs (account_id, reasons) VALUES ($1::uuid, '{manual}')`, id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		done, err := gr.barrier.Complete(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the running projector never projected the account")
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop() // must return: the loop exits on cancel
}

func TestRunServesTheGraphEndpointsWhenNeo4jIsConfigured(t *testing.T) {
	setGraphEnv(t)
	resetAccounts(t)
	var id string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name, domain) VALUES ('Graph Co', 'graph-test.example') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	base, stop := startCore(t, testConfig("127.0.0.1:0"))
	defer stop()
	for _, path := range []string{"/graph/projection", "/accounts/" + id + "/graph"} {
		if code, body := request(t, http.MethodGet, base+path, apiToken, ""); code != http.StatusOK {
			t.Errorf("GET %s = %d %s", path, code, body)
		}
	}
	if code, body := request(t, http.MethodGet, base+"/accounts/"+id+"/graph", apiToken, ""); code == http.StatusOK && !strings.Contains(body, `"account_id"`) {
		t.Errorf("graph body = %s", body)
	}
}

func TestGraphEndpointsDoNotExistWithoutNeo4j(t *testing.T) {
	t.Setenv("NEO4J_URI", "")
	base, stop := startCore(t, testConfig("127.0.0.1:0"))
	defer stop()
	if code, _ := request(t, http.MethodGet, base+"/graph/projection", apiToken, ""); code != http.StatusNotFound {
		t.Errorf("GET /graph/projection without Neo4j = %d, want 404", code)
	}
}
