package coalesce_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunWarnsPeriodicallyWhileJobsAreParkedOrActivitiesQuarantined(t *testing.T) {
	w := seedBasic(t)
	act := newActivity(t, w.account, 1, t0)
	if _, err := env.DB.Exec(`INSERT INTO quarantined_activities (activity_id, account_id, reason, permanent, attempts) VALUES ($1::uuid, $2::uuid, 'worker 422', true, 1)`, act, w.account); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids, claimed_at, claimed_by, lease_expires_at, attempts, parked_at, last_error)
		VALUES ($1::uuid, $2, $2, ARRAY[$3::uuid], $2, 'w', 'infinity', 5, $2, 'diff writer down')`, w.account, t0, act); err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	s := newService(t, clock.NewFixed(t0.Add(time.Hour)), coalesce.Options{Logger: logger, WarnEvery: 20 * time.Millisecond, ParkRetry: 1000 * time.Hour})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx, 10*time.Millisecond)
	out := logs.String()
	if n := strings.Count(out, "needs an operator"); n < 2 {
		t.Fatalf("expected a repeating warning, saw %d:\n%s", n, out)
	}
	for _, want := range []string{"parked_jobs=1", "quarantined_activities=1", w.account, "diff writer down", act} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q:\n%s", want, out)
		}
	}

	resetAll(t)
	quiet := &syncBuffer{}
	s = newService(t, clock.NewFixed(t0), coalesce.Options{Logger: slog.New(slog.NewTextHandler(quiet, nil)), WarnEvery: 10 * time.Millisecond})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()
	_ = s.Run(ctx2, 10*time.Millisecond)
	if strings.Contains(quiet.String(), "needs an operator") {
		t.Fatalf("a healthy queue must not warn:\n%s", quiet.String())
	}
}
