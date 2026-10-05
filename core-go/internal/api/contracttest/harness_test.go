// Package contracttest runs the core HTTP API against a real database and checks every response
// against contracts/openapi/core.yaml: the account reads, the run trace and the run-token-scoped
// context pulls. It holds only tests.
package contracttest

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/outbox"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

const (
	apiToken   = "operator-token-for-tests-0123456789"
	signingKey = "0123456789abcdef0123456789abcdef-signing"
	missingID  = "99999999-9999-4999-8999-999999999999"
)

var env *storetest.Env

// testBreaker is the provider circuit breaker behind GET /provider-breaker (HAR-135).
var testBreaker = func() *providerbreaker.Breaker {
	b, err := providerbreaker.New(2, time.Minute, nil, nil)
	if err != nil {
		panic(err)
	}
	return b
}()

func TestMain(m *testing.M) {
	code := storetest.Main(m, func(e *storetest.Env) { env = e })
	neo4jtest.Stop()
	os.Exit(code)
}

// stack is a running API over the sample world.
type stack struct {
	t      *testing.T
	srv    *httptest.Server
	world  *ctxfixture.World
	signer *runtoken.Signer
	spec   *openapitest.Spec
	logs   *bytes.Buffer
	ing    *ingest.Service
	sup    *reactions.Service
}

var sharedSpec *openapitest.Spec

// checkedOperations records every "METHOD path-template" a conformance check has validated.
var checkedOperations = map[string]bool{}

func newStack(t *testing.T) *stack {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	if sharedSpec == nil {
		spec, err := openapitest.Load()
		if err != nil {
			t.Fatal(err)
		}
		sharedSpec = spec
	}
	signer, err := runtoken.NewSigner([]byte(signingKey), runtoken.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension()})
	if err != nil {
		t.Fatal(err)
	}
	reads, err := readmodel.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	pulls, err := corectx.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := strategystore.New(env.DB, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := knowledgestore.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	events, err := outbox.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := surfacemsg.New(env.DB)
	if err != nil {
		t.Fatal(err)
	}
	sup, err := reactions.New(env.DB, lifecycleRules(t), reactions.Options{})
	if err != nil {
		t.Fatal(err)
	}
	disputes, err := evaldispute.New(env.DB, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(svc, apiToken, slog.New(slog.NewTextHandler(logs, nil)),
		api.WithReads(reads), api.WithContext(pulls, signer), api.WithBreaker(testBreaker), api.WithStrategy(strategies), api.WithKnowledge(knowledge), api.WithOutbox(events), api.WithSurfaceMessages(refs), api.WithSupervision(sup), api.WithEvalDisputes(disputes))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &stack{t: t, srv: srv, world: world, signer: signer, spec: sharedSpec, logs: logs, ing: svc, sup: sup}
}

// reply is one HTTP exchange.
type reply struct {
	status int
	body   []byte
	header http.Header
}

func (s *stack) do(method, path, template, token string, body []byte) reply {
	s.t.Helper()
	req, err := http.NewRequest(method, s.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
	r := reply{status: resp.StatusCode, body: raw, header: resp.Header}
	if template != "" {
		checkedOperations[method+" "+template] = true
		if err := s.spec.Response(method, template, r.status, raw); err != nil {
			s.t.Errorf("%s %s -> %d violates the contract: %v\n%s", method, path, r.status, err, clip(raw))
		}
	}
	return r
}

func clip(b []byte) string {
	if len(b) > 600 {
		return string(b[:600]) + "..."
	}
	return string(b)
}

// get is an operator GET, checked against the spec template.
func (s *stack) get(path, template string) reply { return s.do("GET", path, template, apiToken, nil) }

// pull is a context pull with a run token, checked against the spec.
func (s *stack) pull(token, tool, query string) reply {
	path := "/internal/ctx/" + tool
	if query != "" {
		path += "?" + query
	}
	return s.do("GET", path, "/internal/ctx/{tool}", token, nil)
}

// token mints a valid run token for a fresh open run of the account and returns both.
func (s *stack) token(account string) (runID, token string) {
	s.t.Helper()
	runID = ctxfixture.FreshRun(s.t, env.DB, account, "context_built")
	tok, err := s.signer.Issue(runID, time.Now())
	if err != nil {
		s.t.Fatal(err)
	}
	return runID, tok
}

func decodePacket(t *testing.T, r reply) corectx.Packet {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, clip(r.body))
	}
	var p corectx.Packet
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func errorCode(t *testing.T, r reply) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil {
		t.Fatalf("not an error envelope: %s", clip(r.body))
	}
	return e.Error.Code
}

// lifecycleRules loads the shipped lifecycle contract for the supervision service under test.
func lifecycleRules(t *testing.T) knowledge.Rules {
	t.Helper()
	r, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatalf("load lifecycle rules: %v", err)
	}
	return r
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}
