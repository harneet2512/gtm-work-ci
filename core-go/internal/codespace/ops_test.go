package codespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// fakeAdmin is an in-memory cluster: database name -> a generation counter that Clone copies, so a restore is
// observable.
type fakeAdmin struct {
	dbs    map[string]int
	calls  []string
	fail   map[string]error  // verb:name -> error
	onDrop func(name string) // a restore puts a world back: tests use it to reset what a fake world remembers
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
	if a.onDrop != nil {
		a.onDrop(name)
	}
	return a.record("drop", name)
}
func (a *fakeAdmin) Clone(_ context.Context, target, source string) error {
	gen, ok := a.dbs[source]
	if !ok {
		return fmt.Errorf("template %s does not exist", source)
	}
	if _, ok := a.dbs[target]; ok {
		return fmt.Errorf("database %s already exists", target)
	}
	a.dbs[target] = gen
	return a.record("clone", target+"<-"+source)
}

// fakePlatform records core and graph operations and answers the invisibility of whatever manifest it is asked.
type fakePlatform struct {
	events      []string
	invisible   map[string]string // manifest id -> status (default withheld)
	failRebuild error
	failStart   error
}

func (p *fakePlatform) StopCore(context.Context) error {
	p.events = append(p.events, "stop-core")
	return nil
}
func (p *fakePlatform) StartCore(_ context.Context, c Case) error {
	p.events = append(p.events, "start-core:"+c.Slot)
	return p.failStart
}
func (p *fakePlatform) RebuildGraph(_ context.Context, c Case) error {
	p.events = append(p.events, "rebuild:"+c.Slot)
	return p.failRebuild
}
func (p *fakePlatform) Invisibility(_ context.Context, id string) (demorun.Invisibility, error) {
	status := "withheld"
	if s, ok := p.invisible[id]; ok {
		status = s
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
	root  string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	l := demorun.NewLayout(root)
	l.StateDir = filepath.Join(root, "state")
	r := &rig{t: t, admin: newFakeAdmin(), plat: &fakePlatform{invisible: map[string]string{}}, root: root}
	r.ops = Ops{Cases: DefaultCases(), Paths: Paths{Layout: l}, Admin: r.admin, P: r.plat}
	return r
}

// seeded marks both cases as seeded (state file, database and template exist), the world after the first-time setup.
func (r *rig) seeded() {
	r.t.Helper()
	for i, c := range r.ops.Cases {
		st := demorun.DemoState{ManifestID: fmt.Sprintf("man-%d", i+1), AccountID: fmt.Sprintf("acct-%d", i+1), OpportunityID: c.OpportunityID, CaseName: c.Label}
		if err := demorun.SaveState(r.ops.Paths.State(c.Slot), st); err != nil {
			r.t.Fatal(err)
		}
		r.admin.dbs[c.Database], r.admin.dbs[c.Template()] = 0, 0
	}
}

func TestActivateStopsCoreRebuildsTheGraphOnceAndStartsCoreOnTheCase(t *testing.T) {
	r := newRig(t)
	r.seeded()
	status, err := r.ops.Activate(context.Background(), SlotCase2)
	if err != nil || status != "withheld" {
		t.Fatalf("Activate = %q, %v", status, err)
	}
	if got := strings.Join(r.plat.events, ","); got != "stop-core,rebuild:case2,start-core:case2" {
		t.Fatalf("events = %s", got)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase2 {
		t.Fatalf("active = %q", active)
	}
	// The graph already holds case 2: a second activation must not rebuild it.
	r.plat.events = nil
	if _, err := r.ops.Activate(context.Background(), SlotCase2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.plat.events, ","); got != "stop-core,start-core:case2" {
		t.Fatalf("events = %s, want no rebuild when the graph already holds the case", got)
	}
	// Switching to the other case rebuilds.
	r.plat.events = nil
	if _, err := r.ops.Activate(context.Background(), SlotCase1); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.plat.events, ","); got != "stop-core,rebuild:case1,start-core:case1" {
		t.Fatalf("events = %s", got)
	}
}

func TestActivateRefusesAnUnseededCaseAndAnUnknownOne(t *testing.T) {
	r := newRig(t)
	if _, err := r.ops.Activate(context.Background(), SlotCase1); err == nil || !strings.Contains(err.Error(), "not seeded") {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.ops.Activate(context.Background(), "case9"); err == nil {
		t.Fatal("unknown slot accepted")
	}
	if len(r.plat.events) != 0 {
		t.Fatalf("nothing may be touched: %v", r.plat.events)
	}
}

func TestActivateRefusesALeakedWorldAndDoesNotRecordItAsActive(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.plat.invisible["man-1"] = "leaked"
	_, err := r.ops.Activate(context.Background(), SlotCase1)
	if err == nil || !strings.Contains(err.Error(), "Event N is visible before Play") {
		t.Fatalf("err = %v", err)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != "" {
		t.Fatalf("a leaked case must not become active: %q", active)
	}
}

func TestActivateDistrustsTheGraphMarkerAfterAFailedRebuild(t *testing.T) {
	r := newRig(t)
	r.seeded()
	if err := WriteMarker(r.ops.Paths.GraphMarker(SlotCase2), SlotCase1); err != nil { // not the case's own name: untrusted
		t.Fatal(err)
	}
	r.plat.failRebuild = errors.New("neo4j down")
	if _, err := r.ops.Activate(context.Background(), SlotCase2); err == nil {
		t.Fatal("a failed rebuild must fail the activation")
	}
	if g, _ := ReadMarker(r.ops.Paths.GraphMarker(SlotCase2)); g != "" {
		t.Fatalf("the graph may be half-built; the marker must be cleared, got %q", g)
	}
	r.plat.failRebuild, r.plat.failStart = nil, errors.New("core will not start")
	if _, err := r.ops.Activate(context.Background(), SlotCase2); err == nil || !strings.Contains(err.Error(), "core will not start") {
		t.Fatalf("err = %v", err)
	}
}

func TestResetAllRestoresBothDatabasesFromTheirTemplatesAndEndsOnCaseOne(t *testing.T) {
	r := newRig(t)
	r.seeded()
	// Event N was played in case 1 (its database moved on), the templates did not.
	r.admin.dbs["ghost_case1"] = 7
	r.admin.dbs["ghost_case1_frozen"] = 3
	r.admin.dbs["ghost_case2_frozen"] = 5
	var steps []string
	if err := r.ops.ResetAll(context.Background(), func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatalf("ResetAll: %v", err)
	}
	if r.admin.dbs["ghost_case1"] != 3 || r.admin.dbs["ghost_case2"] != 5 {
		t.Fatalf("databases = %v, want each restored to its template's generation", r.admin.dbs)
	}
	if active, _ := ReadMarker(r.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q, want case1", active)
	}
	if got := strings.Join(r.plat.events, ","); got != "stop-core,stop-core,rebuild:case2,start-core:case2,stop-core,rebuild:case1,start-core:case1" {
		t.Fatalf("events = %s: both graphs must be rebuilt, case 1 last", got)
	}
	if last := steps[len(steps)-1]; last != "Reset complete" {
		t.Fatalf("steps = %v", steps)
	}
	for _, want := range []string{"Restoring MedTech Advances to Event N-1", "Restoring EcoLite Innovations to Event N-1", "Checking EcoLite Innovations"} {
		if !contains(steps, want) {
			t.Errorf("steps lack %q: %v", want, steps)
		}
	}
	// Twice in a row is the same result (deterministic rehearsal reset).
	if err := r.ops.ResetAll(context.Background(), nil); err != nil || r.admin.dbs["ghost_case1"] != 3 {
		t.Fatalf("second reset: %v %v", err, r.admin.dbs)
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

func TestResetAllRefusesBeforeTouchingAnythingWhenACaseOrTemplateIsMissing(t *testing.T) {
	r := newRig(t)
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "never seeded") {
		t.Fatalf("unseeded: %v", err)
	}
	r.seeded()
	delete(r.admin.dbs, "ghost_case2_frozen")
	err := r.ops.ResetAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "template") || !strings.Contains(err.Error(), "ghost_case2_frozen") {
		t.Fatalf("missing template: %v", err)
	}
	if len(r.plat.events) != 0 || r.admin.dbs["ghost_case1"] != 0 {
		t.Fatalf("a refused reset must not stop core or drop a database: %v %v", r.plat.events, r.admin.calls)
	}
}

func TestResetAllFailsWhenACaseIsStillNotWithheldAfterTheRestore(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.plat.invisible["man-2"] = "released"
	err := r.ops.ResetAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), `want withheld`) {
		t.Fatalf("err = %v", err)
	}
	r.plat.invisible["man-2"] = "leaked"
	if err := r.ops.ResetAll(context.Background(), nil); err == nil {
		t.Fatal("a leaked restore must fail")
	}
}

func TestResetAllStopsOnAnAdminFailure(t *testing.T) {
	r := newRig(t)
	r.seeded()
	r.admin.fail["drop:ghost_case1"] = errors.New("cannot drop")
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "cannot drop") {
		t.Fatalf("err = %v", err)
	}
	r.admin.fail = map[string]error{}
	r.admin.fail["clone:ghost_case1<-ghost_case1_frozen"] = errors.New("cannot clone")
	if err := r.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "cannot clone") {
		t.Fatalf("err = %v", err)
	}
}

func TestSeedStateIgnoresAnIncompleteStateFile(t *testing.T) {
	r := newRig(t)
	if err := demorun.SaveState(r.ops.Paths.State(SlotCase1), demorun.DemoState{CaseName: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.ops.SeedState(SlotCase1); ok || err != nil {
		t.Fatalf("a state without a manifest id is not a seed: ok=%v err=%v", ok, err)
	}
	if err := os.MkdirAll(r.ops.Paths.CaseDir(SlotCase2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.ops.Paths.State(SlotCase2), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.ops.SeedState(SlotCase2); err == nil {
		t.Fatal("a corrupt state file must be an error")
	}
}
