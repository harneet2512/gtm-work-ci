// Package replaycontract runs POST /replay/play and GET /replay/manifests/{id}/invisibility over HTTP against the
// real Play service, the real pipeline and a real database, and checks every response against
// contracts/openapi/core.yaml. It has its own database and world (the replay tests reset every table), which is
// why it is not part of the contracttest package. It holds only tests.
package replaycontract

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

const apiToken = "operator-token-for-tests-0123456789"

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var bg = context.Background()

type probe struct{ hits []ctxgraph.Hit }

func (p probe) Referencing(context.Context, []string) ([]ctxgraph.Hit, error) { return p.hits, nil }
func (p probe) ActivitiesFrom(context.Context, string, string, time.Time) ([]ctxgraph.Hit, error) {
	return nil, nil
}

type projecting struct {
	t  *testing.T
	mu sync.Mutex
}

func (b *projecting) Wait(context.Context, string, time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	replaytest.FakeProjector{DB: env.DB}.Complete(b.t)
	return nil
}

type stuck struct{}

func (stuck) Wait(ctx context.Context, _ string, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

type dataset map[play.SourceRef]normalize.SourceEvent

func (d dataset) Lookup(_ context.Context, ref play.SourceRef) (normalize.SourceEvent, error) {
	if ev, ok := d[ref]; ok {
		return ev, nil
	}
	return normalize.SourceEvent{}, play.ErrEventNotInSource
}

type drainer struct{ s *replaytest.Stack }

func (d drainer) Drain(ctx context.Context) (coalesce.DrainResult, error) {
	d.s.CoalClock.Set(d.s.CoalClock.Now().Add(replaytest.Debounce + time.Second))
	return d.s.Coalescer.Drain(ctx)
}

// server is core's HTTP surface over a replay world.
type server struct {
	t        *testing.T
	srv      *httptest.Server
	spec     *openapitest.Spec
	world    replaytest.World
	stack    *replaytest.Stack
	held     replaytest.HeldOut
	manifest string
}

func newServer(t *testing.T, mutate func(*play.Options)) *server {
	t.Helper()
	spec, err := openapitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, spec: spec, held: replaytest.NewHeldOut(2)}
	s.world = replaytest.SeedWorld(t, env.DB)
	s.stack = replaytest.NewStack(t, env.DB)
	s.stack.History(t, 2)
	s.manifest = replaytest.InsertManifest(t, env.DB, s.world, s.held)

	ev := s.held.Event
	opts := play.Options{DB: env.DB, Ingest: s.stack.Ingest, Recompute: drainer{s.stack}, Graph: &projecting{t: t}, Probe: probe{},
		Events: dataset{{System: ev.SourceSystem, ObjectID: ev.SourceObjectID, EventKey: ev.SourceEventKey}: ev},
		Clock:  clock.NewFixed(replaytest.T0.Add(2 * time.Hour)), Poll: 5 * time.Millisecond, Timeout: 30 * time.Second}
	stages, err := stageevents.NewRecorder(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := stageevents.NewReader(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.Stages = stages
	if mutate != nil {
		mutate(&opts)
	}
	svc, err := play.NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	h, err := api.NewHandler(s.stack.Ingest, apiToken, slog.New(slog.NewTextHandler(logs, nil)), api.WithReplay(svc), api.WithStrategy(strategies), api.WithProgress(progress))
	if err != nil {
		t.Fatal(err)
	}
	s.srv = httptest.NewServer(h)
	t.Cleanup(s.srv.Close)
	return s
}

type reply struct {
	status int
	body   []byte
	header http.Header
}

// do sends a request and checks the response against the spec operation template.
func (s *server) do(method, path, template, token string, body []byte) reply {
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
	raw, _ := io.ReadAll(resp.Body)
	if err := s.spec.Response(method, template, resp.StatusCode, raw); err != nil {
		s.t.Errorf("%s %s -> %d violates the contract: %v\n%s", method, path, resp.StatusCode, err, raw)
	}
	return reply{status: resp.StatusCode, body: raw, header: resp.Header}
}

const (
	playPath      = "/replay/play"
	invisiblePath = "/replay/manifests/{manifest_id}/invisibility"
)

func (s *server) play(body any) reply {
	s.t.Helper()
	raw, _ := json.Marshal(body)
	return s.do("POST", playPath, playPath, apiToken, raw)
}

func (s *server) invisibility(id string) reply {
	s.t.Helper()
	return s.do("GET", "/replay/manifests/"+id+"/invisibility", invisiblePath, apiToken, nil)
}

func code(t *testing.T, r reply) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.body, &e); err != nil {
		t.Fatalf("not an error envelope: %s", r.body)
	}
	return e.Error.Code
}

