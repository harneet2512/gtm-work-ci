package stageevents

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

// baseTime is the instant newRecorder's clock starts at; the default reader reads at the same instant, so a stage
// a test left running is not yet past its deadline.
var baseTime = time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)

func newReader(t *testing.T) *Reader { return newReaderAt(t, baseTime) }

func newReaderAt(t *testing.T, now time.Time) *Reader {
	t.Helper()
	r, err := NewReader(env.DB, clock.NewFixed(now))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// validDoc asserts the progress document conforms to pipeline_progress.v1.json.
func validDoc(t *testing.T, p Progress) {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("pipeline_progress", raw); err != nil {
		t.Fatalf("progress violates its schema: %v\n%s", err, raw)
	}
}

func statusOf(p Progress, s Stage) Status {
	for _, d := range p.Stages {
		if d.Stage == s {
			return d.Status
		}
	}
	return ""
}

func stageDoc(p Progress, s Stage) StageDoc {
	for _, d := range p.Stages {
		if d.Stage == s {
			return d
		}
	}
	return StageDoc{}
}

func TestAManifestNobodyPlayedIsNotStartedAndEveryStageWaits(t *testing.T) {
	p := newPlay(t)
	got, err := newReader(t).Manifest(bg, p.manifest)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, got)
	if got.Overall != NotStarted || len(got.Stages) != 7 {
		t.Fatalf("overall %s, %d stages", got.Overall, len(got.Stages))
	}
	for i, d := range got.Stages {
		if d.Stage != Order[i] || d.Status != Waiting || d.Seq != nil || d.Attempt != 0 {
			t.Errorf("stage %d = %+v, want a waiting %s with no row", i, d, Order[i])
		}
	}
}

func TestAnUnknownManifestIsNotFound(t *testing.T) {
	if _, err := newReader(t).Manifest(bg, "99999999-9999-4999-8999-999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := newReader(t).Manifest(bg, "not-a-uuid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a malformed id: err = %v, want ErrNotFound", err)
	}
	if _, err := newReader(t).Run(bg, "99999999-9999-4999-8999-999999999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown run: err = %v, want ErrNotFound", err)
	}
}

func TestProgressMergesThePlaysStagesWithTheRunsAndNeverInventsOne(t *testing.T) {
	p := newPlay(t)
	rec, clk := newRecorder(t)
	for _, s := range []Stage{Ingest, Resolve, State, Graph} {
		h, _ := rec.Begin(bg, p.manifestScope(), s)
		clk.Advance(time.Second)
		_ = h.Finish(bg, Completed, Outcome{Detail: string(s)})
	}
	dec, _ := rec.Begin(bg, p.runScope(), Decide) // the run is picked up and still running
	_ = dec

	got, err := newReader(t).Manifest(bg, p.manifest)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, got)
	want := map[Stage]Status{Ingest: Completed, Resolve: Completed, Graph: Completed, State: Completed, Decide: Running, Evals: Waiting, Cliff: Waiting}
	for s, st := range want {
		if statusOf(got, s) != st {
			t.Errorf("%s = %s, want %s", s, statusOf(got, s), st)
		}
	}
	if got.Overall != InProgress {
		t.Errorf("overall = %s, want running (decide is mid-flight, evals and cliff can still arrive)", got.Overall)
	}
	if d := stageDoc(got, Ingest); d.DurationMS == nil || *d.DurationMS != 1000 || d.Seq == nil || d.Attempt != 1 {
		t.Errorf("ingest = %+v, want a 1000 ms duration, a seq and attempt 1", d)
	}
	if got.RunID != nil {
		t.Errorf("a manifest document has no run_id: %v", *got.RunID)
	}
}

func TestAFailureMidPipelineIsFailedAndLaterStagesAreNotRun(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	for _, s := range []Stage{Ingest, Resolve} {
		_ = rec.Record(bg, p.manifestScope(), s, Completed, Outcome{})
	}
	h, _ := rec.Begin(bg, p.manifestScope(), State)
	_ = h.Fail(bg, errors.New("deadlock"))

	got, _ := newReader(t).Manifest(bg, p.manifest)
	validDoc(t, got)
	if got.Overall != Broken || statusOf(got, State) != Failed {
		t.Fatalf("overall %s, state %s", got.Overall, statusOf(got, State))
	}
	for _, s := range []Stage{Graph, Decide, Evals, Cliff} {
		if d := stageDoc(got, s); d.Status != Waiting || d.StartedAt != nil {
			t.Errorf("%s = %+v: a stage after the failure must not look run", s, d)
		}
	}
	if d := stageDoc(got, State); d.FailureKind == nil || *d.FailureKind != Internal {
		t.Errorf("state failure kind = %v, want internal", d.FailureKind)
	}
}

