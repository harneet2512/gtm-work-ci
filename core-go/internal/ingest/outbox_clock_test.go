package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

func TestOutboxSurvivesABackwardsClockAndNeverMovesDueAtBack(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0.Add(10 * time.Second))
	svc := newService(t, clk, ingest.Options{})

	first := mustIngest(t, svc, inboundEmail(t, "m1", "marco@acme.com", "", t0))
	clk.Set(t0) // the clock steps back before first_enqueued_at (NTP, VM restore, ...)
	second, err := svc.Ingest(context.Background(), inboundEmail(t, "m2", "marco@acme.com", "", t0))
	if err != nil {
		t.Fatalf("a backwards clock must not break ingest (CHECK due_at >= first_enqueued_at): %v", err)
	}

	jobs := loadJobs(t)
	if len(jobs) != 1 || len(jobs[0].activity) != 2 {
		t.Fatalf("want one job with both activities, got %+v", jobs)
	}
	if !jobs[0].due.Equal(*first.RecomputeDueAt) || !second.RecomputeDueAt.Equal(*first.RecomputeDueAt) {
		t.Errorf("due_at moved backwards: first %v, now %v", first.RecomputeDueAt, jobs[0].due)
	}
	if jobs[0].due.Before(jobs[0].first) {
		t.Errorf("due %v before first_enqueued_at %v", jobs[0].due, jobs[0].first)
	}
}
