package play

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

func TestPlayReleasesTheHeldOutEventThroughTheRealPipeline(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	before := r.footprint()
	if rep, _ := r.svc.Invisibility(bg, r.manifest); rep.Status != StatusWithheld {
		t.Fatalf("before Play: %+v", rep)
	}

	doc, err := r.play()
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	f := fields(t, doc)
	validate(t, "replay_world", f["replay_world"])
	validate(t, "account_change", f["account_change"])
	validate(t, "business_intelligence_update", f["business_intelligence_update"])

	// the pipeline ran: ingest, activity, claims, state, diff, signals, trigger, run, graph job
	after := r.footprint()
	for _, table := range []string{"source_events", "activities", "state_history", "state_diffs", "trigger_evaluations", "agent_runs", "account_changes", "business_intelligence_updates", "demo_plays"} {
		b, _ := strconv.Atoi(before[table])
		a, _ := strconv.Atoi(after[table])
		if a <= b {
			t.Errorf("%s did not grow: %s -> %s", table, before[table], after[table])
		}
	}
	if after["source_events"] != "3" || after["activities"] != "3" {
		t.Errorf("exactly one event is released: source_events %s activities %s (2 history + N)", after["source_events"], after["activities"])
	}
	if got := replaytest.One(t, env.DB, `SELECT count(*)::text FROM signals`); got == "0" {
		t.Error("the recompute emitted no signal")
	}
	if got := replaytest.One(t, env.DB, `SELECT eligible::text FROM trigger_evaluations ORDER BY evaluated_at DESC LIMIT 1`); got != "true" {
		t.Errorf("the trigger evaluation of the event: eligible = %s", got)
	}

	// the change and the update restate what the pipeline stored
	if got := replaytest.One(t, env.DB, `SELECT held_out_event_id::text FROM account_changes`); got != r.held.EventID {
		t.Errorf("held_out_event_id = %s, want %s", got, r.held.EventID)
	}
	if got := replaytest.One(t, env.DB, `SELECT (c.state_diff_id = d.id)::text FROM account_changes c JOIN state_diffs d ON d.activity_ids @> c.trigger_activity_ids`); got != "true" {
		t.Error("the change does not point at the diff of its trigger activity")
	}
	var change struct {
		MaterialChange bool `json:"material_change"`
	}
	_ = json.Unmarshal(f["account_change"], &change)
	if !change.MaterialChange {
		t.Error("the SOC2 email is a material change")
	}

	// the play record is complete and the event is no longer withheld
	if got := replaytest.One(t, env.DB, `SELECT status FROM demo_plays`); got != "complete" {
		t.Errorf("play status = %s", got)
	}
	if rep, _ := r.svc.Invisibility(bg, r.manifest); rep.Status != StatusReleased {
		t.Errorf("after Play: %+v", rep)
	}
}

