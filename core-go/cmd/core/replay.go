package main

import (
	"database/sql"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/play"
)

// newReplay builds the Play service behind POST /replay/play, the episode surface and the invisibility check
// (HAR-124, HAR-129). It releases through core's own ingest service and waits for the background coalescer
// and the graph projector, so there is no second pipeline. Without the replay dataset or without Neo4j it
// still serves, and each call says what is missing (503) before anything is released; both are said out loud
// at startup. Without lifecycle rules the episode view's knowledge-as-of read fails closed
// (503 knowledge_unavailable), and GHOST_REPLAY_HISTORICAL_END moves the historical/live split off its
// N-1 default.
func newReplay(db *sql.DB, svc *ingest.Service, cfg config.Config, logger *slog.Logger, gr *graphRuntime) (*play.Service, error) {
	stages, err := newStageRecorder(db)
	if err != nil {
		return nil, err
	}
	opts := play.Options{DB: db, Ingest: svc, HistoricalEnd: cfg.ReplayHistoricalEnd, Stages: stages}
	if cfg.ReplayEventsPath != "" {
		src, err := play.NewFileSource(cfg.ReplayEventsPath)
		if err != nil {
			return nil, err
		}
		opts.Events = src
	} else {
		logger.Warn("GHOST_REPLAY_EVENTS is empty: Play cannot release the held-out event (POST /replay/play answers 503 replay_source_unavailable)")
	}
	if cfg.KnowledgeRulesPath != "" {
		rules, err := knowledge.LoadRules(cfg.KnowledgeRulesPath)
		if err != nil {
			return nil, err
		}
		opts.Rules = &rules
	} else {
		logger.Warn("GHOST_KNOWLEDGE_RULES is empty: GET /replay/manifests/{id}/episodes answers 503 knowledge_unavailable")
	}
	if gr != nil {
		opts.Graph, opts.Probe = gr.barrier, gr.reader
	} else {
		logger.Warn("NEO4J_URI is empty: Play and the event-N-invisible check need the graph (they answer 503 graph_unavailable)")
	}
	return play.NewService(opts)
}
