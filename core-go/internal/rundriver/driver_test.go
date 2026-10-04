package rundriver_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/orchestrator"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/rundriver"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) { os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e })) }

var bg = context.Background()

// fakeRunner stands in for the orchestrator: it records calls and, by default, "publishes" by moving the run on.
type fakeRunner struct {
	mu          sync.Mutex
	calls       []string
	inflight    int
	maxInflight int
	fn          func(ctx context.Context, runID string) (orchestrator.Outcome, error)
}

func (f *fakeRunner) Run(ctx context.Context, runID string) (orchestrator.Outcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, runID)
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	fn := f.fn
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if fn != nil {
		return fn(ctx, runID)
	}
	return publish(runID)
}

func publish(runID string) (orchestrator.Outcome, error) {
	if err := ctxfixture.SetStatus(bg, env.DB, runID, "awaiting_human"); err != nil {
		return orchestrator.Outcome{}, err
	}
	return orchestrator.Outcome{RunID: runID, Published: true}, nil
}

func (f *fakeRunner) callCount(runID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == runID {
			n++
		}
	}
	return n
}

func (f *fakeRunner) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// clean cancels every open run, so a test sees only the runs it makes.
func clean(t *testing.T) {
	t.Helper()
	if _, err := env.DB.Exec(`DELETE FROM demo_plays`); err != nil { // a play row would make a later run "the play's run"
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`UPDATE agent_runs SET status = 'cancelled' WHERE status IN ('pending', 'context_built', 'drafted', 'awaiting_human')`); err != nil {
		t.Fatal(err)
	}
}

// openRuns makes one run of the given status for each of the two sample accounts.
func openRuns(t *testing.T, statuses ...string) []string {
	t.Helper()
	clean(t)
	w := ctxfixture.Get(t, env.DB)
	accounts := []string{w.AccountA, w.AccountB}
	var ids []string
	for i, s := range statuses {
		ids = append(ids, ctxfixture.FreshRun(t, env.DB, accounts[i], s))
	}
	return ids
}

