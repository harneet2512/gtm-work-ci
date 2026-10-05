package providerbreaker

import (
	"context"
	"sync"
	"time"
)

// Limiter is a token bucket that paces model calls to a provider's requests-per-minute ceiling. One token
// is spent per call; tokens refill continuously at rpm/60 per second up to a burst of rpm, so a quiet period
// buys a short burst and a busy one is spread out. A nil Limiter (rpm <= 0) paces nothing.
//
// It is safe for concurrent use: callers that compute the same wait race for the token and the loser waits
// again, which keeps the average rate at or below the ceiling.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64 // capacity
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// NewLimiter returns a limiter for rpm requests per minute, or nil when rpm <= 0 (unlimited).
func NewLimiter(rpm int) *Limiter {
	if rpm <= 0 {
		return nil
	}
	return newLimiter(float64(rpm)/60, float64(rpm), time.Now, sleepContext)
}

// newLimiter builds a limiter with an injectable clock and sleeper, so tests stay deterministic.
func newLimiter(rate, burst float64, now func() time.Time, sleep func(context.Context, time.Duration) error) *Limiter {
	return &Limiter{rate: rate, burst: burst, now: now, sleep: sleep, tokens: burst, last: now()}
}

// Wait blocks until a token is available or ctx ends. A nil limiter never blocks.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.rate <= 0 {
		return ctx.Err()
	}
	for {
		if wait := l.take(); wait <= 0 {
			return ctx.Err()
		} else if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// take refills the bucket, spends one token if it can, and otherwise reports how long to wait for one.
func (l *Limiter) take() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if elapsed := now.Sub(l.last); elapsed > 0 {
		l.tokens = min(l.burst, l.tokens+elapsed.Seconds()*l.rate)
		l.last = now
	}
	if l.tokens >= 1 {
		l.tokens--
		return 0
	}
	return time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
}

// sleepContext waits d, or returns early when ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
