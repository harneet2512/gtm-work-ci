package play

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// progressRig is a rig whose Play records its stages (HAR-145 live Play progress).
type progressRig struct {
	*rig
	rec    *stageevents.Recorder
	reader *stageevents.Reader
}

func newProgressRig(t *testing.T, held replaytest.HeldOut) *progressRig {
	t.Helper()
	pr := &progressRig{rig: newRig(t, held)}
	var err error
	if pr.rec, err = stageevents.NewRecorder(env.DB, nil); err != nil {
		t.Fatal(err)
	}
	if pr.reader, err = stageevents.NewReader(env.DB, nil); err != nil {
		t.Fatal(err)
	}
	pr.svc = pr.service(func(o *Options) { o.Stages = pr.rec })
	return pr
}

// service builds a Service over the rig that records its stages.
func (p *progressRig) recording(mutate func(*Options)) *Service {
	return p.service(func(o *Options) {
		o.Stages = p.rec
		mutate(o)
	})
}

func (p *progressRig) progress() stageevents.Progress {
	p.t.Helper()
	doc, err := p.reader.Manifest(bg, p.manifest)
	if err != nil {
		p.t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	v, err := schemacheck.New()
	if err != nil {
		p.t.Fatal(err)
	}
	if err := v.Validate("pipeline_progress", raw); err != nil {
		p.t.Fatalf("progress violates its schema: %v\n%s", err, raw)
	}
	return doc
}

func stage(p stageevents.Progress, s stageevents.Stage) stageevents.StageDoc {
	for _, d := range p.Stages {
		if d.Stage == s {
			return d
		}
	}
	return stageevents.StageDoc{}
}

func statuses(p stageevents.Progress) map[stageevents.Stage]stageevents.Status {
	out := map[stageevents.Stage]stageevents.Status{}
	for _, d := range p.Stages {
		out[d.Stage] = d.Status
	}
	return out
}

func allStatuses(ingest, resolve, graph, state, decide, evals, cliff stageevents.Status) map[stageevents.Stage]stageevents.Status {
	return map[stageevents.Stage]stageevents.Status{
		stageevents.Ingest: ingest, stageevents.Resolve: resolve, stageevents.Graph: graph, stageevents.State: state,
		stageevents.Decide: decide, stageevents.Evals: evals, stageevents.Cliff: cliff,
	}
}

const (
	completed = stageevents.Completed
	failed    = stageevents.Failed
	waiting   = stageevents.Waiting
	skipped   = stageevents.Skipped
)

func TestPlayRecordsEveryStageItActuallyExecutes(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	if got := r.progress(); got.Overall != stageevents.NotStarted {
		t.Fatalf("before Play: %s, want not_started", got.Overall)
	}
	if _, err := r.svc.Play(bg, Request{ManifestID: r.manifest}); err != nil {
		t.Fatalf("Play: %v", err)
	}
	p := r.progress()

	// Play executes ingest..graph; the decision is a run's work and is still owed (waiting, not completed)
	if got, want := statuses(p), allStatuses(completed, completed, completed, completed, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if p.Overall != stageevents.InProgress {
		t.Errorf("overall = %s, want running: the run has not decided yet", p.Overall)
	}

	// each stage points at the rows it really produced
	src := replaytest.One(t, env.DB, `SELECT source_event_id::text FROM demo_plays`)
	act := replaytest.One(t, env.DB, `SELECT activity_id::text FROM demo_plays`)
	if d := stage(p, stageevents.Ingest); d.Refs.SourceEventID != src || d.Refs.ActivityID != act {
		t.Errorf("ingest refs = %+v, want source event %s and activity %s", d.Refs, src, act)
	}
	change := replaytest.One(t, env.DB, `SELECT id::text FROM account_changes`)
	if d := stage(p, stageevents.Resolve); d.Refs.ActivityID != act || d.Refs.AccountChangeID != change {
		t.Errorf("resolve refs = %+v, want the activity %s and the account change %s", d.Refs, act, change)
	}
	diff := replaytest.One(t, env.DB, `SELECT id::text FROM state_diffs WHERE activity_ids @> ARRAY[$1]::uuid[]`, act)
	version := replaytest.One(t, env.DB, `SELECT version::text FROM account_state`)
	trigger := replaytest.One(t, env.DB, `SELECT id::text FROM trigger_evaluations ORDER BY evaluated_at DESC LIMIT 1`)
	if d := stage(p, stageevents.State); d.Refs.StateDiffID != diff || strconv.Itoa(d.Refs.StateVersion) != version || d.Refs.TriggerEvaluationID != trigger {
		t.Errorf("state refs = %+v, want diff %s, version %s, trigger %s", d.Refs, diff, version, trigger)
	}
	graph := replaytest.One(t, env.DB, `SELECT min(id)::text FROM graph_projection_diffs WHERE $1::uuid = ANY (source_event_ids)`, src)
	if d := stage(p, stageevents.Graph); strconv.FormatInt(d.Refs.GraphDiffID, 10) != graph {
		t.Errorf("graph refs = %+v, want graph diff %s", d.Refs, graph)
	}

	// the stages start in the order the pipeline ran them (seq is a change counter, not an order: started_at is)
	var last time.Time
	for _, s := range []stageevents.Stage{stageevents.Ingest, stageevents.Resolve, stageevents.State, stageevents.Graph} {
		d := stage(p, s)
		if d.Seq == nil || d.StartedAt == nil || d.StartedAt.Before(last) || d.EndedAt == nil || d.EndedAt.Before(*d.StartedAt) {
			t.Fatalf("%s: seq %v started %v ended %v (previous start %v)", s, d.Seq, d.StartedAt, d.EndedAt, last)
		}
		last = *d.StartedAt
	}
}

func TestAFailureAtTheGraphMarksItFailedAndNothingAfterItRan(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	stuck := r.recording(func(o *Options) { o.Graph = stuckBarrier{}; o.Timeout = time.Second })
	if _, err := stuck.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Play with the projector down: %v", err)
	}
	p := r.progress()
	if got, want := statuses(p), allStatuses(completed, completed, failed, completed, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if d := stage(p, stageevents.Graph); d.FailureKind == nil || *d.FailureKind != stageevents.Transport {
		t.Errorf("graph failure kind = %v: a projector that never answered is a transport problem", d.FailureKind)
	}
	if p.Overall != stageevents.Broken {
		t.Errorf("overall = %s, want failed", p.Overall)
	}
	for _, s := range []stageevents.Stage{stageevents.Decide, stageevents.Evals, stageevents.Cliff} {
		if d := stage(p, s); d.StartedAt != nil || d.Seq != nil {
			t.Errorf("%s has a row though the pipeline stopped before it: %+v", s, d)
		}
	}
}

// noDrain is a recompute that never produces the diff: the coalescer is down.
type noDrain struct{}

func (noDrain) Drain(context.Context) (coalesce.DrainResult, error) {
	return coalesce.DrainResult{}, nil
}

func TestAFailureAtTheStateMarksItFailedAndTheGraphNeverStarted(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	svc := r.recording(func(o *Options) { o.Recompute = noDrain{}; o.Timeout = 300 * time.Millisecond })
	if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Play with the coalescer down: %v, want ErrTimeout", err)
	}
	p := r.progress()
	if got, want := statuses(p), allStatuses(completed, completed, waiting, failed, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if d := stage(p, stageevents.State); *d.FailureKind != stageevents.Transport {
		t.Errorf("state failure kind = %s, want transport (a timeout is not a verdict)", *d.FailureKind)
	}
}

// erroringIngest is an ingest path that errors.
type erroringIngest struct{ err error }

func (f erroringIngest) Ingest(context.Context, normalize.SourceEvent) (ingest.Result, error) {
	return ingest.Result{}, f.err
}

func TestAFailureAtIngestLeavesEveryLaterStageWaiting(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		kind stageevents.FailureKind
	}{
		"a database error":    {errors.New("insert: deadlock detected"), stageevents.Internal},
		"a deadline exceeded": {context.DeadlineExceeded, stageevents.Transport},
	} {
		t.Run(name, func(t *testing.T) {
			r := newProgressRig(t, replaytest.NewHeldOut(2))
			svc := r.recording(func(o *Options) { o.Ingest = erroringIngest{tc.err} })
			if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); err == nil {
				t.Fatal("Play succeeded with a failing ingest")
			}
			p := r.progress()
			if got, want := statuses(p), allStatuses(failed, waiting, waiting, waiting, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
				t.Fatalf("stages = %v, want %v", got, want)
			}
			d := stage(p, stageevents.Ingest)
			if *d.FailureKind != tc.kind {
				t.Errorf("failure kind = %s, want %s", *d.FailureKind, tc.kind)
			}
			if d.Detail != nil && strings.Contains(*d.Detail, "deadlock") {
				t.Errorf("detail repeats raw error text: %q", *d.Detail)
			}
		})
	}
}

