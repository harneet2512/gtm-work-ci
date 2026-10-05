package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// setupRig is the first-time setup over fakes: the real configuration and service plan, no process and no database.
type setupRig struct {
	rt     codespace.Runtime
	events []string
	steps  setupSteps
	fail   map[string]error
}

func newSetupRig(t *testing.T, withCassettes bool) *setupRig {
	t.Helper()
	codespaceRepo(t)
	cassettes := filepath.Join(t.TempDir(), "cassettes")
	if withCassettes {
		if err := os.MkdirAll(cassettes, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GHOST_DEMO_CASSETTES", cassettes)
	rt, closeFlow, err := loadCodespace(io.Discard, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeFlow)
	r := &setupRig{rt: rt, fail: map[string]error{}}
	r.rt.Boot.Prepare = func(context.Context) (demorun.Tooling, error) {
		r.events = append(r.events, "prepare")
		return demorun.Tooling{Core: "core", Host: "host", Python: "python", Npm: "npm", Slackbot: "slackbot"}, r.fail["prepare"]
	}
	names := func(specs []demorun.Spec) string {
		out := make([]string, 0, len(specs))
		for _, s := range specs {
			out = append(out, s.Name)
		}
		return strings.Join(out, "+")
	}
	r.steps = setupSteps{
		up: func(_ context.Context, s []demorun.Spec) error {
			r.events = append(r.events, "up:"+names(s))
			return r.fail["up"]
		},
		down: func(_ context.Context, s []demorun.Spec) error {
			r.events = append(r.events, "down:"+names(s))
			return r.fail["down"]
		},
		seedAll: func(context.Context) error { r.events = append(r.events, "seed"); return r.fail["seed"] },
	}
	return r
}

func TestSetupBuildsStartsTheStoresFreezesBothCasesAndStopsAgain(t *testing.T) {
	r := newSetupRig(t, true)
	if err := setupWith(context.Background(), r.rt, r.steps); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := strings.Join(r.events, ","); got != "prepare,up:neo4j+neo4j-case2+postgres+worker,seed,down:neo4j+neo4j-case2+postgres+worker+core" {
		t.Fatalf("events = %s", got)
	}
	if url := os.Getenv("GHOST_DEMO_FREEZE_WORKER_URL"); !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("the freeze must reach the replay worker on loopback: %q", url)
	}
}

func TestSetupStopsAtTheFirstFailureAndNamesIt(t *testing.T) {
	for _, step := range []string{"prepare", "up", "seed", "down"} {
		r := newSetupRig(t, true)
		r.fail[step] = errors.New(step + " broke")
		err := setupWith(context.Background(), r.rt, r.steps)
		if err == nil || !strings.Contains(err.Error(), step+" broke") {
			t.Errorf("%s: err = %v", step, err)
		}
		if step == "up" && contains(r.events, "seed") {
			t.Errorf("nothing is frozen when the stores did not start: %v", r.events)
		}
	}
}

func TestSetupRefusesWithoutTheExtractionCassettes(t *testing.T) {
	r := newSetupRig(t, false)
	r.rt.Cfg.Process = demorun.Merge(r.rt.Cfg.Process, demorun.Env{"GHOST_DEMO_CASSETTES": filepath.Join(t.TempDir(), "absent")})
	err := setupWith(context.Background(), r.rt, r.steps)
	if err == nil || !strings.Contains(err.Error(), "extraction cassettes were not found") {
		t.Fatalf("err = %v", err)
	}
	if contains(r.events, "seed") {
		t.Fatalf("the freeze cannot reproduce the mined history without them: %v", r.events)
	}
}

func TestCodespaceSetupIsWiredToTheRealSupervisorAndSeeder(t *testing.T) {
	r := newSetupRig(t, true)
	r.rt.Boot.Prepare = func(context.Context) (demorun.Tooling, error) {
		return demorun.Tooling{}, errors.New("stop before any process")
	}
	if err := codespaceSetup(context.Background(), r.rt); err == nil || !strings.Contains(err.Error(), "stop before any process") {
		t.Fatalf("err = %v", err)
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
