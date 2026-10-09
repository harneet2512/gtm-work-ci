package demorun

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// timeoutCore answers 504 "timeout" `fails` times, then the outcome (or err when set).
type timeoutCore struct {
	fails, calls int
	err          error
}

func (c *timeoutCore) Play(context.Context, string) (PlayOutcome, error) {
	c.calls++
	if c.calls <= c.fails {
		return PlayOutcome{}, &APIError{Status: http.StatusGatewayTimeout, Code: "timeout", Message: "call Play again to resume"}
	}
	return PlayOutcome{AccountChangeID: "chg-1"}, c.err
}

func resumeOptions() (PlayOptions, *fakeClock) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	return PlayOptions{Timeout: time.Minute, Poll: 3 * time.Second, Now: clk.Now, Sleep: clk.Sleep}.withDefaults(), clk
}

// core's contract for a Play that outruns its 55 s deadline is "call Play again to resume": the first real recording hit it.
func TestPlayResumingCallsPlayAgainOnA504UntilItFinishes(t *testing.T) {
	core := &timeoutCore{fails: 2}
	o, _ := resumeOptions()
	out, err := playResuming(context.Background(), core, "m-1", o, func(string, ...any) {})
	if err != nil || out.AccountChangeID != "chg-1" || core.calls != 3 {
		t.Fatalf("out %+v err %v calls %d, want the third call to finish", out, err, core.calls)
	}
}

func TestPlayResumingGivesUpAtTheOverallTimeoutAndNeverRetriesOtherErrors(t *testing.T) {
	core := &timeoutCore{fails: 1000}
	o, _ := resumeOptions()
	if _, err := playResuming(context.Background(), core, "m-1", o, func(string, ...any) {}); err == nil {
		t.Fatal("a Play that never finishes must fail at the overall timeout")
	}
	other := &timeoutCore{err: errors.New("boom")}
	o2, _ := resumeOptions()
	if _, err := playResuming(context.Background(), other, "m-1", o2, func(string, ...any) {}); err == nil || other.calls != 1 {
		t.Fatalf("err %v calls %d: only a 504 is resumed", err, other.calls)
	}
}
