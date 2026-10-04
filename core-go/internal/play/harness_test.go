package play

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var bg = context.Background()

// fakeProbe is Neo4j: it holds hits and counts the ids it was asked about.
type fakeProbe struct {
	mu        sync.Mutex
	hits      []ctxgraph.Hit
	laterHits []ctxgraph.Hit // Activity nodes at or after the cutoff
	err       error
	calls     [][]string
	laterFrom []time.Time
}

func (p *fakeProbe) ActivitiesFrom(_ context.Context, _, _ string, cutoff time.Time) ([]ctxgraph.Hit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.laterFrom = append(p.laterFrom, cutoff)
	return p.laterHits, p.err
}

func (p *fakeProbe) Referencing(_ context.Context, ids []string) ([]ctxgraph.Hit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, append([]string(nil), ids...))
	return p.hits, p.err
}

func (p *fakeProbe) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// projectingBarrier is the graph barrier with a projector behind it: Wait completes the account's jobs and
// records their diffs, as the real projector does between the commit of a recompute and the barrier opening.
type projectingBarrier struct {
	t  *testing.T
	mu sync.Mutex
}

func (b *projectingBarrier) Wait(context.Context, string, time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	replaytest.FakeProjector{DB: env.DB}.Complete(b.t)
	return nil
}

// stuckBarrier never opens: the projector is down.
type stuckBarrier struct{}

func (stuckBarrier) Wait(ctx context.Context, _ string, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

// countingIngest counts what reaches the ingest path.
type countingIngest struct {
	inner Ingester
	mu    sync.Mutex
	n     int
}

func (c *countingIngest) Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.inner.Ingest(ctx, ev)
}

func (c *countingIngest) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// mapSource is the replay dataset.
type mapSource map[SourceRef]normalize.SourceEvent

func (m mapSource) Lookup(_ context.Context, ref SourceRef) (normalize.SourceEvent, error) {
	if ev, ok := m[ref]; ok {
		return ev, nil
	}
	return normalize.SourceEvent{}, ErrEventNotInSource
}

func refOf(ev normalize.SourceEvent) SourceRef {
	return SourceRef{System: ev.SourceSystem, ObjectID: ev.SourceObjectID, EventKey: ev.SourceEventKey}
}

// drainer runs the recompute the way the background coalescer would, past the debounce window.
type drainer struct{ stack *replaytest.Stack }

func (d drainer) Drain(ctx context.Context) (coalesce.DrainResult, error) {
	d.stack.CoalClock.Set(d.stack.CoalClock.Now().Add(replaytest.Debounce + time.Second))
	return d.stack.Coalescer.Drain(ctx)
}

// rig is a world with two history emails, a manifest whose held-out event is the SOC2 email, the real
// pipeline, and the fakes for Neo4j.
type rig struct {
	t        *testing.T
	world    replaytest.World
	stack    *replaytest.Stack
	held     replaytest.HeldOut
	manifest string
	probe    *fakeProbe
	ingest   *countingIngest
	events   mapSource
	svc      *Service
}

func newRig(t *testing.T, held replaytest.HeldOut) *rig {
	t.Helper()
	r := &rig{t: t, held: held, probe: &fakeProbe{}}
	r.world = replaytest.SeedWorld(t, env.DB)
	r.stack = replaytest.NewStack(t, env.DB)
	r.stack.History(t, 2)
	r.manifest = replaytest.InsertManifest(t, env.DB, r.world, held)
	r.ingest = &countingIngest{inner: r.stack.Ingest}
	r.events = mapSource{refOf(held.Event): held.Event}
	r.svc = r.service(func(o *Options) {})
	return r
}

// service builds a Service over the rig, with mutate adjusting the options.
func (r *rig) service(mutate func(*Options)) *Service {
	r.t.Helper()
	o := Options{DB: env.DB, Ingest: r.ingest, Events: r.events, Recompute: drainer{r.stack}, Graph: &projectingBarrier{t: r.t},
		Probe: r.probe, Clock: clock.NewFixed(replaytest.T0.Add(2 * time.Hour)), Poll: 5 * time.Millisecond, Timeout: 30 * time.Second}
	mutate(&o)
	svc, err := NewService(o)
	if err != nil {
		r.t.Fatal(err)
	}
	return svc
}

func (r *rig) play() ([]byte, error) {
	return r.svc.Play(bg, Request{ManifestID: r.manifest})
}

func (r *rig) count(table string) string {
	return replaytest.One(r.t, env.DB, `SELECT count(*)::text FROM `+table)
}

// footprint counts every table Play can write, so "changes nothing" is a comparison of two snapshots.
func (r *rig) footprint() map[string]string {
	out := map[string]string{}
	for _, table := range []string{"source_events", "activities", "claims", "account_state", "state_history", "state_diffs", "signals",
		"trigger_evaluations", "agent_runs", "recompute_jobs", "graph_projection_jobs", "graph_projection_diffs", "account_changes",
		"business_intelligence_updates", "demo_plays", "state_transitions"} {
		out[table] = r.count(table)
	}
	out["state_version"] = replaytest.One(r.t, env.DB, `SELECT version::text FROM account_state`)
	return out
}

func validate(t *testing.T, schema string, doc []byte) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(schema, doc); err != nil {
		t.Fatalf("%s violates its schema: %v\n%s", schema, err, doc)
	}
}

func fields(t *testing.T, doc []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(doc, &m); err != nil {
		t.Fatalf("%v\n%s", err, doc)
	}
	return m
}
