// Command core is the Ghost core service: it applies database migrations and serves the
// HTTP API (GET /healthz, POST /ingest, the account and run reads, the outbox feed, and the run-token-scoped
// GET /internal/ctx/{tool}). With GHOST_ORCHESTRATOR=on it also drives every opened AgentRun to a published
// StrategySet in the background (orchestrator.go). Configuration comes from the environment (see
// .env.example); it stops gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api"
	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/outbox"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
	"github.com/harneet2512/gtm-work/core-go/internal/transitionstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

const (
	startupTimeout  = 2 * time.Minute
	shutdownTimeout = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, logger, nil); err != nil {
		logger.Error("core stopped", "error", err)
		os.Exit(1)
	}
}

// execute loads the configuration from the environment and serves until ctx is cancelled.
func execute(ctx context.Context, logger *slog.Logger, onListen func(net.Addr)) error {
	cfg, err := loadServeConfig()
	if err != nil {
		return err
	}
	return run(ctx, cfg, logger, onListen)
}

// loadServeConfig loads the environment and applies the serve-only requirements.
func loadServeConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, err
	}
	return cfg, cfg.ValidateForServe()
}

// run opens the database, migrates it, and serves until ctx is cancelled. onListen, when
// non-nil, receives the bound address (useful with port 0).
func run(ctx context.Context, cfg config.Config, logger *slog.Logger, onListen func(net.Addr)) error {
	return runWith(ctx, cfg, logger, onListen, deps{})
}

