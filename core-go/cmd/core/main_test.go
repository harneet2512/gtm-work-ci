package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	code := storetest.Main(m, func(e *storetest.Env) { env = e })
	neo4jtest.Stop()
	os.Exit(code)
}

const apiToken = "core-test-token-0123456789-0123456789"

func testConfig(addr string) config.Config {
	return config.Config{
		DatabaseURL:      env.URL,
		CoreAddr:         addr,
		RunMode:          "dry_run",
		CoalesceDebounce: 50 * time.Millisecond,
		CoalesceMaxWait:  time.Second,
		APIToken:         apiToken,
	}
}

func quietLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// startCore runs the service in the background and returns its base URL and a stop function
// that cancels it and returns run's error.
func startCore(t *testing.T, cfg config.Config) (baseURL string, stop func() error) {
	t.Helper()
	return startCoreWith(t, cfg, deps{})
}

// startCoreWith is startCore with collaborators replaced (the scripted worker, a breaker the test controls).
func startCoreWith(t *testing.T, cfg config.Config, d deps) (baseURL string, stop func() error) {
	t.Helper()
	logger, _ := quietLogger()
	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- runWith(ctx, cfg, logger, func(a net.Addr) { addrCh <- a }, d) }()

	select {
	case a := <-addrCh:
		baseURL = "http://" + a.String()
	case err := <-done:
		cancel()
		t.Fatalf("run exited before listening: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("service did not start listening")
	}
	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-done:
			case <-time.After(20 * time.Second):
				stopErr = errors.New("service did not stop after cancel")
			}
		})
		return stopErr
	}
	// A test that fails before it stops the service must not leave it running against the shared database.
	t.Cleanup(func() { _ = stop() })
	return baseURL, stop
}

