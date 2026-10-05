package rundriver_test

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/rundriver"
)

// markAttempted gives the run a draft step that already has an attempt, which is what a crash leaves behind.
func markAttempted(t *testing.T, runID string) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status, detail)
 VALUES ($1::uuid, 1, 'draft', 'dry_run', 'failed', '{"attempt":1}')`, runID); err != nil {
		t.Fatal(err)
	}
}

func scoped(scope string) func(*rundriver.Options) {
	return func(o *rundriver.Options) { o.Scope = scope }
}

func driveFor(t *testing.T, f *fakeRunner, scope string, until func() bool) {
	t.Helper()
	stop := newDriver(t, f, scoped(scope)).Start(bg)
	defer stop()
	waitFor(t, "the expected runs to be driven", until)
	time.Sleep(200 * time.Millisecond) // many more passes: a run that should not be driven would have been by now
}

func TestPlayScopeDrivesOnlyThePlaysRunAndNeverAHistoricalPlaceholderRun(t *testing.T) {
	ids := openRuns(t, "pending", "pending") // [0] a historical placeholder run, [1] the play's run
	ctxfixture.MarkRunAsPlay(t, env.DB, ids[1])
	f := &fakeRunner{}
	driveFor(t, f, rundriver.ScopePlay, func() bool { return status(t, ids[1]) == "awaiting_human" })
	if n := f.callCount(ids[0]); n != 0 {
		t.Fatalf("the historical run was driven %d times; under the play scope it never is", n)
	}
	if status(t, ids[0]) != "pending" {
		t.Fatalf("the historical run moved to %s", status(t, ids[0]))
	}
	if n := f.callCount(ids[1]); n != 1 {
		t.Fatalf("the play's run was driven %d times, want once", n)
	}
}

func TestPlayScopeIsTheDefaultWhenNoScopeIsGiven(t *testing.T) {
	openRuns(t, "pending")
	f := &fakeRunner{}
	stop := newDriver(t, f, scoped("")).Start(bg)
	time.Sleep(250 * time.Millisecond)
	stop()
	if f.total() != 0 {
		t.Fatalf("an unscoped driver drove %d historical runs", f.total())
	}
}

func TestPlayScopeStillResumesARunItAlreadyAttemptedAfterACrash(t *testing.T) {
	ids := openRuns(t, "context_built", "pending") // [0] interrupted mid-generation, [1] untouched placeholder
	markAttempted(t, ids[0])
	f := &fakeRunner{}
	driveFor(t, f, rundriver.ScopePlay, func() bool { return status(t, ids[0]) == "awaiting_human" })
	if n := f.callCount(ids[1]); n != 0 {
		t.Fatalf("the untouched placeholder run was driven %d times", n)
	}
}

func TestAllScopeDrivesEveryOpenRunLikeBefore(t *testing.T) {
	ids := openRuns(t, "pending", "pending")
	f := &fakeRunner{}
	driveFor(t, f, rundriver.ScopeAll, func() bool { return status(t, ids[0]) == "awaiting_human" && status(t, ids[1]) == "awaiting_human" })
}

func TestAnUnknownScopeIsRefused(t *testing.T) {
	if _, err := rundriver.New(rundriver.Options{DB: env.DB, Runner: &fakeRunner{}, Scope: "everything"}); err == nil {
		t.Fatal("an unknown scope was accepted")
	}
}
