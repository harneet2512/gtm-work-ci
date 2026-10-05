package providerbreaker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeWaker is a deterministic clock and sleeper for the limiter: sleeping advances the clock instead of
// blocking, and records every wait.
type fakeWaker struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (f *fakeWaker) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeWaker) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	f.waits = append(f.waits, d)
	return nil
}

func (f *fakeWaker) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fakeWaker) waitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waits)
}

func (f *fakeWaker) total() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total time.Duration
	for _, w := range f.waits {
		total += w
	}
	return total
}

func TestTheTokenBucketAllowsABurstThenPacesToTheCeiling(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	l := newLimiter(5.0/60, 5, w.clock, w.sleep)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if w.waitCount() != 0 {
		t.Fatalf("the burst of 5 waited %d times", w.waitCount())
	}
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if w.waitCount() != 1 || w.total() != 12*time.Second {
		t.Fatalf("the 6th call waited %d times for %s, want once for 12s (one token at 5/min)", w.waitCount(), w.total())
	}
}

func TestTheBucketRefillsUpToItsBurstAndNoFurther(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	l := newLimiter(5.0/60, 5, w.clock, w.sleep)
	w.advance(time.Hour) // a long quiet period must not bank more than the burst
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if w.waitCount() != 0 {
		t.Fatalf("a full bucket waited %d times", w.waitCount())
	}
	if err := l.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if w.waitCount() != 1 {
		t.Fatalf("the 6th call waited %d times, want once", w.waitCount())
	}
}

func TestANilOrZeroRPMLimiterNeverBlocks(t *testing.T) {
	var nilLimiter *Limiter
	if err := nilLimiter.Wait(context.Background()); err != nil {
		t.Fatalf("a nil limiter waited: %v", err)
	}
	if NewLimiter(0) != nil || NewLimiter(-3) != nil {
		t.Fatal("a non-positive rpm must return no limiter")
	}
	if NewLimiter(5) == nil {
		t.Fatal("a positive rpm must return a limiter")
	}
}

func TestWaitEndsWhenTheContextEnds(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	l := newLimiter(1, 0, w.clock, w.sleep) // an empty bucket: the first call must wait
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait returned %v, want context.Canceled", err)
	}
}

func TestTheLimiterIsSafeUnderConcurrency(t *testing.T) {
	w := &fakeWaker{now: time.Unix(1_700_000_000, 0)}
	l := newLimiter(5.0/60, 5, w.clock, w.sleep)
	const calls = 20
	var wg sync.WaitGroup
	errs := make([]error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = l.Wait(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if w.waitCount() < calls-5 {
		t.Fatalf("waits = %d, want at least %d: only 5 tokens were in the bucket", w.waitCount(), calls-5)
	}
}