func request(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const unresolvedEmail = `{"source_system":"email","source_object_id":"core-main-1","source_event_key":"received","payload":{
 "kind":"email","message_id":"core-main-1","thread_id":"t","direction":"inbound",
 "from":{"email":"sam@nowhere-test.example"},"to":[{"email":"dana@ghostvendor.com"}],
 "date":"2026-10-03T10:00:00Z","subject":"hi","body_text":"hello"}}`

func TestRunServesHealthzAndIngestThenShutsDownCleanly(t *testing.T) {
	base, stop := startCore(t, testConfig("127.0.0.1:0"))

	if code, body := request(t, http.MethodGet, base+"/healthz", "", ""); code != 200 || !strings.Contains(body, `"ok"`) {
		t.Fatalf("healthz = %d %s", code, body)
	}
	if code, _ := request(t, http.MethodPost, base+"/ingest", "", unresolvedEmail); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ingest = %d, want 401", code)
	}
	if code, _ := request(t, http.MethodPost, base+"/ingest", "wrong-token", unresolvedEmail); code != http.StatusUnauthorized {
		t.Fatalf("wrong-token ingest = %d, want 401", code)
	}
	code, body := request(t, http.MethodPost, base+"/ingest", apiToken, unresolvedEmail)
	if code != http.StatusCreated || !strings.Contains(body, `"duplicate":false`) {
		t.Fatalf("first ingest = %d %s", code, body)
	}
	code, body = request(t, http.MethodPost, base+"/ingest", apiToken, unresolvedEmail)
	if code != http.StatusOK || !strings.Contains(body, `"duplicate":true`) {
		t.Fatalf("replayed ingest = %d %s", code, body)
	}
	if code, body := request(t, http.MethodPost, base+"/ingest", apiToken, `{"source_system":"fax","source_object_id":"1","source_event_key":"k","payload":{}}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid event = %d %s", code, body)
	}

	if err := stop(); err != nil {
		t.Fatalf("run returned %v after cancel, want nil", err)
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Fatal("still serving after shutdown")
	}
}

func TestRunAppliesMigrationsOnStart(t *testing.T) {
	// The test database is already migrated; run must be idempotent about it, and a database
	// at version 0 must come up migrated. Reset to 0 and let run do the work.
	if err := env.Migrator.DownTo(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	base, stop := startCore(t, testConfig("127.0.0.1:0"))
	defer func() { _ = stop() }()

	if code, body := request(t, http.MethodPost, base+"/ingest", apiToken, unresolvedEmail); code != http.StatusCreated {
		t.Fatalf("ingest after auto-migration = %d %s", code, body)
	}
}

func TestRunRefusesToStartWithoutAPIToken(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.APIToken = ""
	logger, _ := quietLogger()
	if err := run(context.Background(), cfg, logger, nil); err == nil || !strings.Contains(err.Error(), "GHOST_API_TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFailsFastOnUnreachableDatabase(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.DatabaseURL = "postgres://ghost:ghost@127.0.0.1:1/ghost_test?sslmode=disable&connect_timeout=2"
	logger, _ := quietLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := run(ctx, cfg, logger, nil)
	if err == nil {
		t.Fatal("unreachable database accepted")
	}
	if strings.Contains(err.Error(), "ghost:ghost") {
		t.Errorf("error leaks credentials: %v", err)
	}
}

func TestRunFailsWhenAddressIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	logger, _ := quietLogger()
	if err := run(context.Background(), testConfig(taken.Addr().String()), logger, nil); err == nil {
		t.Fatal("bound an address that is already in use")
	}
}

func TestExecuteReadsTheEnvironmentAndStopsWhenCancelled(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", env.URL)
	t.Setenv("CORE_ADDR", "127.0.0.1:0")
	t.Setenv("GHOST_API_TOKEN", apiToken)
	logger, logs := quietLogger()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel as soon as the listener is up: serve, then shut down immediately.
	if err := execute(ctx, logger, func(net.Addr) { cancel() }); err != nil {
		t.Fatalf("execute = %v", err)
	}
	if !strings.Contains(logs.String(), "core listening") {
		t.Errorf("service never reported listening: %s", logs.String())
	}
	if strings.Contains(logs.String(), apiToken) || strings.Contains(logs.String(), "ghost:ghost") {
		t.Errorf("logs leak secrets: %s", logs.String())
	}

	t.Setenv("GHOST_API_TOKEN", "")
	if err := execute(context.Background(), logger, nil); err == nil {
		t.Fatal("execute started without a token")
	}
}

func TestRunRefusesAWorkerTimeoutThatDoesNotOutlastTheWorkerDeadline(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = "http://127.0.0.1:1"
	cfg.WorkerExtractDeadline = 180 * time.Second
	cfg.WorkerTimeout = 120 * time.Second
	logger, _ := quietLogger()
	err := run(context.Background(), cfg, logger, nil)
	if err == nil || !strings.Contains(err.Error(), "WORKER_TIMEOUT_MS") {
		t.Fatalf("run must refuse the budget, got %v", err)
	}
}

func TestRunRefusesALeaseShorterThanOneWorkerCall(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = "http://127.0.0.1:1"
	cfg.WorkerExtractDeadline = 180 * time.Second
	cfg.WorkerTimeout = 210 * time.Second
	cfg.CoalesceLease = time.Minute
	logger, _ := quietLogger()
	err := run(context.Background(), cfg, logger, nil)
	if err == nil || !strings.Contains(err.Error(), "COALESCE_LEASE_MS") {
		t.Fatalf("run must refuse the lease, got %v", err)
	}
}

func TestEffectiveLeaseFallsBackToTheCoalesceDefault(t *testing.T) {
	if got := effectiveLease(config.Config{}); got != 5*time.Minute {
		t.Fatalf("default lease %v", got)
	}
	if got := effectiveLease(config.Config{CoalesceLease: time.Hour}); got != time.Hour {
		t.Fatalf("configured lease %v", got)
	}
}

func TestWorkerTimeoutOptionsAlwaysApplyABudget(t *testing.T) {
	if got := workerTimeoutOptions(config.Config{}); len(got) != 1 {
		t.Fatalf("an unset timeout must still get the default budget, got %d options", len(got))
	}
	d, to := config.Config{}.EffectiveWorkerBudget()
	if d != 180*time.Second || to != 210*time.Second {
		t.Fatalf("default budget %v %v", d, to)
	}
}

func TestLoadServeConfigRequiresToken(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_API_TOKEN", "")
	if _, err := loadServeConfig(); err == nil {
		t.Fatal("serve config without token accepted")
	}
	t.Setenv("GHOST_API_TOKEN", "tok-0123456789-0123456789-0123456789")
	cfg, err := loadServeConfig()
	if err != nil || cfg.APIToken != "tok-0123456789-0123456789-0123456789" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}