func status(t *testing.T, runID string) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(`SELECT status FROM agent_runs WHERE id = $1::uuid`, runID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newDriver(t *testing.T, r rundriver.Runner, adjust func(*rundriver.Options)) *rundriver.Driver {
	t.Helper()
	opt := rundriver.Options{DB: env.DB, Runner: r, Scope: rundriver.ScopeAll, Poll: 10 * time.Millisecond, RetryDelay: time.Minute, Clock: clock.NewFixed(time.Now())}
	if adjust != nil {
		adjust(&opt)
	}
	d, err := rundriver.New(opt)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNewValidatesItsOptions(t *testing.T) {
	good := rundriver.Options{DB: env.DB, Runner: &fakeRunner{}}
	for name, mutate := range map[string]func(*rundriver.Options){
		"no database":           func(o *rundriver.Options) { o.DB = nil },
		"no runner":             func(o *rundriver.Options) { o.Runner = nil },
		"negative concurrency":  func(o *rundriver.Options) { o.Concurrency = -1 },
		"negative max attempts": func(o *rundriver.Options) { o.MaxAttempts = -1 },
		"negative poll":         func(o *rundriver.Options) { o.Poll = -1 },
		"negative retry":        func(o *rundriver.Options) { o.RetryDelay = -1 },
	} {
		o := good
		mutate(&o)
		if _, err := rundriver.New(o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := rundriver.New(good); err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
}

func TestItResumesPendingAndContextBuiltRunsAtStartup(t *testing.T) {
	ids := openRuns(t, "pending", "context_built")
	f := &fakeRunner{}
	stop := newDriver(t, f, nil).Start(bg)
	defer stop()
	waitFor(t, "both runs driven", func() bool { return status(t, ids[0]) == "awaiting_human" && status(t, ids[1]) == "awaiting_human" })
	stop()
	for _, id := range ids {
		if n := f.callCount(id); n != 1 {
			t.Errorf("run %s driven %d times, want exactly once", id, n)
		}
	}
}

func TestItLeavesLiveAndAlreadyPublishedRunsAlone(t *testing.T) {
	ids := openRuns(t, "awaiting_human", "pending") // published already; and a pending run turned live (no steps yet)
	if _, err := env.DB.Exec(`UPDATE agent_runs SET run_mode = 'live' WHERE id = $1::uuid`, ids[1]); err != nil {
		t.Fatalf("make the run live: %v", err)
	}
	f := &fakeRunner{}
	stop := newDriver(t, f, nil).Start(bg)
	time.Sleep(200 * time.Millisecond)
	stop()
	if f.total() != 0 {
		t.Fatalf("the driver called the orchestrator %d times for runs that are not its to drive", f.total())
	}
}

func TestOneCallAtATimeByDefaultAndTheConfiguredBoundOtherwise(t *testing.T) {
	for _, c := range []struct{ concurrency, wantMax int }{{0, 1}, {1, 1}, {2, 2}} {
		t.Run(fmt.Sprintf("concurrency %d", c.concurrency), func(t *testing.T) {
			ids := openRuns(t, "pending", "pending")
			release := make(chan struct{})
			f := &fakeRunner{}
			f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return publish(runID)
			}
			stop := newDriver(t, f, func(o *rundriver.Options) { o.Concurrency = c.concurrency }).Start(bg)
			defer stop()
			waitFor(t, "the first calls to start", func() bool {
				f.mu.Lock()
				defer f.mu.Unlock()
				return f.inflight >= 1
			})
			time.Sleep(150 * time.Millisecond) // a second call would have started by now if it were allowed
			f.mu.Lock()
			inflight := f.inflight
			f.mu.Unlock()
			if inflight != c.wantMax {
				t.Fatalf("in flight = %d, want %d", inflight, c.wantMax)
			}
			close(release)
			waitFor(t, "both runs published", func() bool { return status(t, ids[0]) == "awaiting_human" && status(t, ids[1]) == "awaiting_human" })
			stop()
			if f.maxInflight != c.wantMax {
				t.Fatalf("max in flight = %d, want %d", f.maxInflight, c.wantMax)
			}
		})
	}
}

func TestAnOpenBreakerMeansNoCallsAndTheRunStaysResumable(t *testing.T) {
	ids := openRuns(t, "context_built")
	breaker, err := providerbreaker.New(1, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	breaker.Failure("worker 424 provider_unavailable_nonretryable")
	if !breaker.Open() {
		t.Fatal("the breaker did not open")
	}
	f := &fakeRunner{}
	stop := newDriver(t, f, func(o *rundriver.Options) { o.Breaker = breaker }).Start(bg)
	defer stop()
	time.Sleep(300 * time.Millisecond) // many passes
	if f.total() != 0 || status(t, ids[0]) != "context_built" {
		t.Fatalf("calls = %d, status = %s: an open breaker must mean no call and an untouched run", f.total(), status(t, ids[0]))
	}
	breaker.Reset()
	waitFor(t, "the run to complete once the breaker closed", func() bool { return status(t, ids[0]) == "awaiting_human" })
}

func TestATransientFailureBacksTheRunOffUntilTheRetryDelayPasses(t *testing.T) {
	ids := openRuns(t, "context_built")
	clk := clock.NewFixed(time.Now())
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		if f.callCount(runID) == 1 {
			return orchestrator.Outcome{}, &orchestrator.TransientError{Phase: "generate", Reason: "worker unreachable"}
		}
		return publish(runID)
	}
	stop := newDriver(t, f, func(o *rundriver.Options) { o.Clock = clk; o.RetryDelay = time.Minute }).Start(bg)
	defer stop()
	waitFor(t, "the first attempt", func() bool { return f.callCount(ids[0]) == 1 })
	time.Sleep(200 * time.Millisecond)
	if f.callCount(ids[0]) != 1 {
		t.Fatalf("the run was retried %d times inside its backoff", f.callCount(ids[0]))
	}
	clk.Advance(61 * time.Second)
	waitFor(t, "the resumed run to complete", func() bool { return status(t, ids[0]) == "awaiting_human" })
	if n := f.callCount(ids[0]); n != 2 {
		t.Fatalf("calls = %d, want 2", n)
	}
}

func TestAPermanentFailureIsNotRetriedBecauseTheRunLeavesTheOpenStatuses(t *testing.T) {
	ids := openRuns(t, "pending")
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		_ = ctxfixture.SetStatus(bg, env.DB, runID, "failed") // what the orchestrator does before returning a PermanentError
		return orchestrator.Outcome{}, &orchestrator.PermanentError{Phase: "generate", Reason: "invalid output"}
	}
	stop := newDriver(t, f, nil).Start(bg)
	waitFor(t, "the failure", func() bool { return status(t, ids[0]) == "failed" })
	time.Sleep(150 * time.Millisecond)
	stop()
	if n := f.callCount(ids[0]); n != 1 {
		t.Fatalf("a failed run was driven %d times", n)
	}
}

func TestARunAnotherCallerOwnsIsBackedOffAndANonRunnableRunIsNotAnError(t *testing.T) {
	ids := openRuns(t, "pending", "pending")
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		if runID == ids[0] {
			return orchestrator.Outcome{}, fmt.Errorf("%w: run %s", orchestrator.ErrInProgress, runID)
		}
		return orchestrator.Outcome{}, fmt.Errorf("%w: run %s", orchestrator.ErrNotRunnable, runID)
	}
	stop := newDriver(t, f, func(o *rundriver.Options) { o.Concurrency = 2 }).Start(bg)
	defer stop()
	waitFor(t, "both attempts", func() bool { return f.callCount(ids[0]) >= 1 && f.callCount(ids[1]) >= 1 })
	time.Sleep(200 * time.Millisecond)
	// ErrInProgress backs off for the (frozen-clock) retry delay; ErrNotRunnable has no backoff and is simply listed again
	if f.callCount(ids[0]) != 1 {
		t.Fatalf("an owned run was retried %d times inside its backoff", f.callCount(ids[0]))
	}
}

func TestShutdownStopsCleanlyCancelsTheCallInFlightAndStartsNothingNew(t *testing.T) {
	ids := openRuns(t, "pending", "pending")
	started := make(chan struct{}, 2)
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		started <- struct{}{}
		<-ctx.Done()
		return orchestrator.Outcome{}, &orchestrator.TransientError{Phase: "generate", Reason: "context canceled", Err: ctx.Err()}
	}
	d := newDriver(t, f, nil)
	ctx, cancel := context.WithCancel(bg)
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v on shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the driver did not stop")
	}
	if f.total() != 1 {
		t.Fatalf("calls = %d: nothing new may start after the shutdown, and Concurrency 1 starts one", f.total())
	}
	for _, id := range ids {
		if s := status(t, id); s != "pending" {
			t.Fatalf("run %s is %s after shutdown, want it left open", id, s)
		}
	}
}

