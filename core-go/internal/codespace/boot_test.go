package codespace

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

type bootRig struct {
	*rig
	boot     Boot
	started  []string
	failOn   string
	failDown error
	prepared int
}

func newBootRig(t *testing.T) *bootRig {
	t.Helper()
	r := newRig(t)
	r.seeded()
	b := &bootRig{rig: r}
	cfg := testBase()
	cfg.Layout = r.ops.Paths.Layout
	b.boot = Boot{
		Cfg: cfg, Ops: r.ops, Out: &bytes.Buffer{},
		Prepare: func(context.Context) (demorun.Tooling, error) {
			b.prepared++
			return demorun.Tooling{Core: "core", Host: "host", Python: "python", Npm: "npm", Slackbot: "slackbot"}, nil
		},
		ControlSpec: func(demorun.Tooling) demorun.Spec { return demorun.Spec{Name: "control"} },
		Down: func(_ context.Context, _ demorun.Supervisor, specs []demorun.Spec) error {
			for i := len(specs) - 1; i >= 0; i-- {
				b.plat.events = append(b.plat.events, "down:"+specs[i].Name)
			}
			return b.failDown
		},
		Up: func(_ context.Context, _ demorun.Supervisor, specs []demorun.Spec) error {
			for _, s := range specs {
				if s.Name == b.failOn {
					return &demorun.ServiceError{Service: s.Name, Phase: "health", Err: errors.New("did not answer")}
				}
				b.started = append(b.started, s.Name)
				b.plat.events = append(b.plat.events, "up:"+s.Name)
			}
			return nil
		},
	}
	return b
}

func TestBootResetsTheDemoThenStartsTheSurfacesOnCaseOne(t *testing.T) {
	b := newBootRig(t)
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The control service is restarted first (a new boot, a new token); stores next; the surfaces stop before the reset (the bot's relay state and the web's pages are from the old world),
	// both cases are restored and checked, case 1 last, then the surfaces start on case 1.
	want := "down:control,up:control,up:neo4j,up:postgres,up:worker,down:web,down:slackbot," +
		"stop-core,stop-core,rebuild:case2,start-core:case2,stop-core,rebuild:case1,start-core:case1,up:slackbot,up:web"
	if got := strings.Join(b.plat.events, ","); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	if b.prepared != 1 {
		t.Fatalf("prepare ran %d times", b.prepared)
	}
	if !strings.Contains(b.boot.Out.(*bytes.Buffer).String(), "All systems ready") {
		t.Fatalf("output = %s", b.boot.Out.(*bytes.Buffer))
	}
	if b.ops.Paths.ReadBootError() != "" {
		t.Fatal("a successful boot leaves no error")
	}
}

// Start always resets (product owner): whatever case and played state a previous session left, the audience gets MedTech at Event N-1.
func TestBootStartDiscardsAPlayedWorldAndTheRememberedCase(t *testing.T) {
	b := newBootRig(t)
	_ = WriteMarker(b.ops.Paths.ActiveFile(), SlotCase2)
	_ = WriteMarker(b.ops.Paths.GraphMarker(SlotCase2), SlotCase2)
	b.admin.dbs["ghost_case1"] = 7 // Event N was played, knowledge formed
	b.admin.dbs["ghost_case2"] = 8
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.admin.dbs["ghost_case1"] != 0 || b.admin.dbs["ghost_case2"] != 0 {
		t.Fatalf("both cases must be restored from their templates: %v", b.admin.dbs)
	}
	if active, _ := ReadMarker(b.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q, want case1", active)
	}
}

func TestBootStopsWhenTheSurfacesCannotBeStoppedForTheReset(t *testing.T) {
	b := newBootRig(t)
	b.failDown = errors.New("web will not stop")
	if err := b.boot.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "web will not stop") {
		t.Fatalf("err = %v", err)
	}
	if contains(b.plat.events, "stop-core") {
		t.Fatalf("nothing may be restored while the old surfaces still run: %v", b.plat.events)
	}
}

func TestBootRecordsTheFailedServiceForTheStatusAndClearsItOnTheNextSuccess(t *testing.T) {
	b := newBootRig(t)
	b.failOn = "worker"
	err := b.boot.Run(context.Background())
	var se *demorun.ServiceError
	if !errors.As(err, &se) || se.Service != "worker" {
		t.Fatalf("err = %v", err)
	}
	if got := b.ops.Paths.ReadBootError(); got != "worker failed to health: did not answer" {
		t.Fatalf("boot error = %q", got)
	}
	for _, name := range b.started {
		if name == "core" || name == "web" {
			t.Fatalf("nothing after the failed service may start: %v", b.started)
		}
	}
	b.failOn = ""
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := b.ops.Paths.ReadBootError(); got != "" {
		t.Fatalf("the recorded failure must be cleared on success: %q", got)
	}
}

func TestBootStopsWhenPrepareControlActivateOrASurfaceFails(t *testing.T) {
	b := newBootRig(t)
	b.boot.Prepare = func(context.Context) (demorun.Tooling, error) {
		return demorun.Tooling{}, errors.New("go build failed")
	}
	if err := b.boot.Run(context.Background()); err == nil || b.ops.Paths.ReadBootError() != "go build failed" {
		t.Fatalf("prepare: %v / %q", err, b.ops.Paths.ReadBootError())
	}
	b = newBootRig(t)
	b.failOn = "control"
	if err := b.boot.Run(context.Background()); err == nil {
		t.Fatal("a control failure must stop the boot")
	}
	b = newBootRig(t)
	b.plat.invisible["man-1"] = "leaked"
	if err := b.boot.Run(context.Background()); err == nil || !strings.Contains(b.ops.Paths.ReadBootError(), "Event N is visible") {
		t.Fatalf("a leaked world must stop the boot and say why: %v / %q", err, b.ops.Paths.ReadBootError())
	}
	b = newBootRig(t)
	b.failOn = "web"
	if err := b.boot.Run(context.Background()); err == nil || !strings.HasPrefix(b.ops.Paths.ReadBootError(), "web failed") {
		t.Fatalf("a surface failure: %v / %q", err, b.ops.Paths.ReadBootError())
	}
}

func TestBootWithoutSlackSkipsTheBot(t *testing.T) {
	b := newBootRig(t)
	b.boot.Cfg.NoSlack = true
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range b.started {
		if n == "slackbot" {
			t.Fatalf("slackbot started with NoSlack: %v", b.started)
		}
	}
}
