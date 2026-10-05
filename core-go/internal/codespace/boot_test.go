package codespace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// The control service is restarted first (a new boot, a new token); the worker next; the surfaces stop before the restore (the bot's relay state and the web's pages are from the old world),
	// the stores stop, the baseline replaces the live copy, the stores restart, both cases are checked, case 1 last, then the surfaces start on case 1.
	want := "down:control,up:control,up:worker,down:web,down:slackbot," +
		"stop-core,stop-stores,start-stores,stop-core,start-core:case2,stop-core,start-core:case1,up:slackbot,up:web"
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

// The default Start restores the baseline (product owner): whatever case and played state a previous session left, the audience gets
// MedTech at Event N-1. It never rebuilds a graph, migrates, carries or calls a model.
func TestBootStartRestoresTheBaselineAndNeverRebuildsOrCallsAModel(t *testing.T) {
	b := newBootRig(t)
	want := fingerprintOf(t, b.w.live)
	b.played()
	b.admin.calls = nil
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintOf(t, b.w.live); got != want {
		t.Fatalf("Start must leave the live copy equal to the baseline: %+v vs %+v", got, want)
	}
	if active, _ := ReadMarker(b.ops.Paths.ActiveFile()); active != SlotCase1 {
		t.Fatalf("active = %q, want case1", active)
	}
	if b.plat.rebuilds != 0 || b.carries != 0 || b.w.modelCalls != 0 || b.w.pipelineRuns != 0 || len(b.admin.calls) != 0 {
		t.Fatalf("rebuilds=%d carries=%d model calls=%d pipeline runs=%d database calls=%v", b.plat.rebuilds, b.carries, b.w.modelCalls, b.w.pipelineRuns, b.admin.calls)
	}
}

func TestBootResumeKeepsTheLiveStateAndNeverRebuilds(t *testing.T) {
	b := newBootRig(t)
	b.played()
	want := fingerprintOf(t, b.w.live)
	b.boot.Mode = StartResume
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintOf(t, b.w.live); got != want {
		t.Fatalf("resume must keep the live copy: %+v vs %+v", got, want)
	}
	if contains(b.plat.events, "stop-stores") || b.plat.rebuilds != 0 || b.w.modelCalls != 0 {
		t.Fatalf("events = %v", b.plat.events)
	}
	wantEvents := "down:control,up:control,up:worker,down:web,down:slackbot,up:neo4j,up:postgres,stop-core,start-core:case2,up:slackbot,up:web"
	if got := joined(b.plat.events); got != wantEvents {
		t.Fatalf("events = %s\nwant     %s", got, wantEvents)
	}
}

// Reset always restores: its shortcut passes --mode restore, which beats GHOST_DEMO_START=resume from .env or the environment.
func TestResetEntryPointRestoresEvenWhenTheEnvironmentSaysResume(t *testing.T) {
	b := newBootRig(t)
	want := fingerprintOf(t, b.w.live)
	b.played()
	mode, err := ResolveStartMode("restore", StartResume) // what `ghostctl codespace up --mode restore` computes
	if err != nil || mode != StartRestore {
		t.Fatalf("mode = %q, %v", mode, err)
	}
	b.boot.Mode = mode
	if err := b.boot.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintOf(t, b.w.live); got != want || !contains(b.plat.events, "stop-stores") {
		t.Fatalf("Reset under a resume setting must still restore the baseline: %+v vs %+v", got, want)
	}
}

func TestResolveStartModeLetsTheFlagWinAndRejectsBadValues(t *testing.T) {
	for _, c := range []struct{ flag, env, want string }{
		{"", "", StartRestore}, {"", "resume", StartResume}, {"restore", "resume", StartRestore}, {"resume", "", StartResume}, {" Restore ", "resume", StartRestore},
	} {
		if got, err := ResolveStartMode(c.flag, c.env); err != nil || got != c.want {
			t.Errorf("ResolveStartMode(%q, %q) = %q, %v, want %q", c.flag, c.env, got, err, c.want)
		}
	}
	if _, err := ResolveStartMode("rebuild", ""); err == nil {
		t.Error("an unknown flag value must be rejected")
	}
}

func TestBootRejectsAnUnknownStartModeBeforeTouchingAnything(t *testing.T) {
	b := newBootRig(t)
	b.boot.Mode = "rebuild"
	err := b.boot.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), StartModeEnv) || b.prepared != 0 || len(b.plat.events) != 0 {
		t.Fatalf("err = %v prepared=%d events=%v", err, b.prepared, b.plat.events)
	}
	for in, want := range map[string]string{"": StartRestore, "restore": StartRestore, " Resume ": StartResume} {
		if got, err := ParseStartMode(in); err != nil || got != want {
			t.Errorf("ParseStartMode(%q) = %q, %v", in, got, err)
		}
	}
}

// With no usable baseline, both modes fail with a message to run the setup once, before any build or stop, and never rebuild.
func TestBootWithAMissingOrCorruptBaselineFailsClearlyInBothModes(t *testing.T) {
	for _, mode := range []string{StartRestore, StartResume} {
		for name, damage := range map[string]func(b *bootRig){
			"missing": func(b *bootRig) { _ = os.RemoveAll(b.ops.Base.(Baseline).Dir()) },
			"corrupt": func(b *bootRig) {
				_ = os.Remove(filepath.Join(b.ops.Base.(Baseline).Dir(), "live", "pg", "data", "PG_VERSION"))
			},
		} {
			b := newBootRig(t)
			damage(b)
			b.boot.Mode = mode
			err := b.boot.Run(context.Background())
			if !errors.Is(err, ErrNoBaseline) || !strings.Contains(err.Error(), "one-time setup") {
				t.Fatalf("%s/%s: err = %v", mode, name, err)
			}
			if b.prepared != 0 || len(b.plat.events) != 0 || b.plat.rebuilds != 0 {
				t.Fatalf("%s/%s: nothing may be built, stopped or rebuilt: prepared=%d events=%v", mode, name, b.prepared, b.plat.events)
			}
			if got := b.ops.Paths.ReadBootError(); !strings.Contains(got, "one-time setup") {
				t.Fatalf("%s/%s: the status must name the fix: %q", mode, name, got)
			}
		}
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