func TestStartupReleasesTheClaimsAPreviousProcessLeftBehind(t *testing.T) {
	ids := openRuns(t, "context_built")
	insertSteps(t, ids[0])
	if _, err := env.DB.Exec(`UPDATE agent_run_steps SET status = 'running', started_at = now(), detail = '{"attempt":1}'
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, ids[0]); err != nil {
		t.Fatal(err)
	}
	called := make(chan struct{}, 1)
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		// by now the orphaned claim must have been released, or the orchestrator would answer ErrInProgress
		var step, reason string
		if err := env.DB.QueryRow(`SELECT status, detail #>> '{error,reason}' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID).Scan(&step, &reason); err != nil {
			return orchestrator.Outcome{}, err
		}
		if step != "failed" || reason == "" {
			return orchestrator.Outcome{}, fmt.Errorf("draft step is %q (%q) when the run was handed over", step, reason)
		}
		called <- struct{}{}
		return publish(runID)
	}
	stop := newDriver(t, f, nil).Start(bg)
	defer stop()
	select {
	case <-called:
	case <-time.After(15 * time.Second):
		t.Fatal("the orphaned run was never handed to the orchestrator")
	}
	waitFor(t, "the run to complete", func() bool { return status(t, ids[0]) == "awaiting_human" })
}

