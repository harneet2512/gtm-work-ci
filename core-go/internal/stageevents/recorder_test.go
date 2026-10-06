package stageevents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var bg = context.Background()

// play is a run that came from a Play: the run, its account, and the manifest and activity of the play.
type play struct{ run, account, manifest, activity string }

// newPlay marks a fresh run of the sample world as the run of a demo play.
func newPlay(t *testing.T) play {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	p := play{run: ctxfixture.FreshRun(t, env.DB, world.AccountA, "context_built"), account: world.AccountA}
	ctxfixture.MarkRunAsPlay(t, env.DB, p.run)
	if err := env.DB.QueryRow(`SELECT manifest_id::text, activity_id::text FROM demo_plays WHERE account_id = $1::uuid ORDER BY started_at DESC LIMIT 1`,
		p.account).Scan(&p.manifest, &p.activity); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = env.DB.Exec(`DELETE FROM pipeline_stage_events WHERE account_id = $1::uuid`, p.account)
		_, _ = env.DB.Exec(`DELETE FROM demo_plays WHERE manifest_id = $1::uuid`, p.manifest)
	})
	return p
}

func (p play) manifestScope() Scope { return Scope{ManifestID: p.manifest, AccountID: p.account} }
func (p play) runScope() Scope      { return Scope{RunID: p.run, AccountID: p.account} }

type row struct {
	status, kind, detail string
	attempt              int
	ended                bool
	ids                  string
}

func (p play) row(t *testing.T, scope Scope, stage Stage) (row, bool) {
	t.Helper()
	var r row
	var ended sql.NullTime
	var kind, detail sql.NullString
	q := `SELECT status, attempt, ended_at, failure_kind, detail, eval_result_ids::text FROM pipeline_stage_events WHERE stage = $1 AND `
	arg := scope.ManifestID
	if scope.RunID != "" {
		q, arg = q+`run_id = $2::uuid`, scope.RunID
	} else {
		q += `manifest_id = $2::uuid AND run_id IS NULL`
	}
	err := env.DB.QueryRow(q, string(stage), arg).Scan(&r.status, &r.attempt, &ended, &kind, &detail, &r.ids)
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	r.ended, r.kind, r.detail = ended.Valid, kind.String, detail.String
	return r, true
}

func newRecorder(t *testing.T) (*Recorder, *clock.Fixed) {
	t.Helper()
	clk := clock.NewFixed(time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC))
	r, err := NewRecorder(env.DB, clk)
	if err != nil {
		t.Fatal(err)
	}
	return r, clk
}

func TestBeginIsVisibleBeforeTheStageEnds(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	if _, err := rec.Begin(bg, p.manifestScope(), Ingest); err != nil {
		t.Fatal(err)
	}
	got, ok := p.row(t, p.manifestScope(), Ingest)
	if !ok || got.status != "running" || got.ended || got.attempt != 1 {
		t.Fatalf("a started stage is committed as running with no end: %+v ok=%v", got, ok)
	}
	if _, found := p.row(t, p.manifestScope(), Resolve); found {
		t.Fatal("a stage that has not started has no row (it is waiting, not placeholder-written)")
	}
}

func TestFinishRecordsTheEndAndTheProducedRows(t *testing.T) {
	p := newPlay(t)
	rec, clk := newRecorder(t)
	h, err := rec.Begin(bg, p.manifestScope(), State)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(1500 * time.Millisecond)
	if err := h.Finish(bg, Completed, Outcome{Refs: Refs{StateVersion: 7, StateDiffID: p.activity}, Detail: "state v7"}); err != nil {
		t.Fatal(err)
	}
	var started, ended time.Time
	var refs string
	if err := env.DB.QueryRow(`SELECT started_at, ended_at, refs::text FROM pipeline_stage_events WHERE manifest_id = $1::uuid AND stage = 'state'`,
		p.manifest).Scan(&started, &ended, &refs); err != nil {
		t.Fatal(err)
	}
	if d := ended.Sub(started); d != 1500*time.Millisecond {
		t.Errorf("duration %v, want the clock's 1.5s (timestamps come from the stage's own start and end)", d)
	}
	if want := fmt.Sprintf(`{"state_diff_id": "%s", "state_version": 7}`, p.activity); refs != want {
		t.Errorf("refs = %s, want %s", refs, want)
	}
}

