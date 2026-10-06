package demorun

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastOpts(timeout time.Duration) WaitOptions {
	return WaitOptions{Timeout: timeout, Interval: 5 * time.Millisecond}
}

func TestWaitHealthyReturnsOnceTheCheckPasses(t *testing.T) {
	var calls atomic.Int32
	check := func(context.Context) error {
		if calls.Add(1) < 4 {
			return errors.New("not yet")
		}
		return nil
	}
	if err := WaitHealthy(context.Background(), "core", check, fastOpts(2*time.Second)); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("expected 4 attempts, got %d", calls.Load())
	}
}

func TestWaitHealthyTimeoutNamesTheServiceAndTheLastError(t *testing.T) {
	check := func(context.Context) error { return errors.New("connection refused") }
	start := time.Now()
	err := WaitHealthy(context.Background(), "neo4j", check, fastOpts(80*time.Millisecond))
	if err == nil {
		t.Fatal("must time out")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("did not honour the timeout: %v", time.Since(start))
	}
	var se *ServiceError
	if !errors.As(err, &se) || se.Service != "neo4j" {
		t.Fatalf("error must be a *ServiceError naming neo4j, got %T %v", err, err)
	}
	for _, want := range []string{"neo4j", "not healthy", "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}
}

func TestWaitHealthyFailsFastWhenTheProcessDied(t *testing.T) {
	check := func(context.Context) error { return errors.New("refused") }
	opts := fastOpts(30 * time.Second)
	opts.Alive = func() bool { return false }
	start := time.Now()
	err := WaitHealthy(context.Background(), "worker", check, opts)
	if err == nil || !strings.Contains(err.Error(), "exited") || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("want an 'exited' error naming worker, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("a dead process must not wait out the timeout")
	}
}

func TestWaitHealthyHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	check := func(context.Context) error { return errors.New("no") }
	err := WaitHealthy(ctx, "web", check, fastOpts(30*time.Second))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWaitHealthyBoundsASingleHangingCheck(t *testing.T) {
	check := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	opts := fastOpts(150 * time.Millisecond)
	opts.CheckTimeout = 20 * time.Millisecond
	start := time.Now()
	if err := WaitHealthy(context.Background(), "core", check, opts); err == nil {
		t.Fatal("must fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("a hanging check must not hang the wait")
	}
}

func TestHTTPCheck(t *testing.T) {
	var status atomic.Int32
	status.Store(503)
	var sawAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	check := HTTPCheck(srv.URL, map[string]string{"Authorization": "Bearer t"})
	if err := check(context.Background()); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("503 must fail and say so, got %v", err)
	}
	status.Store(200)
	if err := check(context.Background()); err != nil {
		t.Fatalf("200 must pass: %v", err)
	}
	if sawAuth.Load() != "Bearer t" {
		t.Fatal("headers were not sent")
	}
	if err := HTTPCheck("http://127.0.0.1:1/", nil)(context.Background()); err == nil {
		t.Fatal("a closed port must fail")
	}
}

func TestTCPCheck(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := TCPCheck(addr)(context.Background()); err != nil {
		t.Fatalf("listening port must pass: %v", err)
	}
	_ = l.Close()
	if err := TCPCheck(addr)(context.Background()); err == nil {
		t.Fatal("closed port must fail")
	}
}

func TestHTTPAliveCheckPassesOnAnyAnswerAndFailsOnNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	if err := HTTPAliveCheck(srv.URL)(context.Background()); err != nil {
		t.Fatalf("a 500 from a live server means the server is up: %v", err)
	}
	if err := HTTPAliveCheck("http://127.0.0.1:1/")(context.Background()); err == nil {
		t.Fatal("a closed port is not alive")
	}
	if err := HTTPAliveCheck("://bad")(context.Background()); err == nil {
		t.Fatal("a malformed URL is an error")
	}
}
