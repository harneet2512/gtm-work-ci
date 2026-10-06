package codespace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// fakeAdmin is an in-memory cluster: database name -> a generation counter. Only the one-time setup uses an Admin.
type fakeAdmin struct {
	dbs   map[string]int
	calls []string
	fail  map[string]error // verb:name -> error
}

func newFakeAdmin() *fakeAdmin { return &fakeAdmin{dbs: map[string]int{}, fail: map[string]error{}} }

func (a *fakeAdmin) record(verb, name string) error {
	a.calls = append(a.calls, verb+":"+name)
	return a.fail[verb+":"+name]
}
func (a *fakeAdmin) Exists(_ context.Context, name string) (bool, error) {
	_, ok := a.dbs[name]
	return ok, a.record("exists", name)
}
func (a *fakeAdmin) Create(_ context.Context, name string) error {
	if _, ok := a.dbs[name]; ok {
		return fmt.Errorf("database %s already exists", name)
	}
	a.dbs[name] = 0
	return a.record("create", name)
}
func (a *fakeAdmin) Drop(_ context.Context, name string) error {
	delete(a.dbs, name)
	return a.record("drop", name)
}
func (a *fakeAdmin) Clone(_ context.Context, target, source string) error {
	gen, ok := a.dbs[source]
	if !ok {
		return fmt.Errorf("template %s does not exist", source)
	}
	a.dbs[target] = gen
	return a.record("clone", target+"<-"+source)
}

// historyLen is how many history events each case's frozen manifest holds (Event N is the next position).
var historyLen = map[string]int{SlotCase1: 12, SlotCase2: 14}

// world is the demo's mutable state as plain files under the live copy: what a case's database processed (events and
// knowledge) and its graph. Play changes it; the sealed baseline must bring it back exactly.
type world struct {
	live, home string
	cases      []Case
	// pipelineRuns and modelCalls count the work that only a Play does. Start, Reset and the handoff must leave them at zero.
	pipelineRuns, modelCalls int
}

type caseRows struct {
	Events    []int
	Knowledge []string
}

func (w *world) path(rel string) string { return filepath.Join(w.live, filepath.FromSlash(rel)) }