func TestAReleaseThePipelineCannotAttributeFailsResolveAsAContractProblem(t *testing.T) {
	stranger := replaytest.HeldOut{EventID: "0e7e0000-0000-4000-8000-000000000004",
		Event: replaytest.EmailBetween(7, replaytest.T0.Add(time.Hour), "inbound", "someone@unknown.example", "dana@vendor.example", "We need the SOC2 report")}
	r := newProgressRig(t, stranger)
	if _, err := r.svc.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrReleaseMismatch) {
		t.Fatalf("Play: %v, want ErrReleaseMismatch", err)
	}
	p := r.progress()
	if got, want := statuses(p), allStatuses(completed, failed, waiting, waiting, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v (the event was ingested; it could not be resolved to the manifest's account)", got, want)
	}
	if d := stage(p, stageevents.Resolve); *d.FailureKind != stageevents.Contract {
		t.Errorf("resolve failure kind = %s, want contract", *d.FailureKind)
	}
}

type rowState struct {
	Status   stageevents.Status
	Attempt  int
	Seq      int64
	Updated  time.Time
	Started  time.Time
	FailKind string
}

func snapshot(p stageevents.Progress) map[stageevents.Stage]rowState {
	out := map[stageevents.Stage]rowState{}
	for _, d := range p.Stages {
		if d.Seq == nil {
			continue
		}
		rs := rowState{Status: d.Status, Attempt: d.Attempt, Seq: *d.Seq, Updated: *d.UpdatedAt, Started: *d.StartedAt}
		if d.FailureKind != nil {
			rs.FailKind = string(*d.FailureKind)
		}
		out[d.Stage] = rs
	}
	return out
}

