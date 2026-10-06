package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
		settle:  func(context.Context) error { r.events = append(r.events, "settle"); return r.fail["settle"] },
		seal:    func(context.Context) error { r.events = append(r.events, "seal"); return r.fail["seal"] },
	}
	return r
}

func TestSetupBuildsStartsTheStoresFreezesBothCasesAndStopsAgain(t *testing.T) {
	r := newSetupRig(t, true)
	if err := setupWith(context.Background(), r.rt, r.steps); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := strings.Join(r.events, ","); got != "prepare,up:neo4j+neo4j-case2+postgres+worker,seed,down:neo4j+neo4j-case2+postgres+worker+core,settle,seal" {
		t.Fatalf("events = %s", got)
	}
	if url := os.Getenv("GHOST_DEMO_FREEZE_WORKER_URL"); !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("the freeze must reach the replay worker on loopback: %q", url)
	}
}

func TestSetupStopsAtTheFirstFailureAndNamesIt(t *testing.T) {
	for _, step := range []string{"prepare", "up", "seed", "down", "settle", "seal"} {
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

// HAR-124: the allowlist counts are read while the worker runs and noted once the baseline is sealed; a failure to read them stops the setup.
func writeAllowlist(t *testing.T, r *setupRig) string {
	t.Helper()
	p := filepath.Join(r.rt.Cfg.Layout.Root, demorun.KnownMissesFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"version":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSetupNotesTheKnownMissCountsAfterTheSeal(t *testing.T) {
	r := newSetupRig(t, true)
	writeAllowlist(t, r)
	var noted [2]int
	r.steps.knownMisses = func(_ context.Context, url string) (int, int, error) {
		r.events = append(r.events, "misses")
		if !strings.HasPrefix(url, "http://127.0.0.1:") {
			t.Errorf("worker url %q", url)
		}
		return 33, 30, nil
	}
	r.steps.noteKnownMisses = func(a, s int, _ string) error { r.events = append(r.events, "note"); noted = [2]int{a, s}; return nil }
	if err := setupWith(context.Background(), r.rt, r.steps); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.events, ","); got != "prepare,up:neo4j+neo4j-case2+postgres+worker,misses,seed,misses,down:neo4j+neo4j-case2+postgres+worker+core,settle,seal,note" {
		t.Fatalf("events = %s", got)
	}
	if noted != [2]int{33, 30} {
		t.Fatalf("noted %v", noted)
	}
}

func TestSetupStopsWhenTheWorkerCountersCannotBeRead(t *testing.T) {
	r := newSetupRig(t, true)
	r.steps.knownMisses = func(context.Context, string) (int, int, error) { return 0, 0, errors.New("no replay stats") }
	if err := setupWith(context.Background(), r.rt, r.steps); err == nil || contains(r.events, "seal") {
		t.Fatalf("err = %v events = %v, want a stop before the seal", err, r.events)
	}
}

func TestReplayKnownMissesReadsTheWorkerCounters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"cassettes":5,"known_miss_hits":2,"known_misses":33}`))
	}))
	defer srv.Close()
	allowed, served, err := replayKnownMisses(context.Background(), srv.URL)
	if err != nil || allowed != 33 || served != 2 {
		t.Fatalf("got %d/%d %v", allowed, served, err)
	}
	stock := httptest.NewServer(http.NotFoundHandler())
	defer stock.Close()
	if _, _, err := replayKnownMisses(context.Background(), stock.URL); err == nil {
		t.Fatal("the stock worker has no counters: not the replay worker")
	}
}

// A replay worker left over from an earlier setup does not enforce the allowlist: the setup refuses before freezing anything.
func TestSetupRefusesAStaleWorkerThatDoesNotEnforceTheAllowlist(t *testing.T) {
	r := newSetupRig(t, true)
	writeAllowlist(t, r)
	r.steps.knownMisses = func(context.Context, string) (int, int, error) { return 0, 0, nil }
	err := setupWith(context.Background(), r.rt, r.steps)
	if err == nil || !strings.Contains(err.Error(), "known-miss allowlist") || contains(r.events, "seed") {
		t.Fatalf("err = %v events = %v, want a refusal before the seed", err, r.events)
	}
}

// A re-check of seeded cases serves nothing: it must not overwrite the freeze's count, unless the allowlist file changed.
func TestKeepServedKeepsTheFreezesCountOnlyForTheSameAllowlist(t *testing.T) {
	prev := &codespace.KnownMissNote{Allowed: 33, Served: 40, ManifestSHA256: "same"}
	for _, tt := range []struct {
		prev   *codespace.KnownMissNote
		sum    string
		served int
		want   int
	}{{prev, "same", 0, 40}, {prev, "other", 0, 0}, {prev, "same", 7, 7}, {nil, "same", 0, 0}} {
		if got := keepServed(tt.prev, tt.sum, tt.served); got != tt.want {
			t.Errorf("keepServed(%+v, %s, %d) = %d, want %d", tt.prev, tt.sum, tt.served, got, tt.want)
		}
	}
}
