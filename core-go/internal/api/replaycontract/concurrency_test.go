package replaycontract

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
)

// Concurrency and timeout behaviour of POST /replay/play over HTTP: a second Play while one runs, and a Play
// that cannot finish.

// gate holds the Play that reaches ingest until release is closed.
type gate struct {
	inner   play.Ingester
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gate) Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.inner.Ingest(ctx, ev)
}

func TestASecondPlayWhileOneRunsAnswers409PlayInProgress(t *testing.T) {
	g := &gate{entered: make(chan struct{}), release: make(chan struct{})}
	s := newServer(t, func(o *play.Options) { g.inner, o.Ingest = o.Ingest, g })
	first := make(chan reply, 1)
	go func() { first <- s.play(map[string]any{"manifest_id": s.manifest}) }()
	select {
	case <-g.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the first Play never reached ingest")
	}
	r := s.play(map[string]any{"manifest_id": s.manifest})
	if r.status != 409 || code(t, r) != "play_in_progress" || r.header.Get("Retry-After") == "" {
		t.Fatalf("second Play while the first runs: %d %s (Retry-After %q)", r.status, r.body, r.header.Get("Retry-After"))
	}
	close(g.release)
	if done := <-first; done.status != 200 {
		t.Fatalf("the first Play: %d %s", done.status, done.body)
	}
}

func TestAPlayThatDoesNotFinishAnswers504(t *testing.T) {
	s := newServer(t, func(o *play.Options) { o.Graph = stuck{}; o.Timeout = time.Second })
	if r := s.play(map[string]any{"manifest_id": s.manifest}); r.status != 504 || code(t, r) != "timeout" {
		t.Fatalf("Play with the projector down: %d %s", r.status, r.body)
	}
}
