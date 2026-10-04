package providerbreaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
)

func TestHalfOpenAllowsExactlyOneConcurrentProbe(t *testing.T) {
	b, clk := newBreaker(t, 1, nil)
	b.Failure("x")
	clk.Advance(2 * time.Minute)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Allow() {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed.Load() != 1 {
		t.Fatalf("probes allowed = %d, want exactly 1", allowed.Load())
	}
	if b.Allow() {
		t.Fatal("a second probe must wait for the first to report")
	}
	b.Success()
	if !b.Allow() || b.State() != Closed {
		t.Fatal("a successful probe closes the breaker")
	}
}

func TestProbeIsClearedByFailureAndByNoVerdict(t *testing.T) {
	b, clk := newBreaker(t, 1, nil)
	b.Failure("x")
	clk.Advance(2 * time.Minute)
	if !b.Allow() {
		t.Fatal("first probe")
	}
	b.ReleaseProbe() // a request error: no verdict
	if !b.Allow() {
		t.Fatal("after a no-verdict probe the next caller probes")
	}
	b.Failure("x") // verdict: provider still down, breaker reopens and the probe slot is free again
	clk.Advance(2 * time.Minute)
	if !b.Allow() {
		t.Fatal("after the cool-down a new probe is allowed")
	}
}

func TestGuardReleasesTheProbeOnARequestError(t *testing.T) {
	b, clk := newBreaker(t, 1, nil)
	b.Failure("x")
	clk.Advance(2 * time.Minute)
	ex := &claimstest.FakeExtractor{Err: func(claims.ExtractRequest) error { return errors.New("422 invalid request") }}
	g := Guard{Inner: ex, Breaker: b}
	for i := 0; i < 3; i++ {
		if _, err := g.Extract(context.Background(), claims.ExtractRequest{}); err == nil || errors.Is(err, ErrOpen) {
			t.Fatalf("call %d must reach the worker and fail on its own: %v", i, err)
		}
	}
	if len(ex.Calls()) != 3 {
		t.Fatalf("worker calls = %d", len(ex.Calls()))
	}
}