func TestASecondDriverOnTheSameDatabaseStandsBy(t *testing.T) {
	ids := openRuns(t, "context_built", "context_built")
	first := &fakeRunner{}
	first.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		<-ctx.Done() // hold the run (and the single-driver lock) until this driver is stopped
		return orchestrator.Outcome{}, &orchestrator.TransientError{Phase: "generate", Reason: "shutdown", Err: ctx.Err()}
	}
	stop1 := newDriver(t, first, nil).Start(bg)
	waitFor(t, "the first driver to start its run", func() bool { return first.total() >= 1 })

	second := &fakeRunner{}
	stop2 := newDriver(t, second, nil).Start(bg)
	defer stop2()
	time.Sleep(250 * time.Millisecond) // many passes: the second driver must never start a run
	if second.total() != 0 {
		t.Fatalf("a second driver on the same database drove %d runs while the first held the lock", second.total())
	}

	stop1() // releases the single-driver lock; the run in flight stays resumable
	waitFor(t, "the second driver to take over", func() bool { return second.total() >= 1 })
	waitFor(t, "both runs to publish", func() bool {
		return status(t, ids[0]) == "awaiting_human" && status(t, ids[1]) == "awaiting_human"
	})
}

// bumpAttempt mimics the orchestrator's claim: it increments the run's draft-step attempt counter, which is
// how the driver knows how many times a run has already been tried.
func bumpAttempt(db *sql.DB, runID string) error {
	_, err := db.Exec(`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status, detail)
  VALUES ($1::uuid, 1, 'draft', 'dry_run', 'failed', '{"attempt":1}')
  ON CONFLICT (agent_run_id, seq) DO UPDATE SET status = 'failed',
    detail = agent_run_steps.detail || jsonb_build_object('attempt', COALESCE((agent_run_steps.detail ->> 'attempt')::int, 0) + 1)`, runID)
	return err
}

func TestARunIsFailedPermanentlyAfterItsRetryBudget(t *testing.T) {
	ids := openRuns(t, "context_built")
	f := &fakeRunner{}
	f.fn = func(ctx context.Context, runID string) (orchestrator.Outcome, error) {
		if err := bumpAttempt(env.DB, runID); err != nil { // the orchestrator's claim would do this before the model call
			return orchestrator.Outcome{}, err
		}
		return orchestrator.Outcome{}, &orchestrator.TransientError{Phase: "generate", Reason: "worker unreachable"}
	}
	stop := newDriver(t, f, func(o *rundriver.Options) {
		o.MaxAttempts, o.RetryDelay, o.Clock = 3, time.Millisecond, nil // a real clock: the backoff must actually elapse
	}).Start(bg)
	defer stop()
	waitFor(t, "the run to fail permanently", func() bool { return status(t, ids[0]) == "failed" })
	time.Sleep(150 * time.Millisecond) // more passes: a run past its budget is never handed over again
	stop()
	if n := f.callCount(ids[0]); n != 3 {
		t.Fatalf("the run was handed over %d times, want exactly its budget of 3", n)
	}
	var kind, reason string
	if err := env.DB.QueryRow(`SELECT detail #>> '{error,kind}', detail #>> '{error,reason}'
  FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, ids[0]).Scan(&kind, &reason); err != nil {
		t.Fatal(err)
	}
	if kind != "permanent" || reason == "" {
		t.Fatalf("last failure kind=%q reason=%q, want permanent with a reason", kind, reason)
	}
}

func TestRunReturnsAnErrorWhenItCannotReleaseTheClaims(t *testing.T) {
	db, err := sql.Open("pgx", env.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close() // a closed pool: every statement fails
	d, err := rundriver.New(rundriver.Options{DB: db, Runner: &fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Run(bg); err == nil {
		t.Fatal("a database that cannot be reached must be reported, not swallowed")
	}
}

func TestWakeNeverBlocks(t *testing.T) {
	d := newDriver(t, &fakeRunner{}, nil)
	for i := 0; i < 5; i++ {
		d.Wake() // the channel holds one; the rest are dropped
	}
}

func insertSteps(t *testing.T, runID string) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status)
 SELECT $1::uuid, ord, step, 'dry_run', 'pending' FROM unnest(ARRAY['build_context','draft','crm_intent','await_human','execute']) WITH ORDINALITY AS u(step, ord)`, runID); err != nil {
		t.Fatal(err)
	}
}