func TestATransportErrorWhileJudgingIsUnknownAndNeverAnEvalFail(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	h, _ := rec.Begin(bg, p.runScope(), Evals)
	_ = h.Fail(bg, errors.New("x"))
	h2, _ := rec.Begin(bg, p.runScope(), Decide)
	_ = h2.Fail(bg, MarkTransport(errors.New("worker 503")))

	got, _ := newReader(t).Manifest(bg, p.manifest)
	validDoc(t, got)
	if s := statusOf(got, Evals); s != Unknown {
		t.Errorf("evals = %s, want unknown", s)
	}
	if d := stageDoc(got, Decide); d.Status != Failed || *d.FailureKind != Transport {
		t.Errorf("decide = %+v, want failed/transport", d)
	}
	if got.Overall != Broken {
		t.Errorf("overall = %s: a pipeline that stopped on errors is not running", got.Overall)
	}
}

func TestAJudgedEvalFailureIsFailedWithItsResults(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	id := "0e1a0000-0000-4000-8000-000000000911"
	_ = rec.Record(bg, p.runScope(), Evals, Failed, Outcome{EvalResultIDs: []string{id}, Detail: "1 blocking"})
	got, _ := newReader(t).Manifest(bg, p.manifest)
	validDoc(t, got)
	d := stageDoc(got, Evals)
	if d.Status != Failed || d.FailureKind != nil || len(d.EvalResultIDs) != 1 || d.EvalResultIDs[0] != id {
		t.Fatalf("evals = %+v", d)
	}
}

func TestACompletePipelineIsComplete(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	for _, s := range []Stage{Ingest, Resolve, Graph, State} {
		_ = rec.Record(bg, p.manifestScope(), s, Completed, Outcome{})
	}
	_ = rec.Record(bg, p.runScope(), Decide, Completed, Outcome{Refs: Refs{RunID: p.run}})
	_ = rec.Record(bg, p.runScope(), Evals, Warning, Outcome{EvalResultIDs: []string{"0e1a0000-0000-4000-8000-000000000912"}})
	_ = rec.Record(bg, p.manifestScope(), Cliff, Completed, Outcome{})

	got, _ := newReader(t).Manifest(bg, p.manifest)
	validDoc(t, got)
	if got.Overall != Complete {
		t.Fatalf("overall = %s", got.Overall)
	}
}

func TestANonMaterialEventSkipsTheDecisionStagesAndIsComplete(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	for _, s := range []Stage{Ingest, Resolve, Graph, State} {
		_ = rec.Record(bg, p.manifestScope(), s, Completed, Outcome{})
	}
	for _, s := range []Stage{Decide, Evals, Cliff} {
		_ = rec.Record(bg, p.manifestScope(), s, Skipped, Outcome{Detail: "no_material_change"})
	}
	got, _ := newReader(t).Manifest(bg, p.manifest)
	validDoc(t, got)
	if got.Overall != Complete || statusOf(got, Decide) != Skipped {
		t.Fatalf("overall %s decide %s", got.Overall, statusOf(got, Decide))
	}
}

func TestARunsProgressCarriesThePlaysUpstreamStages(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	_ = rec.Record(bg, p.manifestScope(), Ingest, Completed, Outcome{})
	_ = rec.Record(bg, p.runScope(), Decide, Completed, Outcome{})

	got, err := newReader(t).Run(bg, p.run)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, got)
	if got.Scope != "run" || got.RunID == nil || *got.RunID != p.run || got.ManifestID == nil || *got.ManifestID != p.manifest {
		t.Fatalf("scope %s run %v manifest %v", got.Scope, got.RunID, got.ManifestID)
	}
	if statusOf(got, Ingest) != Completed || statusOf(got, Decide) != Completed || statusOf(got, Resolve) != Waiting {
		t.Errorf("stages: ingest %s decide %s resolve %s", statusOf(got, Ingest), statusOf(got, Decide), statusOf(got, Resolve))
	}
}

func TestARunThatDidNotComeFromAPlayReportsUpstreamStagesAsUnknown(t *testing.T) {
	world := ctxfixture.Get(t, env.DB) // the second account's run never came from a Play
	t.Cleanup(func() {
		_, _ = env.DB.Exec(`DELETE FROM pipeline_stage_events WHERE account_id = $1::uuid`, world.AccountB)
	})
	rec, _ := newRecorder(t)
	_ = rec.Record(bg, Scope{RunID: world.RunB, AccountID: world.AccountB}, Decide, Completed, Outcome{})

	got, err := newReader(t).Run(bg, world.RunB)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, got)
	if got.ManifestID != nil {
		t.Errorf("manifest = %v, want null", *got.ManifestID)
	}
	for _, s := range []Stage{Ingest, Resolve, Graph, State} {
		if d := stageDoc(got, s); d.Status != Unknown || d.Seq != nil || d.Detail == nil {
			t.Errorf("%s = %+v: the pipeline did not record it, so it is unknown with a reason, never passed", s, d)
		}
	}
	if statusOf(got, Evals) != Waiting {
		t.Errorf("evals = %s", statusOf(got, Evals))
	}
	if got.Overall == Complete {
		t.Errorf("a run with unknown upstream stages and no evals is not complete")
	}
}