func TestReplayingAPlayIsIdempotentInItsStageEvents(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	stuck := r.recording(func(o *Options) { o.Graph = stuckBarrier{}; o.Timeout = time.Second })
	if _, err := stuck.Play(bg, Request{ManifestID: r.manifest}); err == nil {
		t.Fatal("Play with the projector down succeeded")
	}
	interrupted := snapshot(r.progress())

	if _, err := r.svc.Play(bg, Request{ManifestID: r.manifest}); err != nil { // the projector is back: the Play resumes
		t.Fatalf("resumed Play: %v", err)
	}
	resumed := snapshot(r.progress())
	for _, s := range []stageevents.Stage{stageevents.Ingest, stageevents.Resolve, stageevents.State} {
		if resumed[s].Attempt != 1 || !resumed[s].Started.Equal(interrupted[s].Started) {
			t.Errorf("%s was re-recorded by the resume: %+v -> %+v (a finished stage is never rewritten)", s, interrupted[s], resumed[s])
		}
		// seq is a change counter: only the resolve stage changes on the resume, when the AccountChange that the
		// interrupted Play never reached is attached to it
		if unchanged := resumed[s].Seq == interrupted[s].Seq; unchanged == (s == stageevents.Resolve) {
			t.Errorf("%s: seq %d -> %d: want it moved only for resolve (the late AccountChange ref)", s, interrupted[s].Seq, resumed[s].Seq)
		}
	}
	if g := resumed[stageevents.Graph]; g.Status != completed || g.Attempt != 2 || g.FailKind != "" {
		t.Errorf("graph after the resume = %+v, want completed on attempt 2 with the failure cleared", g)
	}
	if len(resumed) != 4 {
		t.Errorf("stage rows = %d, want 4: the resume must not add rows", len(resumed))
	}

	// a Play of a completed manifest executes nothing and records nothing
	if _, err := r.svc.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatalf("third Play: %v, want ErrAlreadyReleased", err)
	}
	if again := snapshot(r.progress()); !reflect.DeepEqual(again, resumed) {
		t.Fatalf("a refused Play changed the stage events:\nbefore %+v\nafter  %+v", resumed, again)
	}
}

// flipEligible makes the trigger evaluation of the played event ineligible after the recompute wrote it, so
// the Play meets an event that owes no decision.
type flipEligible struct{ inner Recomputer }

func (f flipEligible) Drain(ctx context.Context) (coalesce.DrainResult, error) {
	res, err := f.inner.Drain(ctx)
	if _, derr := env.DB.ExecContext(ctx, `DELETE FROM agent_runs; UPDATE trigger_evaluations SET eligible = false, reason_codes = ARRAY['no_material_change']`); derr != nil {
		return res, derr
	}
	return res, err
}