func status(t *testing.T, r reply) string {
	t.Helper()
	var e struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(r.body, &e)
	return e.Status
}

func TestPlayOverHTTPConformsToTheContractAndFeedsTheExistingReadEndpoint(t *testing.T) {
	s := newServer(t, nil)

	if r := s.invisibility(s.manifest); r.status != 200 || status(t, r) != "withheld" {
		t.Fatalf("before Play: %d %s", r.status, r.body)
	}
	req := map[string]any{"manifest_id": s.manifest}
	if err := s.spec.Request("POST", playPath, mustJSON(req)); err != nil {
		t.Fatalf("the test's own request is not a valid play body: %v", err)
	}
	r := s.play(req)
	if r.status != 200 {
		t.Fatalf("Play: %d %s", r.status, r.body)
	}
	var result struct {
		AccountChange              struct{ ID string } `json:"account_change"`
		BusinessIntelligenceUpdate struct {
			ID     string
			Claims []struct {
				EvidenceRefs []json.RawMessage `json:"evidence_refs"`
			}
		} `json:"business_intelligence_update"`
	}
	if err := json.Unmarshal(r.body, &result); err != nil {
		t.Fatal(err)
	}
	if result.BusinessIntelligenceUpdate.ID == "" || len(result.BusinessIntelligenceUpdate.Claims) == 0 {
		t.Fatalf("a material event must come with its update: %s", r.body)
	}

	// the existing read endpoint (and so Slack Message 1) serves the writer's update
	served := s.do("GET", "/accounts/"+s.world.Account+"/business-intelligence/latest", "/accounts/{account_id}/business-intelligence/latest", apiToken, nil)
	var latest struct{ ID string }
	_ = json.Unmarshal(served.body, &latest)
	if served.status != 200 || latest.ID != result.BusinessIntelligenceUpdate.ID {
		t.Fatalf("latest update = %d %s, Play wrote %s", served.status, served.body, result.BusinessIntelligenceUpdate.ID)
	}

	// a second Play changes nothing
	counts := func() string {
		return replaytest.One(t, env.DB, `SELECT (SELECT count(*) FROM source_events) || '/' || (SELECT count(*) FROM account_changes) || '/' ||
			(SELECT count(*) FROM business_intelligence_updates) || '/' || (SELECT count(*) FROM state_history)`)
	}
	before := counts()
	again := s.play(req)
	if again.status != 409 || code(t, again) != "already_released" {
		t.Fatalf("second Play: %d %s", again.status, again.body)
	}
	var refusal struct {
		Error struct {
			Details struct {
				AccountChangeID string `json:"account_change_id"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(again.body, &refusal); err != nil || refusal.Error.Details.AccountChangeID != result.AccountChange.ID {
		t.Fatalf("the 409 must name the account change %s in its details: %s", result.AccountChange.ID, again.body)
	}
	if after := counts(); after != before {
		t.Fatalf("a second Play changed the database: %s -> %s", before, after)
	}
	if rep := s.invisibility(s.manifest); status(t, rep) != "released" {
		t.Fatalf("after Play: %s", rep.body)
	}
}

func TestPlayRefusalsOverHTTPConformToTheContract(t *testing.T) {
	s := newServer(t, nil)
	missing := "99999999-9999-4999-8999-999999999999"

	if r := s.do("POST", playPath, playPath, "", mustJSON(map[string]any{"manifest_id": s.manifest})); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("GET", "/replay/manifests/"+s.manifest+"/invisibility", invisiblePath, "wrong", nil); r.status != 401 {
		t.Fatalf("wrong token: %d", r.status)
	}
	for name, body := range map[string]string{"unknown field": `{"manifest_id":"` + s.manifest + `","bogus":1}`, "not a uuid": `{"manifest_id":"nope"}`,
		"empty": `{}`, "bad event id": `{"manifest_id":"` + s.manifest + `","event_id":"x"}`} {
		if r := s.do("POST", playPath, playPath, apiToken, []byte(body)); r.status != 400 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := s.play(map[string]any{"manifest_id": missing}); r.status != 404 || code(t, r) != "manifest_not_found" {
		t.Fatalf("unknown manifest: %d %s", r.status, r.body)
	}
	if r := s.invisibility(missing); r.status != 404 || code(t, r) != "manifest_not_found" {
		t.Fatalf("unknown manifest (invisibility): %d %s", r.status, r.body)
	}
	if r := s.invisibility("nope"); r.status != 404 {
		t.Fatalf("malformed manifest id (invisibility): %d", r.status)
	}
	if r := s.play(map[string]any{"manifest_id": s.manifest, "event_id": missing}); r.status != 422 || code(t, r) != "wrong_event" {
		t.Fatalf("another event: %d %s", r.status, r.body)
	}
	if n := replaytest.One(t, env.DB, `SELECT count(*)::text FROM source_events`); n != "2" {
		t.Fatalf("a refused Play released the event: source_events = %s", n)
	}
}

func TestPlayRefusesAVisibleEventOverHTTPAndNamesTheLeak(t *testing.T) {
	s := newServer(t, nil)
	if _, err := s.stack.Ingest.Ingest(bg, s.held.Event); err != nil { // the injected leak
		t.Fatal(err)
	}
	r := s.play(map[string]any{"manifest_id": s.manifest})
	if r.status != 422 || code(t, r) != "held_out_event_visible" {
		t.Fatalf("Play: %d %s", r.status, r.body)
	}
	var e struct {
		Error struct {
			Details struct {
				Report struct {
					Status string `json:"status"`
					Leaks  []struct{ Store, Kind, ID string }
				}
			}
		}
	}
	if err := json.Unmarshal(r.body, &e); err != nil || e.Error.Details.Report.Status != "leaked" || len(e.Error.Details.Report.Leaks) == 0 {
		t.Fatalf("the refusal must name what leaked: %s", r.body)
	}
	if rep := s.invisibility(s.manifest); status(t, rep) != "leaked" {
		t.Fatalf("invisibility: %s", rep.body)
	}
	if n := replaytest.One(t, env.DB, `SELECT count(*)::text FROM account_changes`); n != "0" {
		t.Fatal("a refused Play wrote a change")
	}
}

func TestPlayAnswers503WhenTheGraphOrTheDatasetIsMissing(t *testing.T) {
	cases := map[string]struct {
		mutate func(*play.Options)
		want   string
	}{
		"no graph":   {func(o *play.Options) { o.Graph, o.Probe = nil, nil }, "graph_unavailable"},
		"no dataset": {func(o *play.Options) { o.Events = nil }, "replay_source_unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := newServer(t, tc.mutate)
			r := s.play(map[string]any{"manifest_id": s.manifest})
			if r.status != 503 || code(t, r) != tc.want {
				t.Fatalf("Play: %d %s", r.status, r.body)
			}
			if name == "no graph" && r.header.Get("Retry-After") == "" {
				t.Error("a 503 for the graph carries Retry-After")
			}
			if n := replaytest.One(t, env.DB, `SELECT count(*)::text FROM source_events`); n != "2" {
				t.Fatalf("a refused Play released the event: source_events = %s", n)
			}
		})
	}
	s := newServer(t, func(o *play.Options) { o.Graph, o.Probe = nil, nil })
	if r := s.invisibility(s.manifest); r.status != 503 || code(t, r) != "graph_unavailable" {
		t.Fatalf("invisibility without a graph: %d %s", r.status, r.body)
	}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
