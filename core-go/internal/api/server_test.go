package api

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNewServerSetsTimeouts(t *testing.T) {
	srv := NewServer("127.0.0.1:0", http.NewServeMux())
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("server lacks timeouts: %+v", srv)
	}
	if srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d", srv.MaxHeaderBytes)
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestServeShutsDownGracefullyOnContextCancel(t *testing.T) {
	ln := listen(t)
	started, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	srv := NewServer(ln.Addr().String(), mux)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, 5*time.Second) }()

	type reply struct {
		code int
		err  error
	}
	got := make(chan reply, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			got <- reply{err: err}
			return
		}
		resp.Body.Close()
		got <- reply{code: resp.StatusCode}
	}()

	<-started
	cancel() // shutdown begins while the request is in flight
	select {
	case err := <-done:
		t.Fatalf("Serve returned before the in-flight request finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)

	if r := <-got; r.err != nil || r.code != http.StatusOK {
		t.Fatalf("in-flight request was cut off: %+v", r)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	if _, err := http.Get("http://" + ln.Addr().String() + "/slow"); err == nil {
		t.Fatal("server still accepting connections after shutdown")
	}
}

func TestServeGivesUpAfterTheShutdownGracePeriod(t *testing.T) {
	ln := listen(t)
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/stuck", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	srv := NewServer(ln.Addr().String(), mux)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, 100*time.Millisecond) }()
	go func() { _, _ = http.Get("http://" + ln.Addr().String() + "/stuck") }()

	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a shutdown timeout error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve hung past the grace period")
	}
}

func TestServeReturnsListenerErrors(t *testing.T) {
	ln := listen(t)
	_ = ln.Close() // serving on a closed listener must fail, not hang
	srv := NewServer("127.0.0.1:0", http.NewServeMux())
	if err := Serve(context.Background(), srv, ln, time.Second); err == nil {
		t.Fatal("expected an error")
	}
}
