package replaycontract

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/play"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

const progressPath = "/replay/manifests/{manifest_id}/progress"

type stageView struct {
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	Attempt     int    `json:"attempt"`
	FailureKind string `json:"failure_kind"`
	Refs        struct {
		SourceEventID   string `json:"source_event_id"`
		StateVersion    int    `json:"state_version"`
		GraphDiffID     int64  `json:"graph_diff_id"`
		AccountChangeID string `json:"account_change_id"`
	} `json:"refs"`
}

type progressView struct {
	Overall string      `json:"overall"`
	Scope   string      `json:"scope"`
	Stages  []stageView `json:"stages"`
}

func (p progressView) stage(name string) stageView {
	for _, s := range p.Stages {
		if s.Stage == name {
			return s
		}
	}
	return stageView{}
}

func (s *server) progress(id string) (progressView, reply) {
	s.t.Helper()
	r := s.do("GET", "/replay/manifests/"+id+"/progress", progressPath, apiToken, nil)
	var p progressView
	if r.status == http.StatusOK {
		if err := json.Unmarshal(r.body, &p); err != nil {
			s.t.Fatalf("%v\n%s", err, r.body)
		}
	}
	return p, r
}

// heldBarrier is a graph barrier that holds the projection until the test opens it: the Play request stays open while
// the test polls the progress endpoint, which is how a browser sees the pipeline move.
type heldBarrier struct {
	inner   *projecting
	reached chan struct{}
	open    chan struct{}
	once    sync.Once
}

func (g *heldBarrier) Wait(ctx context.Context, account string, poll time.Duration) error {
	g.once.Do(func() { close(g.reached) })
	select {
	case <-g.open:
	case <-ctx.Done():
		return ctx.Err()
	}
	return g.inner.Wait(ctx, account, poll)
}

func TestProgressMovesWhileThePlayRequestIsStillOpen(t *testing.T) {
	g := &heldBarrier{reached: make(chan struct{}), open: make(chan struct{})}
	s := newServer(t, func(o *play.Options) {
		g.inner = &projecting{t: t}
		o.Graph = g
	})

	p, r := s.progress(s.manifest)
	if r.status != 200 || p.Overall != "not_started" || len(p.Stages) != 7 {
		t.Fatalf("before Play: %d overall %q stages %d", r.status, p.Overall, len(p.Stages))
	}
	if r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("a poll must never be cached: %q", r.header.Get("Cache-Control"))
	}

	done := make(chan reply, 1)
	go func() { done <- s.play(map[string]any{"manifest_id": s.manifest}) }()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the pipeline never reached the graph projection")
	}

	// the POST is still open and the pipeline is inside the graph stage: a separate request sees exactly that
	p, _ = s.progress(s.manifest)
	if p.stage("ingest").Status != "completed" || p.stage("resolve").Status != "completed" || p.stage("state").Status != "completed" {
		t.Fatalf("stages before the graph: %+v", p.Stages)
	}
	if g := p.stage("graph"); g.Status != "running" {
		t.Fatalf("graph = %+v, want running while the projection is pending", g)
	}
	if p.Overall != "running" || p.stage("decide").Status != "waiting" {
		t.Fatalf("overall %q, decide %q", p.Overall, p.stage("decide").Status)
	}
	select {
	case <-done:
		t.Fatal("the Play finished before the projection opened: the progress above was not live")
	default:
	}

	close(g.open)
	if r := <-done; r.status != 200 {
		t.Fatalf("Play: %d %s", r.status, r.body)
	}
	p, _ = s.progress(s.manifest)
	graph := p.stage("graph")
	if graph.Status != "completed" || graph.Refs.GraphDiffID == 0 || p.stage("resolve").Refs.AccountChangeID == "" || p.stage("state").Refs.StateVersion == 0 {
		t.Fatalf("after Play: %+v", p.Stages)
	}
	if p.Overall != "running" { // the run has not decided: decide, evals and cliff still wait
		t.Errorf("overall after Play = %q", p.Overall)
	}
}

func TestAnInterruptedPlayShowsTheFailedStageAndAResumeCompletesIt(t *testing.T) {
	s := newServer(t, func(o *play.Options) { o.Graph = &stuck{}; o.Timeout = time.Second })
	if r := s.play(map[string]any{"manifest_id": s.manifest}); r.status != 504 {
		t.Fatalf("Play with the projector down: %d %s", r.status, r.body)
	}
	p, _ := s.progress(s.manifest)
	if g := p.stage("graph"); g.Status != "failed" || g.FailureKind != "transport" {
		t.Fatalf("graph = %+v, want failed/transport: a projector that never answered is not a verdict", g)
	}
	if p.Overall != "failed" || p.stage("decide").Status != "waiting" {
		t.Fatalf("overall %q decide %q", p.Overall, p.stage("decide").Status)
	}
	if got := replaytest.One(t, env.DB, `SELECT count(*)::text FROM pipeline_stage_events`); got != "4" {
		t.Errorf("stage rows = %s, want the 4 stages that ran", got)
	}
}

func TestProgressOfAnUnknownManifestIs404AndNeedsTheOperatorToken(t *testing.T) {
	s := newServer(t, nil)
	r := s.do("GET", "/replay/manifests/99999999-9999-4999-8999-999999999999/progress", progressPath, apiToken, nil)
	if r.status != 404 || code(t, r) != "manifest_not_found" {
		t.Fatalf("unknown manifest: %d %s", r.status, r.body)
	}
	if r := s.do("GET", "/replay/manifests/"+s.manifest+"/progress", progressPath, "", nil); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
}