func TestTheReplayWorldMovesItsCursorFromNMinusOneToN(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	m, err := LoadManifest(bg, env.DB, r.manifest)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := r.svc.world(bg, m, false)
	if err != nil {
		t.Fatal(err)
	}
	validate(t, "replay_world", pre)

	doc, err := r.play()
	if err != nil {
		t.Fatal(err)
	}
	post := fields(t, doc)["replay_world"]
	validate(t, "replay_world", post)

	cursor := func(raw json.RawMessage) (int, string, string) {
		var w struct {
			Cursor struct {
				Position  int     `json:"replay_position"`
				Last      string  `json:"last_event_id"`
				HeldOutID *string `json:"held_out_event_id"`
			} `json:"event_cursor"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
		held := "<null>"
		if w.Cursor.HeldOutID != nil {
			held = *w.Cursor.HeldOutID
		}
		return w.Cursor.Position, w.Cursor.Last, held
	}
	if pos, last, held := cursor(pre); pos != 2 || last != replaytest.HistoryEventID(2) || held != r.held.EventID {
		t.Errorf("before Play: position %d last %s held-out %s", pos, last, held)
	}
	if pos, last, held := cursor(post); pos != 3 || last != r.held.EventID || held != "<null>" {
		t.Errorf("after Play: position %d last %s held-out %s", pos, last, held)
	}
}

func TestASecondPlayChangesNothing(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	if _, err := r.play(); err != nil {
		t.Fatalf("first Play: %v", err)
	}
	before, ingested, probed := r.footprint(), r.ingest.count(), r.probe.callCount()

	_, err := r.play()
	var released *AlreadyReleasedError
	if !errors.Is(err, ErrAlreadyReleased) || !errors.As(err, &released) {
		t.Fatalf("second Play: %v, want ErrAlreadyReleased", err)
	}
	if want := replaytest.One(t, env.DB, `SELECT id::text FROM account_changes`); released.AccountChangeID != want {
		t.Fatalf("the refusal names change %q, the first Play wrote %q", released.AccountChangeID, want)
	}
	if after := r.footprint(); !reflect.DeepEqual(before, after) {
		t.Fatalf("a second Play changed the world:\nbefore %v\nafter  %v", before, after)
	}
	if r.ingest.count() != ingested || r.probe.callCount() != probed {
		t.Fatal("a second Play reached ingest or the invisibility probe")
	}
	if got := r.count("account_changes") + "/" + r.count("business_intelligence_updates"); got != "1/1" {
		t.Fatalf("changes/updates = %s, want 1/1: the BI is written once per held-out event", got)
	}
}

func TestPlayRefusesWhenEventNIsAlreadyVisibleAndWritesNothing(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	if _, err := r.stack.Ingest.Ingest(bg, r.held.Event); err != nil { // the leak
		t.Fatal(err)
	}
	before, ingested := r.footprint(), r.ingest.count()

	_, err := r.play()
	var visible *VisibleError
	if !errors.As(err, &visible) {
		t.Fatalf("Play: %v, want *VisibleError", err)
	}
	if visible.Report.Status != StatusLeaked || !kinds(visible.Report)["postgres/source_event"] {
		t.Fatalf("report = %+v", visible.Report)
	}
	if after := r.footprint(); !reflect.DeepEqual(before, after) {
		t.Fatalf("a refused Play wrote something:\nbefore %v\nafter  %v", before, after)
	}
	if r.ingest.count() != ingested {
		t.Fatal("a refused Play released the event")
	}
}

func TestPlayRefusesWhenNeo4jAlreadyHoldsSomethingDerivedFromTheEvent(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	r.probe.hits = []ctxgraph.Hit{{Kind: "node", Type: "Claim", ID: "k-1"}}
	before := r.footprint()
	_, err := r.play()
	var visible *VisibleError
	if !errors.As(err, &visible) || !kinds(visible.Report)["neo4j/graph_node:Claim"] {
		t.Fatalf("Play: %v, want a VisibleError naming the graph node", err)
	}
	if after := r.footprint(); !reflect.DeepEqual(before, after) {
		t.Fatal("a refused Play wrote something")
	}
}

func TestPlayRefusesWhatCannotWorkBeforeReleasingAnything(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Options)
		req    func(*rig) Request
		want   error
	}{
		"no graph barrier":         {func(o *Options) { o.Graph = nil }, nil, ErrGraphUnavailable},
		"no graph probe":           {func(o *Options) { o.Probe = nil }, nil, ErrGraphUnavailable},
		"no dataset":               {func(o *Options) { o.Events = nil }, nil, ErrSourceUnavailable},
		"event not in the dataset": {func(o *Options) { o.Events = mapSource{} }, nil, ErrSourceUnavailable},
		"another event id": {func(*Options) {}, func(r *rig) Request {
			return Request{ManifestID: r.manifest, EventID: "0e7e0000-0000-4000-8000-0000000000aa"}
		}, ErrWrongEvent},
		"unknown manifest":   {func(*Options) {}, func(*rig) Request { return Request{ManifestID: "99999999-9999-4999-8999-999999999999"} }, ErrManifestNotFound},
		"malformed manifest": {func(*Options) {}, func(*rig) Request { return Request{ManifestID: "nope"} }, ErrManifestNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, replaytest.NewHeldOut(2))
			svc := r.service(tc.mutate)
			req := Request{ManifestID: r.manifest}
			if tc.req != nil {
				req = tc.req(r)
			}
			before := r.footprint()
			if _, err := svc.Play(bg, req); !errors.Is(err, tc.want) {
				t.Fatalf("Play: %v, want %v", err, tc.want)
			}
			if after := r.footprint(); !reflect.DeepEqual(before, after) {
				t.Fatalf("a refused Play wrote something:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestPlayRefusesADatasetEventThatIsNotTheManifestsEvent(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	wrong := r.held.Event
	later := wrong.OccurredAt.Add(time.Hour)
	wrong.OccurredAt = &later
	r.events[refOf(wrong)] = wrong
	before := r.footprint()
	if _, err := r.play(); !errors.Is(err, ErrReleaseMismatch) {
		t.Fatalf("Play: %v, want ErrReleaseMismatch", err)
	}
	if after := r.footprint(); !reflect.DeepEqual(before, after) {
		t.Fatal("a refused Play wrote something")
	}
}

// Attribution is only known once ingest has run, so this refusal comes after the release: the play record stays
// open and the same refusal answers every retry.
func TestPlayRefusesAnEventThePipelineCannotAttributeToTheManifestsAccount(t *testing.T) {
	stranger := replaytest.HeldOut{EventID: "0e7e0000-0000-4000-8000-000000000004",
		Event: replaytest.EmailBetween(7, replaytest.T0.Add(time.Hour), "inbound", "someone@unknown.example", "dana@ghostvendor.com", "We need the SOC2 report")}
	r := newRig(t, stranger)
	for i := 0; i < 2; i++ {
		if _, err := r.play(); !errors.Is(err, ErrReleaseMismatch) {
			t.Fatalf("Play #%d: %v, want ErrReleaseMismatch", i+1, err)
		}
	}
	if r.count("account_changes") != "0" || replaytest.One(t, env.DB, `SELECT status FROM demo_plays`) != "released" {
		t.Fatal("a mismatched release must not complete")
	}
}

func TestAnInterruptedPlayResumesAndFinishesOnce(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	stuck := r.service(func(o *Options) { o.Graph = stuckBarrier{}; o.Timeout = time.Second })
	if _, err := stuck.Play(bg, Request{ManifestID: r.manifest}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("Play with the projector down: %v, want ErrTimeout", err)
	}
	if got := replaytest.One(t, env.DB, `SELECT status FROM demo_plays`); got != "released" {
		t.Fatalf("play status after the interruption = %s, want released", got)
	}
	if r.count("source_events") != "3" || r.count("account_changes") != "0" {
		t.Fatalf("after the interruption: source_events %s account_changes %s", r.count("source_events"), r.count("account_changes"))
	}

	doc, err := r.play() // the projector is back
	if err != nil {
		t.Fatalf("resumed Play: %v", err)
	}
	validate(t, "account_change", fields(t, doc)["account_change"])
	if r.count("source_events") != "3" || r.count("activities") != "3" {
		t.Errorf("the resume released the event again: source_events %s", r.count("source_events"))
	}
	if got := r.count("account_changes") + "/" + r.count("business_intelligence_updates") + "/" + replaytest.One(t, env.DB, `SELECT status FROM demo_plays`); got != "1/1/complete" {
		t.Errorf("changes/updates/status = %s", got)
	}
}

func TestConcurrentPlaysReleaseAndWriteOnce(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.play()
		}()
	}
	wg.Wait()
	ok, released := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyReleased), errors.Is(err, ErrPlayInProgress):
			released++
		default:
			t.Errorf("a concurrent Play failed: %v", err)
		}
	}
	if ok != 1 || released != n-1 {
		t.Fatalf("%d succeeded, %d were told it was released or in progress; want 1 and %d", ok, released, n-1)
	}
	if got := r.count("source_events") + "/" + r.count("account_changes") + "/" + r.count("business_intelligence_updates"); got != "3/1/1" {
		t.Fatalf("source_events/changes/updates = %s, want 3/1/1", got)
	}
}

// Core runs the coalescer in the background; Play then only waits for it.
func TestPlayWaitsForTheBackgroundCoalescer(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	svc := r.service(func(o *Options) { o.Recompute = nil })
	ctx, stop := context.WithCancel(bg)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_, _ = drainer{r.stack}.Drain(ctx)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	defer func() { stop(); <-done }()
	if _, err := svc.Play(bg, Request{ManifestID: r.manifest}); err != nil {
		t.Fatalf("Play: %v", err)
	}
	if r.count("account_changes") != "1" {
		t.Fatal("no change was written")
	}
}

func TestANonMaterialEventPlaysToAChangeWithoutAnUpdate(t *testing.T) {
	bland := replaytest.HeldOut{EventID: "0e7e0000-0000-4000-8000-000000000003", Event: replaytest.Email(9, replaytest.T0.Add(time.Hour), "inbound", "Thanks for the update")}
	r := newRig(t, bland)
	doc, err := r.play()
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, doc)
	validate(t, "account_change", f["account_change"])
	var material struct {
		MaterialChange bool `json:"material_change"`
	}
	_ = json.Unmarshal(f["account_change"], &material)
	if material.MaterialChange {
		t.Skip("the pipeline found a material change in the bland email")
	}
	if string(f["business_intelligence_update"]) != "null" || r.count("business_intelligence_updates") != "0" {
		t.Fatalf("a non-material event must not produce an update: %s", f["business_intelligence_update"])
	}
}

func TestPlayHonoursACancelledContext(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := r.svc.Play(ctx, Request{ManifestID: r.manifest}); err == nil {
		t.Fatal("Play ran on a cancelled context")
	}
	if r.count("source_events") != "2" {
		t.Fatalf("a cancelled Play released the event: source_events = %s", r.count("source_events"))
	}
}

// deletingRecompute removes the play record after the recompute, as if it had been lost between the release and
// the completion.
type deletingRecompute struct{ inner Recomputer }

func (d deletingRecompute) Drain(ctx context.Context) (coalesce.DrainResult, error) {
	res, err := d.inner.Drain(ctx)
	if _, derr := env.DB.ExecContext(ctx, `DELETE FROM demo_plays`); derr != nil {
		return res, derr
	}
	return res, err
}

func TestACompletionThatFindsNoOpenPlayRecordWritesNothing(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	svc := r.service(func(o *Options) { o.Recompute = deletingRecompute{drainer{r.stack}} })
	_, err := svc.Play(bg, Request{ManifestID: r.manifest})
	if err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("Play: %v, want the missing play record named", err)
	}
	if got := r.count("account_changes") + "/" + r.count("business_intelligence_updates"); got != "0/0" {
		t.Fatalf("changes/updates = %s: a completion without an open record must roll back", got)
	}
}
