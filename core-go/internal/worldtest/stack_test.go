package worldtest

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/worldfixture"
)

const (
	apiToken   = "operator-token-for-tests-0123456789"
	signingKey = "0123456789abcdef0123456789abcdef-signing"
)

var spec *openapitest.Spec

// stack is the real HTTP API over the seeded world: the operator reads, the graph service without Neo4j
// (world reads need none) and the run-token context pulls. Every response is validated against core.yaml.
type stack struct {
	t      *testing.T
	srv    *httptest.Server
	world  *worldfixture.World
	signer *runtoken.Signer
}

func newStack(t *testing.T) *stack {
	t.Helper()
	w := seed(t)
	if spec == nil {
		s, err := openapitest.Load()
		if err != nil {
			t.Fatal(err)
		}
		spec = s
	}
	signer, err := runtoken.NewSigner([]byte(signingKey), runtoken.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension()})
	if err != nil {
		t.Fatal(err)
	}
	reads, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	reader := ctxgraph.NewReader(nil, env.DB, nil) // no Neo4j: only world reads work
	pulls, err := corectx.New(env.DB, corectx.WithGraph(reader))
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(svc, apiToken, slog.New(slog.NewTextHandler(io.Discard, nil)),
		api.WithReads(reads), api.WithGraph(ctxgraph.NewService(reader, env.DB)), api.WithContext(pulls, signer))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &stack{t: t, srv: srv, world: w, signer: signer}
}

type reply struct {
	status int
	body   []byte
}

// get is an operator GET checked against the contract template.
func (s *stack) get(path, template string) reply { return s.do(path, template, apiToken) }

func (s *stack) do(path, template, token string) reply {
	s.t.Helper()
	req, err := http.NewRequest("GET", s.srv.URL+path, bytes.NewReader(nil))
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if template != "" {
		if err := spec.Response("GET", template, resp.StatusCode, raw); err != nil {
			s.t.Errorf("GET %s -> %d violates the contract: %v\n%.600s", path, resp.StatusCode, err, raw)
		}
	}
	return reply{status: resp.StatusCode, body: raw}
}

// pull is a run-scoped context pull checked against the contract.
func (s *stack) pull(token, tool, query string) reply {
	s.t.Helper()
	path := "/internal/ctx/" + tool
	if query != "" {
		path += "?" + query
	}
	return s.do(path, "/internal/ctx/{tool}", token)
}

// runToken mints a run token for a new run triggered by event n.
func (s *stack) runToken(n int) string {
	s.t.Helper()
	runID := s.world.NewRun(s.t, n)
	tok, err := s.signer.Issue(runID, time.Now())
	if err != nil {
		s.t.Fatal(err)
	}
	return tok
}

func errorCode(t *testing.T, r reply) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil {
		t.Fatalf("not an error envelope: %.300s", r.body)
	}
	return e.Error.Code
}