func (w *world) put(t *testing.T, rel string, v any) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := os.MkdirAll(filepath.Dir(w.path(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.path(rel), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *world) rows(t *testing.T, slot string) caseRows {
	t.Helper()
	var r caseRows
	for rel, into := range map[string]any{"pg/data/events-" + slot + ".json": &r.Events, "pg/data/knowledge-" + slot + ".json": &r.Knowledge} {
		raw, err := os.ReadFile(w.path(rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// build lays out the sealed-at-N-1 world: history events 1..N-1 processed, Event N held out, knowledge from the history.
func (w *world) build(t *testing.T) {
	t.Helper()
	for i, c := range w.cases {
		n := historyLen[c.Slot]
		var events []map[string]any
		var done []int
		var knowledge []string
		for p := 1; p <= n; p++ {
			events = append(events, map[string]any{"event": map[string]any{"replay_position": p}})
			done = append(done, p)
			knowledge = append(knowledge, fmt.Sprintf("k-%s-%d", c.Slot, p))
		}
		w.put(t, "cases/"+c.Slot+"/manifest.json", map[string]any{"events": events,
			"held_out_event": map[string]any{"event_id": "held-" + c.Slot, "replay_position": n + 1}})
		st := demorun.DemoState{ManifestID: fmt.Sprintf("man-%d", i+1), AccountID: fmt.Sprintf("acct-%d", i+1), OpportunityID: c.OpportunityID,
			CaseName: c.Label, HeldOutEventID: "held-" + c.Slot}
		if err := demorun.SaveState(filepath.Join(w.live, "cases", c.Slot, "state.json"), st); err != nil {
			t.Fatal(err)
		}
		w.put(t, "pg/data/events-"+c.Slot+".json", done)
		w.put(t, "pg/data/knowledge-"+c.Slot+".json", knowledge)
		graph := "neo4j/data/graph"
		if i > 0 {
			graph = fmt.Sprintf("neo4j-case%d/data/graph", i+1)
		}
		w.put(t, graph, map[string]int{"nodes": 100 * (i + 1)})
		if err := WriteMarker(w.path("graph-"+c.Slot), c.Slot); err != nil {
			t.Fatal(err)
		}
	}
	w.put(t, "pg/data/PG_VERSION", 16)
	if err := os.MkdirAll(w.path("replay-events"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.put(t, "replay-events/events.json", []string{"held-case1", "held-case2"})
	if err := WriteMarker(w.path("active-case"), SlotCase1); err != nil {
		t.Fatal(err)
	}
}

// play is Event N going through the real pipeline: it processes the event, forms knowledge, releases the event, and uses a model.
func (w *world) play(t *testing.T, slot string) {
	t.Helper()
	w.pipelineRuns++
	w.modelCalls++
	r := w.rows(t, slot)
	w.put(t, "pg/data/events-"+slot+".json", append(r.Events, historyLen[slot]+1))
	w.put(t, "pg/data/knowledge-"+slot+".json", append(r.Knowledge, "k-new-"+slot))
	w.put(t, "pg/data/played-"+slot, true)
	w.put(t, "neo4j/data/graph", map[string]int{"nodes": 999})
}

// release is core answering Play for the case that holds this opportunity: Event N is no longer withheld.
func (w *world) release(opportunityID string) {
	for _, c := range w.cases {
		if c.OpportunityID == opportunityID {
			_ = os.WriteFile(w.path("pg/data/played-"+c.Slot), []byte("true"), 0o644)
		}
	}
}

func (w *world) released(slot string) bool {
	_, err := os.Stat(w.path("pg/data/played-" + slot))
	return err == nil
}

// fakePlatform records core and store operations and answers the invisibility of whatever manifest it is asked.
type fakePlatform struct {
	events    []string
	invisible map[string]string // manifest id -> status; overrides what the world says
	w         *world
	failStart error
	rebuilds  int // RebuildGraph calls: only the one-time setup may make any
	failStop  error
}

func (p *fakePlatform) StopCore(context.Context) error {
	p.events = append(p.events, "stop-core")
	return nil
}
func (p *fakePlatform) StartCore(_ context.Context, c Case) error {
	p.events = append(p.events, "start-core:"+c.Slot)
	return p.failStart
}
func (p *fakePlatform) StopStores(context.Context) error {
	p.events = append(p.events, "stop-stores")
	return p.failStop
}
func (p *fakePlatform) StartStores(context.Context) error {
	p.events = append(p.events, "start-stores")
	return nil
}
func (p *fakePlatform) RebuildGraph(_ context.Context, c Case) error {
	p.rebuilds++
	p.events = append(p.events, "rebuild:"+c.Slot)
	return nil
}
func (p *fakePlatform) Invisibility(_ context.Context, id string) (demorun.Invisibility, error) {
	status := "withheld"
	if s, ok := p.invisible[id]; ok {
		status = s
	} else if p.w != nil {
		for i, c := range p.w.cases {
			if id == fmt.Sprintf("man-%d", i+1) && p.w.released(c.Slot) {
				status = "released"
			}
		}
	}
	inv := demorun.Invisibility{ManifestID: id, Status: status}
	if status == "leaked" {
		inv.Leaks = []demorun.Leak{{Store: "postgres", Kind: "claim", ID: "x"}}
	}
	return inv, nil
}

type rig struct {
	t     *testing.T
	ops   Ops
	admin *fakeAdmin
	plat  *fakePlatform
	w     *world
	home  string
	// carries counts knowledge carries (they touch only the database).
	carries int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	home := t.TempDir()
	l := demorun.NewLayout(home)
	l.StateDir = filepath.Join(home, "state")
	l.Live = filepath.Join(l.StateDir, "live")
	cases := DefaultCases()
	w := &world{live: l.Live, home: l.StateDir, cases: cases}
	r := &rig{t: t, admin: newFakeAdmin(), plat: &fakePlatform{invisible: map[string]string{}, w: w}, w: w, home: l.StateDir}
	r.ops = Ops{Cases: cases, Paths: Paths{Layout: l}, Admin: r.admin, P: r.plat, Base: r.baseline(),
		Carry: func(context.Context, Case, Case) (int, error) { r.carries++; return 0, nil }}
	return r
}

func (r *rig) baseline() Baseline {
	return Baseline{Home: r.home, Cases: []string{SlotCase1, SlotCase2}, Retries: 2, Backoff: time.Millisecond,
		Paths: []string{"live/pg/data", "live/neo4j/data", "live/neo4j-case2/data", "live/cases", "live/replay-events",
			"live/active-case", "live/graph-case1", "live/graph-case2"}}
}

// seeded is the world after the one-time setup: both cases frozen at Event N-1, their graphs built, the baseline sealed.
func (r *rig) seeded() {
	r.t.Helper()
	r.w.build(r.t)
	for _, c := range r.ops.Cases {
		r.admin.dbs[c.Database] = 1
	}
	if _, err := r.ops.Base.(Baseline).Seal(context.Background()); err != nil {
		r.t.Fatal(err)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func joined(events []string) string { return strings.Join(events, ",") }
