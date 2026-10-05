// Package orchestrator is the run orchestrator of the strategy generation path (HAR-117, HAR-129 sections 6-9):
// it picks up a pending dry-run AgentRun from WP8, reads the account's deal state and StateTransition at the
// replay clock, converts the knowledge that existed then into DecisionGuidance, has the worker generate three
// materially distinct candidates, evaluates each (deterministic evals in core, semantic evals through the
// worker), revises a blocking candidate at most once, ranks after the evals and publishes one StrategySet with
// its EvalBundles and a DecisionEpisode in a single transaction that moves the run to awaiting_human.
//
// Run statuses are owned here: pending -> context_built (guidance persisted) -> awaiting_human (publish). The
// run never rests in `drafted`, which is pull-accepting and dry-run recordable (contract risk R1).
//
// Failure policy (HAR-117 lead decision 2): a run is resumable in place. A transient failure (worker, provider,
// rate limit, an open breaker, an expired run token) leaves the run open, with the draft step failed and a
// recorded reason, and Run/Resume simply continues it: it never strands the account behind open_run_exists. A
// permanent failure (invalid output after one retry, no account state at the replay clock) marks the run failed
// with its reason, which frees the account.
package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
	"github.com/harneet2512/gtm-work/core-go/internal/usagestore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Worker is the model-backed worker API the orchestrator calls (workerclient.Client, optionally behind
// providerbreaker.WorkerGuard, implements it).
type Worker interface {
	Strategies(ctx context.Context, req workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error)
	Judge(ctx context.Context, req workerclient.JudgeRequest) (workerclient.JudgeResponse, error)
	Revise(ctx context.Context, req workerclient.ReviseRequest) (workerclient.ReviseResponse, error)
}

// Outcome is what a Run produced.
type Outcome struct {
	RunID       string
	SetID       string
	EpisodeID   string
	PreferredID string // candidate_id of the candidate Ghost recommends; "" when the set has no acceptable candidate
	Published   bool   // false when the run already had its set (an idempotent repeat)
}

// Service runs the orchestration. It is safe for concurrent use; concurrent calls for one run converge on one set.
type Service struct {
	db     *sql.DB
	worker Worker
	signer *runtoken.Signer
	clk    clock.Clock // wall time only (run token expiry, step timestamps); the replay clock is the trigger event's time
	cfg    Config
	log    *slog.Logger
	usage  *usagestore.Store     // what each worker call cost (HAR-145 operational metrics): a metric, never an eval
	stages *stageevents.Recorder // nil: no progress recording (HAR-145; see SetStages)
}

// New builds the service. clk nil selects the wall clock; a nil logger discards logs.
func New(db *sql.DB, worker Worker, signer *runtoken.Signer, clk clock.Clock, cfg Config, log *slog.Logger) (*Service, error) {
	switch {
	case db == nil:
		return nil, errors.New("orchestrator: a database is required")
	case worker == nil:
		return nil, errors.New("orchestrator: a worker is required")
	case signer == nil:
		return nil, errors.New("orchestrator: a run token signer is required")
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(signer.TTL()); err != nil {
		return nil, err
	}
	if clk == nil {
		clk = clock.Real{}
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	usage, err := usagestore.New(db, log)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, worker: worker, signer: signer, clk: clk, cfg: cfg, log: log, usage: usage}, nil
}

// Resume continues a run in place; it is Run (every phase is idempotent and restartable).
func (s *Service) Resume(ctx context.Context, runID string) (Outcome, error) {
	return s.Run(ctx, runID)
}

// Run advances the run as far as it can. It returns the published outcome, or an error that says whether the
// run is resumable (IsTransient: call Run again later), finished badly (IsPermanent: the run is failed and the
// account is free), or busy (ErrInProgress: another caller owns it right now).
func (s *Service) Run(ctx context.Context, runID string) (Outcome, error) {
	if !validUUID(runID) {
		return Outcome{}, fmt.Errorf("%w: %q is not a run id", ErrNotRunnable, runID)
	}
	run, err := s.loadRun(ctx, runID)
	if err != nil {
		return Outcome{}, err
	}
	if done, ok, err := s.published(ctx, runID); err != nil || ok {
		return done, err
	}
	if err := run.runnable(); err != nil {
		return Outcome{}, err
	}
	episodeID, err := s.claim(ctx, run)
	if err != nil {
		return Outcome{}, err
	}
	decide := s.beginStage(ctx, run, stageevents.Decide)
	// Every worker call of this run reports what it cost; the spend is stored whether the run publishes, pauses or fails.
	collector := &usagestore.Collector{}
	defer s.flushUsage(ctx, runID, collector)
	out, evals, err := s.execute(workerclient.WithUsageSink(ctx, collector), run, episodeID)
	if err != nil {
		err = s.settle(ctx, run, err)
		s.failStage(ctx, decide, run.ID, err)
		return Outcome{}, err
	}
	s.finishDecide(ctx, decide, run, out)
	s.finishEvals(ctx, evals, run, out)
	return out, nil
}

// flushUsage stores the usage the run's worker calls reported under the draft step (strategies, judge and revise happen
// under it). Metrics are best effort: a failure is logged and never fails the run.
func (s *Service) flushUsage(ctx context.Context, runID string, c *usagestore.Collector) {
	if err := s.usage.Flush(ctx, runID, "draft", c); err != nil {
		s.log.WarnContext(ctx, "could not store the run's model usage", "run_id", runID, "error", err)
	}
}

// execute is phases 2-5; any error it returns is classified by settle. It also returns the open evals stage
// handle (nil when recording is off, or when the evaluation was already finished by an earlier attempt's
// publish), which Run finishes once the set is published and the results are readable.
func (s *Service) execute(ctx context.Context, run runRow, episodeID string) (Outcome, *stageevents.Handle, error) {
	world, err := s.readWorld(ctx, run)
	if err != nil {
		return Outcome{}, nil, err
	}
	guidance, err := s.ensureGuidance(ctx, run, world)
	if err != nil {
		return Outcome{}, nil, err
	}
	gen, err := s.generate(ctx, run, world, guidance, episodeID)
	if err != nil {
		return Outcome{}, nil, err
	}
	out, err := s.publish(ctx, run, world, guidance, episodeID, gen)
	if err != nil {
		s.failStage(ctx, gen.evals, run.ID, err) // judged, but the results never became visible: not known to be good
		return Outcome{}, nil, err
	}
	return out, gen.evals, nil
}
