package demorun

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testTooling() Tooling {
	return Tooling{Host: "/bin/ghostctl-host", Core: "/bin/core", Slackbot: "/bin/slackbot", Python: "/venv/python", Npm: "/usr/bin/npm"}
}

func specNames(specs []Spec) string {
	var n []string
	for _, s := range specs {
		n = append(n, s.Name)
	}
	return strings.Join(n, ",")
}

func TestPlanStartsStoresBeforeWorkerBeforeCoreBeforeSurfaces(t *testing.T) {
	specs := testConfig().Specs(testTooling())
	if got := specNames(specs); got != "neo4j,postgres,worker,core,slackbot,web" {
		t.Fatalf("order = %s", got)
	}
}

func TestPlanOmitsDisabledServices(t *testing.T) {
	c := testConfig()
	c.NoSlack, c.NoWeb = true, true
	if got := specNames(c.Specs(testTooling())); got != "neo4j,postgres,worker,core" {
		t.Fatalf("order = %s", got)
	}
}

func TestPlanCommandsAndWorkingDirectories(t *testing.T) {
	c := testConfig()
	by := map[string]Spec{}
	for _, s := range c.Specs(testTooling()) {
		by[s.Name] = s
	}
	if w := by["worker"]; w.Path != "/venv/python" || !contains(w.Args, "ghost_worker.app:create_app") || !contains(w.Args, "--factory") ||
		!contains(w.Args, "8090") || filepathSlash(w.Dir) != "/repo/worker-py" {
		t.Fatalf("worker spec wrong: %+v", w)
	}
	if co := by["core"]; co.Path != "/bin/core" || filepathSlash(co.Dir) != "/repo" {
		t.Fatalf("core must run from the repository root (relative rule paths): %+v", co)
	}
	if web := by["web"]; web.Path != "/usr/bin/npm" || !contains(web.Args, "dev") || !contains(web.Args, "3000") || filepathSlash(web.Dir) != "/repo/web" {
		t.Fatalf("web spec wrong: %+v", web)
	}
	for _, name := range []string{"neo4j", "postgres"} {
		s := by[name]
		if s.Path != "/bin/ghostctl-host" || !contains(s.Args, "demo-host") || !contains(s.Args, name) || s.Graceful <= 0 {
			t.Fatalf("%s must be a gracefully stopped ghostctl host: %+v", name, s)
		}
	}
	if by["postgres"].Cleanup == nil {
		t.Fatal("postgres needs a cleanup that stops a leftover server after a hard kill")
	}
}

func TestPlanReplayWithCassettesUsesTheExtractionReplayWorker(t *testing.T) {
	c := testConfig()
	c.LLMMode, c.ReplayCassettes = "replay", "/data/cassettes"
	var w Spec
	for _, s := range c.Specs(testTooling()) {
		if s.Name == "worker" {
			w = s
		}
	}
	joined := strings.Join(w.Args, " ")
	if w.Path != "/venv/python" || !strings.Contains(filepathSlash(joined), "bench/data/crmarena_replay_worker.py") ||
		!contains(w.Args, "--cassettes") || !contains(w.Args, "/data/cassettes") || !contains(w.Args, "8090") {
		t.Fatalf("replay with cassettes must run the masked replay worker: %+v", w)
	}
	if !strings.Contains(w.URL, "replay") {
		t.Fatalf("status should say the worker is replaying: %q", w.URL)
	}
	c.LLMMode = "live"
	for _, s := range c.Specs(testTooling()) {
		if s.Name == "worker" && !contains(s.Args, "ghost_worker.app:create_app") {
			t.Fatalf("live mode must run the real worker even when cassettes are configured: %+v", s)
		}
	}
}

func TestPlanEveryServiceHasAHealthCheckAndAGenerousButBoundedTimeout(t *testing.T) {
	for _, s := range testConfig().Specs(testTooling()) {
		if s.Health == nil {
			t.Errorf("%s has no health check", s.Name)
		}
		if s.Wait.Timeout < 30*time.Second || s.Wait.Timeout > 10*time.Minute {
			t.Errorf("%s health timeout %s is out of range", s.Name, s.Wait.Timeout)
		}
		if s.Port == 0 && s.Name != "slackbot" {
			t.Errorf("%s has no port for the conflict check", s.Name)
		}
	}
}

func TestPlanNeverPutsSecretsOnTheCommandLine(t *testing.T) {
	c := testConfig()
	for _, s := range c.Specs(testTooling()) {
		joined := strings.Join(s.Args, " ")
		for _, secret := range []string{orKey, "neo-pass-1234", "xoxb-bot", "xapp-app", "api-token-0123"} {
			if strings.Contains(joined, secret) {
				t.Errorf("%s leaks a secret on its command line (visible in the process list)", s.Name)
			}
		}
	}
}