func TestAnEventThatOwesNoDecisionSkipsTheDecisionStagesAndSaysWhy(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	svc := r.recording(func(o *Options) { o.Recompute = flipEligible{drainer{r.stack}} })
	if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); err != nil {
		t.Fatalf("Play: %v", err)
	}
	p := r.progress()
	if got, want := statuses(p), allStatuses(completed, completed, completed, completed, skipped, skipped, skipped); !reflect.DeepEqual(got, want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	if d := stage(p, stageevents.Decide); d.Detail == nil || !strings.Contains(*d.Detail, "no_material_change") {
		t.Errorf("decide detail = %v, want the trigger's reason", d.Detail)
	}
	if p.Overall != stageevents.Complete {
		t.Errorf("overall = %s, want complete: nothing is owed", p.Overall)
	}
}

func TestPlayWithoutARecorderBehavesAsBefore(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2)) // no Stages
	if _, err := r.play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if got := replaytest.One(t, env.DB, `SELECT count(*)::text FROM pipeline_stage_events`); got != "0" {
		t.Fatalf("a Play with no recorder wrote %s stage events", got)
	}
}

func TestAdvancingToTheHeldOutEventRecordsItsStagesAndHistoryAdvancesRecordNone(t *testing.T) {
	r := newEpisodeRig(t)
	rec, _ := stageevents.NewRecorder(env.DB, nil)
	reader, _ := stageevents.NewReader(env.DB, nil)
	r.svc = r.service(func(o *Options) { o.Rules = episodeRules(t); o.Stages = rec })

	advanceEpisodes(t, r, 2) // history: nothing executes, the frozen pipeline run already left its trail
	before, err := reader.Manifest(bg, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if before.Overall != stageevents.NotStarted {
		t.Fatalf("after two history advances: %s, want not_started (no stage executed)", before.Overall)
	}

	if _, err := r.svc.NextEpisode(bg, r.manifest); err != nil {
		t.Fatalf("advance to the held-out event: %v", err)
	}
	after, _ := reader.Manifest(bg, r.manifest)
	if got, want := statuses(after), allStatuses(completed, completed, completed, completed, waiting, waiting, waiting); !reflect.DeepEqual(got, want) {
		t.Fatalf("after advancing to event N: %v, want %v", got, want)
	}
}

// breakPlayRecord drops the play record after the recompute ran, so the Play's last transaction (the write of the
// account change) fails; it also makes the trigger ineligible, which would skip the decision stages if they were
// written before the commit.
type breakPlayRecord struct{ inner Recomputer }

func (b breakPlayRecord) Drain(ctx context.Context) (coalesce.DrainResult, error) {
	res, err := b.inner.Drain(ctx)
	if _, derr := env.DB.ExecContext(ctx, `DELETE FROM agent_runs; UPDATE trigger_evaluations SET eligible = false, reason_codes = ARRAY['no_material_change']; DELETE FROM demo_plays`); derr != nil {
		return res, derr
	}
	return res, err
}

func TestAPlayWhoseChangeCannotBeWrittenIsFailedNeverCompleteAndNeverSkipsTheDecision(t *testing.T) {
	r := newProgressRig(t, replaytest.NewHeldOut(2))
	svc := r.recording(func(o *Options) { o.Recompute = breakPlayRecord{drainer{r.stack}} })
	if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); err == nil {
		t.Fatal("Play succeeded though its change could not be written")
	}
	p := r.progress()
	if p.Overall != stageevents.Broken {
		t.Fatalf("overall = %s, want failed: a Play that wrote nothing is not complete", p.Overall)
	}
	g := stage(p, stageevents.Graph)
	if g.Status != failed || g.FailureKind == nil || g.Detail == nil || !strings.Contains(*g.Detail, "account change could not be written") {
		t.Fatalf("graph = %+v, want failed with a detail naming the write that failed", g)
	}
	for _, s := range []stageevents.Stage{stageevents.Decide, stageevents.Evals, stageevents.Cliff} {
		if d := stage(p, s); d.Status != waiting || d.Seq != nil {
			t.Errorf("%s = %+v: nothing was committed, so nothing was decided about it (no skipped row before the commit)", s, d)
		}
	}
}

func TestAStageWithNoArtifactToPointAtIsUnknownNotCompleted(t *testing.T) {
	rec, _ := stageevents.NewRecorder(env.DB, nil)
	tr := tracker{rec: rec, db: env.DB}
	const none = "0e1a0000-0000-4000-8000-0000000000ee"
	if st, out := tr.stateOutcome(bg, none, none); st != stageevents.Unknown || out.Refs.StateDiffID != "" || out.Detail == "" {
		t.Errorf("state without a diff = %s %+v, want unknown with no ref and a reason", st, out)
	}
	if st, out := tr.graphOutcome(bg, none); st != stageevents.Unknown || out.Refs.GraphDiffID != 0 || out.Detail == "" {
		t.Errorf("graph without a diff = %s %+v, want unknown with no ref and a reason", st, out)
	}
}
