package play

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/replaytest"
)

// A Play that finds another one running is refused at once. Waiting on the lock would park each caller on a
// pooled connection (the pool has 10), so enough concurrent Plays would starve everything else.
func TestWaitingPlaysDoNotHoldPoolConnections(t *testing.T) {
	r := newRig(t, replaytest.NewHeldOut(2))
	orig := env.DB.Stats().MaxOpenConnections
	const pool = 4
	env.DB.SetMaxOpenConns(pool)
	t.Cleanup(func() { env.DB.SetMaxOpenConns(orig) })

	holder, err := env.DB.Conn(bg) // the Play that is "running"
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(bg, `SELECT pg_advisory_lock($1)`, lockKey(r.manifest)); err != nil {
		t.Fatal(err)
	}

	const callers = 5 * pool // many more than the pool has connections
	ctx, cancel := context.WithTimeout(bg, 20*time.Second)
	defer cancel()
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.svc.Play(ctx, Request{ManifestID: r.manifest})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, ErrPlayInProgress) {
			t.Errorf("caller %d: %v, want ErrPlayInProgress (and no wait for a connection)", i, err)
		}
	}
	if inUse := env.DB.Stats().InUse; inUse != 1 {
		t.Errorf("%d connections in use after the refused Plays, want only the lock holder's", inUse)
	}
	if r.count("source_events") != "2" {
		t.Error("a refused Play released the event")
	}

	if _, err := holder.ExecContext(bg, `SELECT pg_advisory_unlock($1)`, lockKey(r.manifest)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.play(); err != nil { // the lock is free again
		t.Fatalf("Play after the other one finished: %v", err)
	}
}