func TestHostEnvCarriesDirectoryPortAndPasswordNotOnTheCommandLine(t *testing.T) {
	c := testConfig()
	for _, s := range c.Specs(testTooling()) {
		if s.Name != "neo4j" {
			continue
		}
		if s.Env["NEO4J_PASSWORD"] != "neo-pass-1234" || s.Env["GHOST_DEMO_HOST_PORT"] != "17687" || s.Env["GHOST_DEMO_HOST_STOPFILE"] == "" || s.Env["GHOST_DEMO_HOST_DIR"] == "" {
			t.Fatalf("neo4j host env incomplete: %v", s.Env.Names())
		}
	}
}

func TestPortConflictIsReportedAgainstTheService(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	err = CheckPortFree(Spec{Name: "core", Port: port})
	var se *ServiceError
	if !errors.As(err, &se) || se.Service != "core" || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("want a ServiceError for core naming the busy port, got %v", err)
	}
	_ = l.Close()
	if err := CheckPortFree(Spec{Name: "core", Port: port}); err != nil {
		t.Fatalf("a free port must pass: %v", err)
	}
	if err := CheckPortFree(Spec{Name: "slackbot"}); err != nil {
		t.Fatalf("a spec with no port is never a conflict: %v", err)
	}
}

func TestLogContainsSinceTheLastStartIgnoresAnOlderRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slackbot.log")
	body := "--- demo start 2026-10-04T10:00:00Z ---\nslack socket connected\n--- demo start 2026-10-04T11:00:00Z ---\nstarting\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	check := LogContainsSinceStart(path, "slack socket connected")
	if err := check(context.Background()); err == nil {
		t.Fatal("a marker from the previous run must not count")
	}
	if err := os.WriteFile(path, []byte(body+"slack socket connected\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(context.Background()); err != nil {
		t.Fatalf("marker after the last start must pass: %v", err)
	}
	if err := LogContainsSinceStart(filepath.Join(t.TempDir(), "none.log"), "x")(context.Background()); err == nil {
		t.Fatal("a missing log is not healthy")
	}
}

func TestWaitForStopReturnsWhenTheFileAppearsOrContextEnds(t *testing.T) {
	stop := filepath.Join(t.TempDir(), "x.stop")
	done := make(chan error, 1)
	go func() { done <- WaitForStop(context.Background(), stop, 10*time.Millisecond) }()
	time.Sleep(40 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("returned before the stop file existed")
	default:
	}
	if err := os.WriteFile(stop, []byte("stop"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not notice the stop file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForStop(ctx, filepath.Join(t.TempDir(), "never"), 10*time.Millisecond); err != nil {
		t.Fatalf("a cancelled context is a clean stop: %v", err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// HAR-124: the replay worker is told which misses are allowed (and none when no allowlist exists).
func TestPlanReplayWorkerGetsTheKnownMissAllowlistOnlyWhenOneIsConfigured(t *testing.T) {
	workerArgs := func(c Config) []string {
		for _, s := range c.Specs(testTooling()) {
			if s.Name == "worker" {
				return s.Args
			}
		}
		return nil
	}
	c := testConfig()
	c.LLMMode, c.ReplayCassettes = "replay", "/data/cassettes"
	if contains(workerArgs(c), "--known-misses") {
		t.Fatal("no allowlist configured: every miss must stay an error")
	}
	c.KnownMisses = "/data/misses"
	if args := workerArgs(c); !contains(args, "--known-misses") || !contains(args, "/data/misses") {
		t.Fatalf("args = %v, want --known-misses /data/misses", args)
	}
}

// HAR-124 review: only the checked-in manifest is an allowlist. A miss dump (--dump-misses) beside the cassettes, or an environment
// variable, must not widen it.
func TestFindKnownMissesReadsOnlyTheCheckedInManifest(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	cfg.Layout.Root = root
	data := filepath.Join(root, "data", "crmarena_extraction")
	for _, d := range []string{filepath.Join(data, "cassettes"), filepath.Join(data, "misses")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(data, "misses", "ab"+".txt"), []byte("a brand new miss"), 0o644); err != nil {
		t.Fatal(err)
	}
	override := t.TempDir()
	cfg.Process = Env{"GHOST_DEMO_KNOWN_MISSES": override}
	if got := FindKnownMisses(cfg); got == filepath.Join(data, "misses") || got == override || got != "" && !strings.HasSuffix(filepath.ToSlash(got), "bench/data/known_extraction_misses.json") {
		t.Fatalf("a dump folder or an env var must not be an allowlist: got %q", got)
	}
	shipped := filepath.Join(root, KnownMissesFile)
	if err := os.MkdirAll(filepath.Dir(shipped), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shipped, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindKnownMisses(cfg); got != shipped {
		t.Fatalf("got %q, want the checked-in manifest %q", got, shipped)
	}
}
