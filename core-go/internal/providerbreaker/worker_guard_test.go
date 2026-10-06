package providerbreaker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// countingWorker records how many model calls reached the worker and fails them with err.
type countingWorker struct {
	calls int
	err   error
}

func (w *countingWorker) Strategies(context.Context, workerclient.StrategiesRequest) (workerclient.StrategiesResponse, error) {
	w.calls++
	return workerclient.StrategiesResponse{}, w.err
}

func (w *countingWorker) Judge(context.Context, workerclient.JudgeRequest) (workerclient.JudgeResponse, error) {
	w.calls++
	return workerclient.JudgeResponse{}, w.err
}

func (w *countingWorker) Revise(context.Context, workerclient.ReviseRequest) (workerclient.ReviseResponse, error) {
	w.calls++
	return workerclient.ReviseResponse{}, w.err
}

func callAll(g WorkerGuard) []error {
	ctx := context.Background()
	_, e1 := g.Strategies(ctx, workerclient.StrategiesRequest{})
	_, e2 := g.Judge(ctx, workerclient.JudgeRequest{})
	_, e3 := g.Revise(ctx, workerclient.ReviseRequest{})
	return []error{e1, e2, e3}
}

func TestAnOpenBreakerRefusesEveryWorkerCallWithoutCallingTheModel(t *testing.T) {
	b, _ := newBreaker(t, 2, nil)
	inner := &countingWorker{err: &workerclient.Error{Status: 424, Code: workerclient.CodeProviderUnavailable}}
	g := WorkerGuard{Inner: inner, Breaker: b}
	callAll(g) // three provider refusals trip a threshold of two
	if b.State() != Open || inner.calls != 2 {
		t.Fatalf("state %s after %d calls; the third call must already have been refused", b.State(), inner.calls)
	}
	before := inner.calls
	for i, err := range callAll(g) {
		var open OpenError
		if !errors.As(err, &open) || !errors.Is(err, ErrOpen) || !claims.IsProviderUnavailable(err) {
			t.Fatalf("call %d: %v is not a provider-unavailable OpenError", i, err)
		}
	}
	if inner.calls != before {
		t.Fatalf("an open breaker let %d paid calls through", inner.calls-before)
	}
}

func TestARequestErrorDoesNotCountAgainstTheProvider(t *testing.T) {
	b, _ := newBreaker(t, 2, nil)
	inner := &countingWorker{err: &workerclient.Error{Status: 422, Code: "invalid_request"}}
	g := WorkerGuard{Inner: inner, Breaker: b}
	for range 3 {
		callAll(g)
	}
	if b.State() != Closed || inner.calls != 9 {
		t.Fatalf("state %s, %d calls: a 422 is the request's fault, not the provider's", b.State(), inner.calls)
	}
}

func TestSuccessKeepsTheBreakerClosed(t *testing.T) {
	b, _ := newBreaker(t, 2, nil)
	g := WorkerGuard{Inner: &countingWorker{}, Breaker: b}
	for _, err := range callAll(g) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if b.State() != Closed {
		t.Fatal("successful calls must leave the breaker closed")
	}
}

func TestTheWorkerGuardPacesCallsThroughTheLimiter(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	b, _ := newBreaker(t, 5, nil)
	inner := &countingWorker{}
	g := WorkerGuard{Inner: inner, Breaker: b, Limiter: newLimiter(5.0/60, 5, w.clock, w.sleep)}
	for i := 0; i < 6; i++ {
		if _, err := g.Strategies(context.Background(), workerclient.StrategiesRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 6 {
		t.Fatalf("worker calls = %d, want 6", inner.calls)
	}
	if w.waitCount() != 1 || w.total() != 12*time.Second {
		t.Fatalf("the 6th call waited %d times for %s, want once for 12s", w.waitCount(), w.total())
	}
}

func TestTheWorkerGuardStopsWhenTheLimiterWaitEnds(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	b, _ := newBreaker(t, 5, nil)
	inner := &countingWorker{}
	g := WorkerGuard{Inner: inner, Breaker: b, Limiter: newLimiter(1, 0, w.clock, w.sleep)} // empty bucket
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Judge(ctx, workerclient.JudgeRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Judge returned %v, want context.Canceled", err)
	}
	if inner.calls != 0 {
		t.Fatalf("a paced-out call reached the model %d times", inner.calls)
	}
	if b.State() != Closed {
		t.Fatalf("breaker state %s: no call means no verdict on the provider", b.State())
	}
}
