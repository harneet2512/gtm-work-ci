package main

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
)

// graphPoll is how often the projector looks for outbox jobs when idle.
const graphPoll = 200 * time.Millisecond

// graphRuntime is the optional Neo4j projection: the outbox hooks, the single projector, the read side
// and the barrier that keeps an agent from reading a graph that has not caught up.
type graphRuntime struct {
	graph     *ctxgraph.Graph
	projector *ctxgraph.Projector
	service   *ctxgraph.Service
	reader    *ctxgraph.Reader
	barrier   *ctxgraph.Barrier
}

// openGraph connects to Neo4j when NEO4J_URI is set and returns nil when it is not: core then runs
// exactly as before, with no projection and no graph endpoints.
func openGraph(ctx context.Context, db *sql.DB, logger *slog.Logger) (*graphRuntime, error) {
	cfg, ok, err := ctxgraph.ConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if !ok {
		logger.Info("NEO4J_URI is empty: the graph projection is disabled")
		return nil, nil
	}
	g, err := ctxgraph.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p, err := ctxgraph.NewProjector(db, g, ctxgraph.Options{Logger: logger})
	if err != nil {
		return nil, err
	}
	if err := p.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	reader := ctxgraph.NewReader(g, db, nil)
	return &graphRuntime{graph: g, projector: p, reader: reader, service: ctxgraph.NewService(reader, db), barrier: ctxgraph.NewBarrier(db)}, nil
}

// start runs the projector until ctx is cancelled; the returned function stops it and waits.
func (gr *graphRuntime) start(ctx context.Context, logger *slog.Logger) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := gr.projector.Run(ctx, graphPoll); err != nil && ctx.Err() == nil {
			logger.Error("graph projector stopped", "error", err)
		}
	}()
	return func() {
		cancel()
		<-done
		_ = gr.graph.Close(context.Background())
	}
}
