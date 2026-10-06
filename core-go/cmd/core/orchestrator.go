package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/rundriver"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// tokenMargin is how far past RequiredTokenTTL the orchestrator's run tokens live.
const tokenMargin = time.Minute

// deps are the collaborators run builds itself unless a test supplies a double.
type deps struct {
	// worker is the model worker the orchestrator calls. nil selects the HTTP worker at cfg.WorkerURL. Either way it
	// runs behind the process-wide provider breaker.
	worker orchestrator.Worker
	// breaker is the process-wide provider breaker. nil builds one from the configuration; a test passes its own to
	// open and close it.
	breaker *providerbreaker.Breaker
}

// newOrchestratorDriver builds the background driver that takes every opened AgentRun to a published StrategySet
// (HAR-117), or returns nil when GHOST_ORCHESTRATOR is off. Misconfiguration (no worker, a missing rule file)
// fails startup with a message naming the setting; a half-wired driver is never started.
func newOrchestratorDriver(db *sql.DB, cfg config.Config, logger *slog.Logger, breaker *providerbreaker.Breaker, d deps) (*rundriver.Driver, error) {
	if !cfg.Orchestrator.Enabled {
		logger.Info("run orchestrator is off (GHOST_ORCHESTRATOR): opened runs are not driven")
		return nil, nil
	}
	if err := cfg.ValidateForOrchestrator(); err != nil {
		return nil, err
	}
	worker := d.worker
	if worker == nil {
		client, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		worker = client
	}
	svc, err := newOrchestrator(db, cfg, logger, providerbreaker.WorkerGuard{
		Inner: worker, Breaker: breaker, Limiter: providerbreaker.NewLimiter(cfg.ProviderRPM)})
	if err != nil {
		return nil, err
	}
	if runMode(cfg) == runs.Live {
		logger.Warn("GHOST_RUN_MODE=live: the orchestrator drives dry_run runs only; live runs are left alone")
	}
	o := cfg.Orchestrator
	logger.Info("run orchestrator on", "concurrency", o.Concurrency, "poll", o.Poll.String(), "retry", o.Retry.String(),
		"scope", o.Scope, "workspace", o.WorkspaceID, "max_attempts", o.MaxAttempts, "provider_rpm", cfg.ProviderRPM)
	return rundriver.New(rundriver.Options{DB: db, Runner: svc, Scope: o.Scope, Breaker: breaker, Concurrency: o.Concurrency, Poll: o.Poll,
		RetryDelay: o.Retry, MaxAttempts: o.MaxAttempts, Logger: logger})
}

// newOrchestrator loads the rule files and builds the orchestrator service over the guarded worker.
func newOrchestrator(db *sql.DB, cfg config.Config, logger *slog.Logger, worker orchestrator.Worker) (*orchestrator.Service, error) {
	o := cfg.Orchestrator
	rules, err := knowledge.LoadRules(o.KnowledgeRulesPath)
	if err != nil {
		return nil, fmt.Errorf("core: GHOST_KNOWLEDGE_RULES: %w", err)
	}
	routing, err := orchestrator.LoadRouting(o.RoutingPath)
	if err != nil {
		return nil, fmt.Errorf("core: GHOST_TRANSITION_ROUTING: %w", err)
	}
	signer, err := newOrchestratorSigner(cfg)
	if err != nil {
		return nil, err
	}
	svc, err := orchestrator.New(db, worker, signer, nil, orchestrator.Config{WorkspaceID: o.WorkspaceID, Knowledge: rules, Routing: routing}, logger)
	if err != nil {
		return nil, err
	}
	stages, err := newStageRecorder(db)
	if err != nil {
		return nil, err
	}
	svc.SetStages(stages) // the run's decide and evals stages, for GET /runs/{id}/progress (HAR-145)
	// Play: the Bucket 2 decision gates (D1-D3) measure each strategy set as it is published, through the same store
	// and runner the human steps use.
	gated, err := strategystore.New(db, logger)
	if err != nil {
		return nil, err
	}
	if err := attachGates(db, cfg, logger, gated); err != nil {
		return nil, err
	}
	// Bucket 1: B1 to B9 grade the episode as soon as it is published (one model call per semantic gate, through the worker).
	b1, err := newBucket1Runner(db, cfg, logger)
	if err != nil {
		return nil, err
	}
	svc.SetAfterPublish(func(ctx context.Context, runID string) {
		gated.RunGatesAsync(ctx, runID, "publish")
		b1.RunPublishAsync(ctx, runID)
	})
	return svc, nil
}

// newOrchestratorSigner mints the run tokens of the orchestrator. It uses the same key the context API verifies with;
// only the life differs: a token must outlast generation, judging and one revision (orchestrator.RequiredTokenTTL).
func newOrchestratorSigner(cfg config.Config) (*runtoken.Signer, error) {
	return runtoken.NewSigner(runTokenKey(cfg), orchestrator.RequiredTokenTTL(false)+tokenMargin)
}