// runWith is run with the collaborators a test may replace.
func runWith(ctx context.Context, cfg config.Config, logger *slog.Logger, onListen func(net.Addr), d deps) error {
	if err := cfg.ValidateForServe(); err != nil {
		return err
	}
	if err := cfg.ValidateWorkerBudget(effectiveLease(cfg)); err != nil {
		return err
	}
	if err := cfg.ValidateForOrchestrator(); err != nil {
		return err
	}

	startCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	db, err := store.Open(startCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	migrator, err := store.NewMigrator(db)
	if err != nil {
		return err
	}
	if err := migrator.Up(startCtx); err != nil {
		return err
	}

	gr, err := openGraph(startCtx, db, logger)
	if err != nil {
		return err
	}
	breaker := d.breaker
	if breaker == nil {
		if breaker, err = providerbreaker.New(cfg.ProviderBreakerThreshold, cfg.ProviderBreakerCooldown, nil, logger); err != nil {
			return err
		}
	}
	ingestOpts := ingest.Options{Extension: graph.NewExtension(), Debounce: cfg.CoalesceDebounce, MaxWait: cfg.CoalesceMaxWait}
	var wrapHook []func(coalesce.Hook) coalesce.Hook
	if gr != nil {
		ingestOpts.AfterAttribute = ctxgraph.IngestHook()
		wrapHook = append(wrapHook, func(h coalesce.Hook) coalesce.Hook { return ctxgraph.WithProjection(h, nil) })
	}
	svc, err := ingest.NewService(db, ingestOpts)
	if err != nil {
		return err
	}
	handler, err := newHandler(db, svc, cfg, logger, gr, breaker)
	if err != nil {
		return err
	}

	co, err := newCoalescer(db, cfg, logger, breaker, wrapHook...)
	if err != nil {
		return err
	}
	driver, err := newOrchestratorDriver(db, cfg, logger, breaker, d)
	if err != nil {
		return err
	}
	if gr != nil {
		defer gr.start(ctx, logger)()
	}
	ln, err := net.Listen("tcp", cfg.CoreAddr)
	if err != nil {
		return fmt.Errorf("core: listen on %s: %w", cfg.CoreAddr, err)
	}
	stopCoalescer := startCoalescer(ctx, co, coalescePoll(cfg), logger)
	defer stopCoalescer()
	if driver != nil {
		defer driver.Start(ctx)() // stops (and waits for the run in flight to settle) before the database closes
	}
	logger.Info("core listening", "addr", ln.Addr().String(), "run_mode", cfg.RunMode)
	if onListen != nil {
		onListen(ln.Addr())
	}

	err = api.Serve(ctx, api.NewServer(cfg.CoreAddr, handler), ln, shutdownTimeout)
	logger.Info("core stopped")
	return err
}

// workerTimeoutOptions applies the call timeout (the configured one, else the deadline plus margin).
func workerTimeoutOptions(cfg config.Config) []workerclient.Option {
	_, timeout := cfg.EffectiveWorkerBudget()
	return []workerclient.Option{workerclient.WithTimeout(timeout)}
}

// effectiveLease is the job lease the coalescer will use: the configured one, else its default.
func effectiveLease(cfg config.Config) time.Duration {
	if cfg.CoalesceLease > 0 {
		return cfg.CoalesceLease
	}
	return coalesce.DefaultLease
}

// newHandler builds the HTTP API: ingest, the account/run read endpoints and the run-token-scoped
// context pulls. The run token key is GHOST_RUN_TOKEN_SECRET, or derived from the API token.
func newHandler(db *sql.DB, svc *ingest.Service, cfg config.Config, logger *slog.Logger, gr *graphRuntime, breaker *providerbreaker.Breaker) (http.Handler, error) {
	reader, err := readmodel.New(db)
	if err != nil {
		return nil, err
	}
	strategyOpts := []strategystore.Option{strategystore.WithEvalParams(
		evalinput.Params{WorkspaceID: cfg.Orchestrator.WorkspaceID})}
	if cfg.WorkerURL != "" {
		// The delta labeler (POST /v1/human-delta) is the worker; without a URL every delta falls back
		// to core's unlabeled form (HAR-139).
		labeler, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		strategyOpts = append(strategyOpts, strategystore.WithLabeler(labeler))
	}
	strategies, err := strategystore.New(db, logger, strategyOpts...)
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
	var pullOpts []corectx.Option
	apiOpts := []api.Option{api.WithReads(reader), api.WithStrategy(strategies), api.WithOutbox(events), api.WithSurfaceMessages(refs), api.WithReplay(replay)}
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
	return api.NewHandler(svc, cfg.APIToken, logger, append(apiOpts, api.WithContext(pulls, signer), api.WithBreaker(breaker))...)
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

// newCoalescer builds the recompute coalescer. It extracts through the model worker at cfg.WorkerURL;
// with no worker URL it still runs the deterministic rule extractors (CRM, calendar, enrichment).
func newCoalescer(db *sql.DB, cfg config.Config, logger *slog.Logger, breaker *providerbreaker.Breaker, wrap ...func(coalesce.Hook) coalesce.Hook) (*coalesce.Service, error) {
	var extractor claims.Extractor
	if cfg.WorkerURL != "" {
		client, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		extractor = providerbreaker.Guard{Inner: client, Breaker: breaker} // one process-wide breaker for every model call
	} else {
		logger.Warn("WORKER_URL is empty: recomputes use rule extractors only, free text is not extracted")
	}
	if cfg.RunMode == runs.Live && !cfg.AllowExternalWrites {
		logger.Warn("GHOST_RUN_MODE=live without GHOST_ALLOW_EXTERNAL_WRITES=true: runs stay dry_run")
	}
	hook, err := pipeline.New(pipeline.Options{RunMode: runMode(cfg)})
	if err != nil {
		return nil, err
	}
	detector, err := newDetector(cfg, logger)
	if err != nil {
		return nil, err
	}
	var h coalesce.Hook = hook
	for _, w := range wrap {
		h = w(h)
	}
	return coalesce.New(db, coalesce.Options{Extractor: extractor, Lease: cfg.CoalesceLease, Breaker: breaker, Hook: h, Detector: detector, Logger: logger})
}

// newDetector loads the transition rule set named by GHOST_TRANSITION_RULES (ADR-0012). With no path set
// the detector is off and the account never leaves relationship state "unknown": a missing rule set is
// said out loud, never silently replaced by a default.
func newDetector(cfg config.Config, logger *slog.Logger) (coalesce.Detector, error) {
	if cfg.TransitionRulesPath == "" {
		logger.Warn("GHOST_TRANSITION_RULES is empty: the transition detector is off, no StateTransition will be recorded")
		return nil, nil
	}
	rules, err := transitions.LoadRules(cfg.TransitionRulesPath)
	if err != nil {
		return nil, fmt.Errorf("core: transition rules: %w", err)
	}
	logger.Info("transition detector on", "rule_set", rules.Version)
	return transitionstore.NewDetector(rules), nil
}

// runMode is the mode of the runs the pipeline creates: live only when both the live mode and the
// explicit write switch are set (config.ExternalWritesEnabled); everything else is structurally dry-run.
func runMode(cfg config.Config) string {
	if cfg.ExternalWritesEnabled() {
		return runs.Live
	}
	return runs.DryRun
}

// coalescePoll is how often the coalescer looks for due recompute jobs: half the debounce, kept
// between 50 ms and 1 s.
func coalescePoll(cfg config.Config) time.Duration {
	poll := cfg.CoalesceDebounce / 2
	return min(max(poll, 50*time.Millisecond), time.Second)
}

// startCoalescer runs the coalescer until ctx is cancelled; the returned function cancels it and
// waits for it to finish, so the database is never closed under a running recompute.
func startCoalescer(ctx context.Context, co *coalesce.Service, poll time.Duration, logger *slog.Logger) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := co.Run(ctx, poll); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("coalescer stopped", "error", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
