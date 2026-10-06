package demorun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a test: the supervisor tests re-run this test binary as a fake service. Modes:
// serve (HTTP /healthz on a port), die (exit at once), hang (alive, never listens), graceful (serve until the
// stop file appears, then write a marker and exit 0).
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("GHOST_DEMO_HELPER")
	if mode == "" {
		return
	}
	port, _ := strconv.Atoi(os.Getenv("GHOST_DEMO_HELPER_PORT"))
	switch mode {
	case "die":
		fmt.Println("helper dying")
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	case "serve", "graceful":
		fmt.Println("helper up; secret present:", os.Getenv("GHOST_DEMO_HELPER_SECRET") != "")
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		go func() { _ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux) }()
		stop := os.Getenv("GHOST_DEMO_HELPER_STOPFILE")
		for {
			if mode == "graceful" && stop != "" {
				if _, err := os.Stat(stop); err == nil {
					_ = os.WriteFile(os.Getenv("GHOST_DEMO_HELPER_MARKER"), []byte("graceful"), 0o600)
					os.Exit(0)
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	os.Exit(0)
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func helperSpec(t *testing.T, name, mode string, extra Env) (Spec, int) {
	t.Helper()
	port := freeTestPort(t)
	env := Env{"GHOST_DEMO_HELPER": mode, "GHOST_DEMO_HELPER_PORT": strconv.Itoa(port)}
	for k, v := range extra {
		env[k] = v
	}
	return Spec{
		Name: name, Path: os.Args[0], Args: []string{"-test.run=^TestHelperProcess$"},
		Env:    env,
		Health: HTTPCheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil),
		Wait:   WaitOptions{Timeout: 10 * time.Second, Interval: 50 * time.Millisecond, CheckTimeout: time.Second},
	}, port
}

func newSupervisor(t *testing.T) (Supervisor, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	l := NewLayout(t.TempDir())
	return Supervisor{Layout: l, PIDs: l.PIDs(), Out: &out}, &out
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if !ProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pid %d is still alive", pid)
}

func TestSupervisorStartsHealthChecksAndStops(t *testing.T) {
	sup, out := newSupervisor(t)
	spec, port := helperSpec(t, "core", "serve", Env{"GHOST_DEMO_HELPER_SECRET": "super-secret-value"})
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	st, rec := sup.PIDs.State("core")
	if st != StateRunning || rec.PID <= 0 {
		t.Fatalf("state after Start = %v %+v", st, rec)
	}
	logBytes, err := os.ReadFile(sup.Layout.LogFile("core"))
	if err != nil || !strings.Contains(string(logBytes), "helper up; secret present: true") {
		t.Fatalf("log not captured or env not passed: %v %q", err, logBytes)
	}
	if strings.Contains(out.String(), "super-secret-value") || strings.Contains(string(logBytes), "super-secret-value") {
		t.Fatal("the supervisor leaked an environment value")
	}
	if err := sup.Stop(context.Background(), spec); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitDead(t, rec.PID)
	if st, _ := sup.PIDs.State("core"); st != StateStopped {
		t.Fatalf("pid file must be gone after Stop, state = %v", st)
	}
	if err := TCPCheck(fmt.Sprintf("127.0.0.1:%d", port))(context.Background()); err == nil {
		t.Fatal("the port must be closed after Stop")
	}
}

func TestSupervisorReportsAServiceThatDiesAtStartup(t *testing.T) {
	sup, _ := newSupervisor(t)
	spec, _ := helperSpec(t, "worker", "die", nil)
	err := sup.Start(context.Background(), spec)
	var se *ServiceError
	if !errors.As(err, &se) || se.Service != "worker" {
		t.Fatalf("want a ServiceError naming worker, got %v", err)
	}
	if !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "worker.log") {
		t.Fatalf("error should say it exited and point at the log: %v", err)
	}
	if st, _ := sup.PIDs.State("worker"); st != StateStopped {
		t.Fatalf("a failed start must not leave a pid file, state = %v", st)
	}
}

func TestSupervisorKillsAServiceThatNeverBecomesHealthy(t *testing.T) {
	sup, _ := newSupervisor(t)
	spec, _ := helperSpec(t, "web", "hang", nil)
	spec.Wait = WaitOptions{Timeout: 400 * time.Millisecond, Interval: 50 * time.Millisecond, CheckTimeout: 100 * time.Millisecond}
	start := time.Now()
	err := sup.Start(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "web") || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("want a health timeout naming web, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("did not honour the health timeout")
	}
	if st, _ := sup.PIDs.State("web"); st != StateStopped {
		t.Fatalf("the unhealthy process must be killed and forgotten, state = %v", st)
	}
}

func TestSupervisorStartIsIdempotentForARunningService(t *testing.T) {
	sup, out := newSupervisor(t)
	spec, _ := helperSpec(t, "core", "serve", nil)
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	defer sup.Stop(context.Background(), spec)
	_, first := sup.PIDs.State("core")
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	_, second := sup.PIDs.State("core")
	if first.PID != second.PID {
		t.Fatal("a second Start must not spawn a second process")
	}
	if !strings.Contains(out.String(), "already running") {
		t.Fatalf("should say so: %q", out.String())
	}
}

func TestSupervisorStopOfAStoppedServiceIsFine(t *testing.T) {
	sup, _ := newSupervisor(t)
	if err := sup.Stop(context.Background(), Spec{Name: "slackbot"}); err != nil {
		t.Fatalf("Stop of a service that never ran: %v", err)
	}
	if err := sup.PIDs.Write(Record{Service: "slackbot", PID: 2147483000}); err != nil {
		t.Fatal(err)
	}
	if err := sup.Stop(context.Background(), Spec{Name: "slackbot"}); err != nil {
		t.Fatalf("Stop of a stale record: %v", err)
	}
	if st, _ := sup.PIDs.State("slackbot"); st != StateStopped {
		t.Fatalf("stale record must be cleaned, state = %v", st)
	}
}

func TestSupervisorStopsGracefullyThroughTheStopFileWhenAsked(t *testing.T) {
	sup, _ := newSupervisor(t)
	marker := filepath.Join(t.TempDir(), "graceful.marker")
	spec, _ := helperSpec(t, "postgres", "graceful", Env{
		"GHOST_DEMO_HELPER_STOPFILE": sup.StopFile("postgres"), "GHOST_DEMO_HELPER_MARKER": marker})
	spec.Graceful = 10 * time.Second
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := sup.Stop(context.Background(), spec); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "graceful" {
		t.Fatalf("the service was killed instead of asked to stop: %v %q", err, b)
	}
	if _, err := os.Stat(sup.StopFile("postgres")); !os.IsNotExist(err) {
		t.Fatal("the stop file must be removed afterwards so the next start does not exit at once")
	}
}

func TestSupervisorRunsCleanupAfterStop(t *testing.T) {
	sup, _ := newSupervisor(t)
	called := 0
	spec := Spec{Name: "postgres", Cleanup: func() error { called++; return nil }}
	if err := sup.Stop(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("Cleanup ran %d times, want 1 (even when nothing was running)", called)
	}
}

func TestStatusReportsEveryServiceWithoutValues(t *testing.T) {
	sup, _ := newSupervisor(t)
	spec, _ := helperSpec(t, "core", "serve", Env{"GHOST_DEMO_HELPER_SECRET": "xyz"})
	if err := sup.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	defer sup.Stop(context.Background(), spec)
	rows := sup.Status(context.Background(), []Spec{spec, {Name: "web"}})
	if len(rows) != 2 || rows[0].State != StateRunning || !rows[0].Healthy || rows[1].State != StateStopped {
		t.Fatalf("status rows wrong: %+v", rows)
	}
	var buf bytes.Buffer
	WriteStatus(&buf, rows)
	if !strings.Contains(buf.String(), "core") || !strings.Contains(buf.String(), "running") || strings.Contains(buf.String(), "xyz") {
		t.Fatalf("status table wrong: %q", buf.String())
	}
}

func TestTailLogsReturnsTheLastLines(t *testing.T) {
	sup, _ := newSupervisor(t)
	if err := os.MkdirAll(sup.Layout.LogDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	if err := os.WriteFile(sup.Layout.LogFile("core"), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := sup.Logs("core", 3, &out); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "line 98\nline 99\nline 100" {
		t.Fatalf("tail = %q", got)
	}
	if err := sup.Logs("nosuch", 3, &out); err == nil {
		t.Fatal("an unknown log must be an error naming the service")
	}
	if err := sup.Logs("../x", 3, &out); err == nil {
		t.Fatal("path traversal must be refused")
	}
}
