package providerbreaker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
)

var t0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func newBreaker(t *testing.T, threshold int, logs *bytes.Buffer) (*Breaker, *clock.Fixed) {
	t.Helper()
	clk := clock.NewFixed(t0)
	var log *slog.Logger
	if logs != nil {
		log = slog.New(slog.NewTextHandler(logs, nil))
	}
	b, err := New(threshold, time.Minute, clk, log)
	if err != nil {
		t.Fatal(err)
	}
	return b, clk
}

func TestTripsAtTheThresholdAndLogsOneClearLine(t *testing.T) {
	var logs bytes.Buffer
	b, _ := newBreaker(t, 3, &logs)
	b.Failure("402")
	b.Failure("402")
	if b.State() != Closed || !b.Allow() {
		t.Fatal("below the threshold the breaker stays closed")
	}
	b.Failure("402")
	b.Failure("402") // already open: no second trip, no second line
	if b.State() != Open || b.Allow() {
		t.Fatalf("state = %s", b.State())
	}
	if n := strings.Count(logs.String(), "circuit breaker OPEN"); n != 1 || !strings.Contains(logs.String(), "ghostctl breaker reset") {
		t.Fatalf("want one clear OPEN line, got %d:\n%s", n, logs.String())
	}
	st := b.Status()
	if st.TripsTotal != 1 || st.RejectedTotal != 1 || st.ConsecutiveFailures != 4 || st.OpenedAt == nil || st.LastReason != "402" {
		t.Fatalf("status = %+v", st)
	}
}

func TestSuccessEndsTheStreak(t *testing.T) {
	b, _ := newBreaker(t, 2, nil)
	b.Failure("x")
	b.Success()
	b.Failure("x")
	if b.State() != Closed {
		t.Fatal("failures must be consecutive")
	}
}

func TestCooldownGivesHalfOpenAndOneFailureReopensOneSuccessCloses(t *testing.T) {
	b, clk := newBreaker(t, 1, nil)
	b.Failure("x")
	clk.Advance(59 * time.Second)
	if b.State() != Open {
		t.Fatal("still cooling down")
	}
	clk.Advance(2 * time.Second)
	if b.State() != HalfOpen || !b.Allow() {
		t.Fatalf("state = %s", b.State())
	}
	b.Failure("x")
	if b.State() != Open || b.Status().TripsTotal != 2 {
		t.Fatalf("a failed probe reopens: %+v", b.Status())
	}
	clk.Advance(2 * time.Minute)
	b.Success()
	if b.State() != Closed {
		t.Fatal("a good probe closes")
	}
}

func TestOperatorResetClosesImmediately(t *testing.T) {
	b, _ := newBreaker(t, 1, nil)
	b.Failure("x")
	b.Reset()
	if b.State() != Closed || !b.Allow() || b.Status().ConsecutiveFailures != 0 {
		t.Fatalf("status = %+v", b.Status())
	}
}

func TestNewRejectsNegativeAndAppliesDefaults(t *testing.T) {
	if _, err := New(-1, 0, nil, nil); err == nil {
		t.Fatal("negative threshold must fail")
	}
	b, err := New(0, 0, nil, nil)
	if err != nil || b.Status().Threshold != DefaultThreshold || b.Status().CooldownSeconds != DefaultCooldown.Seconds() {
		t.Fatalf("%+v %v", b, err)
	}
}

type fault struct{ unavailable, counts bool }

func (f fault) Error() string             { return "fault" }
func (f fault) ProviderUnavailable() bool { return f.unavailable }
func (f fault) ProviderFault() bool       { return f.counts }

func TestGuardCountsOnlyProviderFaultsAndRefusesWhileOpen(t *testing.T) {
	b, _ := newBreaker(t, 2, nil)
	var failWith error
	ex := &claimstest.FakeExtractor{Err: func(claims.ExtractRequest) error { return failWith }}
	g := Guard{Inner: ex, Breaker: b}
	call := func() error { _, err := g.Extract(context.Background(), claims.ExtractRequest{}); return err }

	failWith = errors.New("422 invalid request") // the request's fault, not the provider's
	for i := 0; i < 5; i++ {
		_ = call()
	}
	failWith = context.Canceled
	_ = call()
	if b.State() != Closed {
		t.Fatal("request errors and cancellations must not trip the breaker")
	}
	failWith = fault{counts: true} // a recoverable 5xx counts, it is just retried
	_ = call()
	_ = call()
	if b.State() != Open {
		t.Fatal("two provider faults trip it")
	}
	before := len(ex.Calls())
	err := call()
	if !errors.Is(err, ErrOpen) || !claims.IsProviderUnavailable(err) || len(ex.Calls()) != before {
		t.Fatalf("an open breaker must refuse locally without calling the worker: %v", err)
	}
}
