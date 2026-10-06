package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/outbox"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// newHandler builds the HTTP API: ingest, the account/run read endpoints and the run-token-scoped
// context pulls. The run token key is GHOST_RUN_TOKEN_SECRET, or derived from the API token.
func newHandler(db *sql.DB, svc *ingest.Service, cfg config.Config, logger *slog.Logger, gr *graphRuntime, breaker *providerbreaker.Breaker, sup *reactions.Service) (http.Handler, error) {
	reader, err := readmodel.New(db)
	if err != nil {
		return nil, err
	}
	strategyOpts := []strategystore.Option{strategystore.WithEvalParams(
		evalinput.Params{WorkspaceID: cfg.Orchestrator.WorkspaceID})}
	if cfg.Orchestrator.KnowledgeRulesPath != "" {
		// The learning loop's lifecycle rules (HAR-119): candidate seeds record their source episode as
		// evidence, so a promotion gate can read how the row earned its status. The configured default
		// is repo-root-relative: an explicitly set GHOST_KNOWLEDGE_RULES that fails is a hard error,
		// but a missing default only degrades the seeds (strategystore treats nil rules as "no
		// episode evidence write") so core still boots away from the repo root.
		rules, err := knowledge.LoadRules(cfg.Orchestrator.KnowledgeRulesPath)
		if err != nil {
			if _, set := os.LookupEnv("GHOST_KNOWLEDGE_RULES"); set {
				return nil, fmt.Errorf("load knowledge rules: %w", err)
			}
			logger.Warn("knowledge rules unavailable; learned candidates seed without episode evidence",
				"path", cfg.Orchestrator.KnowledgeRulesPath, "err", err)
		} else {
			strategyOpts = append(strategyOpts, strategystore.WithKnowledgeRules(rules))
		}
	}
	if cfg.WorkerURL != "" {
		// The delta labeler (POST /v1/human-delta) is the worker; without a URL every delta falls back
		// to core's unlabeled form (HAR-139).
		labeler, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		strategyOpts = append(strategyOpts, strategystore.WithLabeler(labeler), strategystore.WithInferrer(labeler))
	}
	strategies, err := strategystore.New(db, logger, strategyOpts...)
	if err != nil {
		return nil, err
	}
	if err := attachGates(db, cfg, logger, strategies); err != nil {
		return nil, err
	}
	gateReader, err := bucket2.NewReader(db)
	if err != nil {
		return nil, err
	}
	knowledge, err := knowledgestore.New(db)
	if err != nil {
		return nil, err
	}
	events, err := outbox.New(db)
	if err != nil {
		return nil, err
	}
	refs, err := surfacemsg.New(db)
	if err != nil {
		return nil, err
	}
	replay, err := newReplay(db, svc, cfg, logger, gr)
	if err != nil {
		return nil, err
	}
	disputes, err := evaldispute.New(db, logger)
	if err != nil {
		return nil, err
	}
	control, err := controlplane.New(db)
	if err != nil {
		return nil, err
	}
	stages, err := newStageRecorder(db)
	if err != nil {
		return nil, err
	}
	progress, err := stageevents.NewReader(db, nil)
	if err != nil {
		return nil, err
	}
	recomputation, err := recompute.New(db, nil)
	if err != nil {
		return nil, err
	}
	var pullOpts []corectx.Option
	// The Slack message refs are observed so the Cliff stage follows what was really reserved and posted (HAR-145).
	observed := stageevents.ObservedSurfaceMessages{Inner: refs, Rec: stages}
	apiOpts := []api.Option{api.WithReads(reader), api.WithStrategy(strategies), api.WithKnowledge(knowledge), api.WithOutbox(events), api.WithSurfaceMessages(observed), api.WithReplay(replay), api.WithEvalDisputes(disputes), api.WithProgress(progress), api.WithRecomputation(recomputation), api.WithControlPlane(control), api.WithGateResults(gateReader)}
	if sup != nil {
		apiOpts = append(apiOpts, api.WithSupervision(sup))
	}
	if gr != nil {
		// The graph tool rebuilds the world graph from Postgres at the run's cutoff, so no pull waits for the projection.
		pullOpts = append(pullOpts, corectx.WithGraph(gr.reader))
		apiOpts = append(apiOpts, api.WithGraph(gr.service))
	}
	pulls, err := corectx.New(db, pullOpts...)
	if err != nil {
		return nil, err
	}
	signer, err := newRunSigner(cfg)
	if err != nil {
		return nil, err
	}
	apiOpts = append(apiOpts, api.WithContext(pulls, signer), api.WithBreaker(breaker))
	askW, err := newAskWiring(cfg, os.Getenv, logger, db)
	if err != nil {
		return nil, err
	}
	if askW.option != nil {
		apiOpts = append(apiOpts, askW.option)
	}
	handler, err := api.NewHandler(svc, cfg.APIToken, logger, apiOpts...)
	if err != nil {
		return nil, err
	}
	if askW.loopback != nil {
		askW.loopback.Bind(handler) // Ask Cliff's tools and actions call this same router, in process
	}
	return handler, nil
}

// newRunSigner builds the run token signer of cfg.
func newRunSigner(cfg config.Config) (*runtoken.Signer, error) {
	return runtoken.NewSigner(runTokenKey(cfg), runtoken.DefaultTTL)
}

// runTokenKey is the signing key shared by every run-token signer of the process.
func runTokenKey(cfg config.Config) []byte {
	if cfg.RunTokenSecret != "" {
		return []byte(cfg.RunTokenSecret)
	}
	return runtoken.DeriveKey(cfg.APIToken)
}