func TestCliffPrefersTheManifestsRowAndRunStagesTheRuns(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	_ = rec.Record(bg, p.manifestScope(), Cliff, Completed, Outcome{Detail: "manifest cliff"})
	_ = rec.Record(bg, p.runScope(), Cliff, Unknown, Outcome{Detail: "run cliff"})
	_ = rec.Record(bg, p.manifestScope(), Decide, Skipped, Outcome{Detail: "manifest decide"})
	_ = rec.Record(bg, p.runScope(), Decide, Completed, Outcome{Detail: "run decide"})
	got, _ := newReader(t).Manifest(bg, p.manifest)
	if d := stageDoc(got, Cliff); d.Detail == nil || *d.Detail != "manifest cliff" {
		t.Errorf("cliff = %+v, want the manifest's row", d)
	}
	if d := stageDoc(got, Decide); d.Detail == nil || *d.Detail != "run decide" {
		t.Errorf("decide = %+v, want the run's row", d)
	}
}

func TestOverallOf(t *testing.T) {
	mk := func(statuses ...Status) []StageDoc {
		out := make([]StageDoc, len(Order))
		for i, s := range Order {
			st := Waiting
			if i < len(statuses) {
				st = statuses[i]
			}
			out[i] = StageDoc{Stage: s, Status: st}
		}
		return out
	}
	seq := int64(1)
	recordedUnknown := mk(Completed, Unknown)
	recordedUnknown[1].Seq = &seq
	cases := []struct {
		name   string
		stages []StageDoc
		want   Overall
	}{
		{"nothing ran", mk(), NotStarted},
		{"one ran", mk(Completed), InProgress},
		{"a stage running", mk(Completed, Running), InProgress},
		{"a failure", mk(Completed, Failed), Broken},
		{"all done", mk(Completed, Completed, Completed, Completed, Completed, Warning, Skipped), Complete},
		{"a recorded unknown stopped the pipeline", recordedUnknown, Broken},
		{"unrecorded unknown upstream does not block completion", mk(Unknown, Unknown, Unknown, Unknown, Completed, Completed, Completed), Complete},
	}
	for _, tc := range cases {
		if got := overallOf(tc.stages); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// A process that crashed mid-stage never writes the end. The reader reports such a stage as unknown (transport,
// "no heartbeat") once it outlives its deadline, so a poller is never left waiting on a stage that cannot finish.
func TestARunningStagePastItsDeadlineIsUnknownNotRunningForever(t *testing.T) {
	p := newPlay(t)
	rec, _ := newRecorder(t)
	if _, err := rec.Begin(bg, p.manifestScope(), State); err != nil {
		t.Fatal(err)
	}
	within, _ := newReaderAt(t, baseTime.Add(deadlines[State])).Manifest(bg, p.manifest)
	if d := stageDoc(within, State); d.Status != Running {
		t.Fatalf("at the deadline: %s, want running (not yet past it)", d.Status)
	}
	got, err := newReaderAt(t, baseTime.Add(deadlines[State]+time.Second)).Manifest(bg, p.manifest)
	if err != nil {
		t.Fatal(err)
	}
	validDoc(t, got)
	d := stageDoc(got, State)
	if d.Status != Unknown || d.FailureKind == nil || *d.FailureKind != Transport || d.Detail == nil || !strings.HasPrefix(*d.Detail, "no heartbeat") || d.EndedAt != nil {
		t.Fatalf("a stale running stage = %+v, want unknown / transport / no heartbeat", d)
	}
	if got.Overall != Broken {
		t.Errorf("overall = %s, want failed: the pipeline stopped at a stage that never ended", got.Overall)
	}
	if row, _ := p.row(t, p.manifestScope(), State); row.status != "running" {
		t.Errorf("stored status = %s: the read must not rewrite the row", row.status)
	}
}

func TestEveryStageHasADeadline(t *testing.T) {
	for _, s := range Order {
		if deadlines[s] <= 0 {
			t.Errorf("stage %s has no deadline", s)
		}
	}
}

func TestOnlyARunningStageGrowsStale(t *testing.T) {
	long := baseTime.Add(100 * time.Hour)
	started := baseTime
	for _, st := range []Status{Completed, Passed, Failed, Skipped, Unknown, Waiting} {
		d := StageDoc{Stage: Decide, Status: st, StartedAt: &started}
		if got := withDeadline(d, long); got.Status != st {
			t.Errorf("%s became %s", st, got.Status)
		}
	}
}