func TestAFailedStageNamesTheKindAndLaterStagesStayWaiting(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	h, _ := rec.Begin(bg, p.manifestScope(), Graph)
	if err := h.Fail(bg, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	got, _ := p.row(t, p.manifestScope(), Graph)
	if got.status != "failed" || got.kind != "transport" || !got.ended {
		t.Fatalf("a timed-out graph stage: %+v", got)
	}
	for _, later := range []Stage{State, Decide, Evals, Cliff} {
		if _, found := p.row(t, p.manifestScope(), later); found {
			t.Errorf("%s has a row though it never ran", later)
		}
	}
}

func TestATransportErrorWhileJudgingIsUnknownNeverAnEvalFail(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	h, _ := rec.Begin(bg, p.runScope(), Evals)
	if err := h.Fail(bg, &net.OpError{Op: "dial", Err: errors.New("connection refused")}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.row(t, p.runScope(), Evals)
	if got.status != "unknown" || got.kind != "transport" {
		t.Fatalf("a transport error while judging: %+v, want unknown/transport", got)
	}
}

func TestEvalsFailsOnlyAsAJudgmentWithItsResults(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	h, _ := rec.Begin(bg, p.runScope(), Evals)
	if err := h.Finish(bg, Failed, Outcome{}); err == nil {
		t.Fatal("evals failed with no failing results: that is an error, not a judgment")
	}
	id := "0e1a0000-0000-4000-8000-000000000911"
	if err := h.Finish(bg, Failed, Outcome{EvalResultIDs: []string{id}, Detail: "1 blocking fail"}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.row(t, p.runScope(), Evals)
	if got.status != "failed" || got.kind != "" || got.ids != "{"+id+"}" {
		t.Fatalf("a judged failure: %+v", got)
	}
}

func TestAResumedStageBumpsItsAttemptAndAPassedStageIsNeverRewritten(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	h, _ := rec.Begin(bg, p.manifestScope(), Ingest)
	_ = h.Fail(bg, errors.New("boom"))
	again, err := rec.Begin(bg, p.manifestScope(), Ingest)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.row(t, p.manifestScope(), Ingest); got.status != "running" || got.attempt != 2 || got.kind != "" || got.ended {
		t.Fatalf("a re-run of a failed stage: %+v, want running, attempt 2, failure cleared", got)
	}
	if err := again.Finish(bg, Completed, Outcome{Detail: "ok"}); err != nil {
		t.Fatal(err)
	}

	noop, err := rec.Begin(bg, p.manifestScope(), Ingest) // an idempotent replay re-runs ingest
	if err != nil {
		t.Fatal(err)
	}
	if err := noop.Finish(bg, Failed, Outcome{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.row(t, p.manifestScope(), Ingest); got.status != "completed" || got.attempt != 2 || got.detail != "ok" {
		t.Fatalf("a passed stage was rewritten by a replay: %+v", got)
	}
}

func TestRecordWritesAFinishedStageInOneStep(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	if err := rec.Record(bg, p.manifestScope(), Decide, Skipped, Outcome{Detail: "no_material_change"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.row(t, p.manifestScope(), Decide); got.status != "skipped" || !got.ended || got.detail != "no_material_change" {
		t.Fatalf("a skipped stage: %+v", got)
	}
}

func TestMergeRefsAttachesLateRowsToAFinishedStageOnly(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	change := "0acc0000-0000-4000-8000-000000000801"
	if err := rec.MergeRefs(bg, p.manifestScope(), Resolve, Refs{AccountChangeID: change}); err == nil {
		t.Fatal("refs were attached to a stage that never ran")
	}
	h, _ := rec.Begin(bg, p.manifestScope(), Resolve)
	if err := rec.MergeRefs(bg, p.manifestScope(), Resolve, Refs{AccountChangeID: change}); err == nil {
		t.Fatal("refs were attached to a stage that is still running")
	}
	_ = h.Finish(bg, Completed, Outcome{Refs: Refs{ActivityID: p.activity}})
	if err := rec.MergeRefs(bg, p.manifestScope(), Resolve, Refs{AccountChangeID: change}); err != nil {
		t.Fatal(err)
	}
	var refs string
	if err := env.DB.QueryRow(`SELECT refs::text FROM pipeline_stage_events WHERE manifest_id = $1::uuid AND stage = 'resolve'`, p.manifest).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf(`{"activity_id": "%s", "account_change_id": "%s"}`, p.activity, change); refs != want {
		t.Errorf("refs = %s, want the activity and the change", refs)
	}
	// the run-scoped form
	_ = rec.Record(bg, p.runScope(), Decide, Completed, Outcome{})
	if err := rec.MergeRefs(bg, p.runScope(), Decide, Refs{StrategySetID: change}); err != nil {
		t.Fatal(err)
	}
	var nilRec *Recorder
	if nilRec.MergeRefs(bg, Scope{}, Resolve, Refs{}) != nil {
		t.Fatal("a nil recorder must do nothing")
	}
	if err := rec.MergeRefs(bg, Scope{}, Resolve, Refs{}); err == nil {
		t.Fatal("an invalid scope was accepted")
	}
}

func TestANilRecorderIsASafeNoOp(t *testing.T) {
	var rec *Recorder
	h, err := rec.Begin(bg, Scope{}, Ingest)
	if err != nil || h.Finish(bg, Completed, Outcome{}) != nil || h.Fail(bg, errors.New("x")) != nil {
		t.Fatalf("a nil recorder must do nothing: %v", err)
	}
	if rec.Record(bg, Scope{}, Ingest, Completed, Outcome{}) != nil {
		t.Fatal("a nil recorder must do nothing")
	}
}

func TestAFailureIsRecordedEvenWhenTheRequestWasCancelled(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	ctx, cancel := context.WithCancel(bg)
	h, _ := rec.Begin(ctx, p.manifestScope(), Graph)
	cancel() // the Play timed out: its context is gone, the failure must still be written
	if err := h.Fail(ctx, ctx.Err()); err != nil {
		t.Fatal(err)
	}
	if got, _ := p.row(t, p.manifestScope(), Graph); got.status != "failed" || got.kind != "transport" {
		t.Fatalf("a cancelled Play: %+v", got)
	}
}

func TestBeginValidatesItsScope(t *testing.T) {
	rec, _ := newRecorder(t)
	for name, sc := range map[string]Scope{
		"no owner":   {AccountID: "0a0c0000-0000-4000-8000-000000000001"},
		"no account": {RunID: "0f0a0000-0000-4000-8000-000000000601"},
		"bad id":     {ManifestID: "nope", AccountID: "0a0c0000-0000-4000-8000-000000000001"},
	} {
		if _, err := rec.Begin(bg, sc, Ingest); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := rec.Begin(bg, Scope{RunID: "0f0a0000-0000-4000-8000-000000000601", AccountID: "0a0c0000-0000-4000-8000-000000000001"}, Stage("deploy")); err == nil {
		t.Error("an unknown stage was accepted")
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureKind
	}{
		{"deadline", context.DeadlineExceeded, Transport},
		{"cancelled", fmt.Errorf("wrapped: %w", context.Canceled), Transport},
		{"dial", &net.OpError{Op: "dial", Err: errors.New("refused")}, Transport},
		{"explicit transport", MarkTransport(errors.New("worker 503")), Transport},
		{"plain error", errors.New("constraint violated"), Internal},
		{"contract", MarkContract(errors.New("unusable output")), Contract},
		{"nil is internal", nil, Internal},
	}
	for _, tc := range cases {
		if got := ClassifyError(tc.err); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// passed and warning are eval verdicts: only the judging stage may render them; every other stage says completed.
func TestTheVocabularyIsHonestPassedAndWarningAreEvalVerdictsOnly(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	for _, bad := range []Status{Passed, Warning} {
		h, err := rec.Begin(bg, p.manifestScope(), Ingest)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Finish(bg, bad, Outcome{}); err == nil {
			t.Errorf("a non-eval stage accepted %q", bad)
		}
	}
	e, err := rec.Begin(bg, p.runScope(), Evals)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Finish(bg, Completed, Outcome{}); err == nil {
		t.Error("the evals stage accepted completed: it renders verdicts")
	}
	if err := e.Finish(bg, Passed, Outcome{EvalResultIDs: []string{"0e1a0000-0000-4000-8000-000000000912"}}); err != nil {
		t.Errorf("evals passed: %v", err)
	}
}

func TestEveryUpdateOfAStageMovesItsSeq(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	seq := func() int64 {
		var n int64
		if err := env.DB.QueryRow(`SELECT seq FROM pipeline_stage_events WHERE manifest_id = $1::uuid AND stage = 'ingest'`, p.manifest).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	h, err := rec.Begin(bg, p.manifestScope(), Ingest)
	if err != nil {
		t.Fatal(err)
	}
	started := seq()
	if err := h.Finish(bg, Completed, Outcome{Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	ended := seq()
	if err := rec.MergeRefs(bg, p.manifestScope(), Ingest, Refs{ActivityID: p.activity}); err != nil {
		t.Fatal(err)
	}
	if merged := seq(); !(started < ended && ended < merged) {
		t.Errorf("seq %d -> %d -> %d: a poller keyed on seq would miss a change", started, ended, merged)
	}
}
