package ingest_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
)

type jobRow struct {
	id       int64
	claimed  bool
	due      time.Time
	first    time.Time
	activity []string
}

func loadJobs(t *testing.T) []jobRow {
	t.Helper()
	rows, err := env.DB.Query(`SELECT id, claimed_at IS NOT NULL, due_at, first_enqueued_at,
		coalesce((SELECT string_agg(x::text, ',' ORDER BY x) FROM unnest(activity_ids) x), '')
		FROM recompute_jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobRow
	for rows.Next() {
		var r jobRow
		var ids string
		if err := rows.Scan(&r.id, &r.claimed, &r.due, &r.first, &ids); err != nil {
			t.Fatal(err)
		}
		if ids != "" {
			r.activity = strings.Split(ids, ",")
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOutboxCoalescesActivitiesIntoOnePendingJob(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0)
	svc := newService(t, clk, ingest.Options{})

	first := mustIngest(t, svc, inboundEmail(t, "m1", "marco@acme.com", "", t0))
	clk.Advance(2 * time.Second)
	second := mustIngest(t, svc, inboundEmail(t, "m2", "marco@acme.com", "", t0))

	jobs := loadJobs(t)
	if len(jobs) != 1 {
		t.Fatalf("want ONE pending job, got %d: %+v", len(jobs), jobs)
	}
	want := []string{first.ActivityID, second.ActivityID}
	sort.Strings(want)
	if fmt.Sprint(jobs[0].activity) != fmt.Sprint(want) {
		t.Errorf("job activity ids = %v, want both %v", jobs[0].activity, want)
	}
	if !jobs[0].first.Equal(t0) {
		t.Errorf("first_enqueued_at = %v, want %v (must not move)", jobs[0].first, t0)
	}
	if wantDue := t0.Add(2*time.Second + testDebounce); !jobs[0].due.Equal(wantDue) {
		t.Errorf("due_at = %v, want debounce extended to %v", jobs[0].due, wantDue)
	}
	if second.RecomputeDueAt == nil || !second.RecomputeDueAt.Equal(jobs[0].due) {
		t.Errorf("result due %v != job due %v", second.RecomputeDueAt, jobs[0].due)
	}
}

func TestOutboxDueAtIsCappedAtFirstPlusMaxWait(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0)
	svc := newService(t, clk, ingest.Options{})

	// A steady trickle every 2s would postpone the recompute forever without the cap.
	cases := []struct {
		offset  time.Duration
		wantDue time.Duration // relative to t0
	}{
		{0, 3 * time.Second},
		{2 * time.Second, 5 * time.Second},
		{8 * time.Second, 11 * time.Second},
		{12 * time.Second, 15 * time.Second}, // 12+3 == cap
		{13 * time.Second, 15 * time.Second}, // 13+3 > cap -> capped
		{14 * time.Second, 15 * time.Second},
	}
	for i, tc := range cases {
		clk.Set(t0.Add(tc.offset))
		res := mustIngest(t, svc, inboundEmail(t, fmt.Sprintf("m%d", i), "marco@acme.com", "", t0))
		if res.RecomputeDueAt == nil || !res.RecomputeDueAt.Equal(t0.Add(tc.wantDue)) {
			t.Fatalf("event %d at +%v: due = %v, want t0+%v", i, tc.offset, res.RecomputeDueAt, tc.wantDue)
		}
	}
	jobs := loadJobs(t)
	if len(jobs) != 1 || len(jobs[0].activity) != len(cases) {
		t.Fatalf("want one job with %d ids, got %+v", len(cases), jobs)
	}
	if !jobs[0].due.Equal(t0.Add(testMaxWait)) || !jobs[0].first.Equal(t0) {
		t.Errorf("due=%v first=%v, want due=first+maxWait", jobs[0].due, jobs[0].first)
	}
}

func TestOutboxKeepsSeparateJobsPerAccount(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	seedAccount(t, "Beta", "beta.io")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	mustIngest(t, svc, inboundEmail(t, "m1", "marco@acme.com", "", t0))
	mustIngest(t, svc, inboundEmail(t, "m2", "bob@beta.io", "", t0))
	if jobs := loadJobs(t); len(jobs) != 2 {
		t.Fatalf("want a job per account, got %d", len(jobs))
	}
}

func TestOutboxActivityDuringRecomputeLandsInNewPendingJob(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	clk := clock.NewFixed(t0)
	svc := newService(t, clk, ingest.Options{})
	first := mustIngest(t, svc, inboundEmail(t, "m1", "marco@acme.com", "", t0))

	// A worker claims the pending job (recompute in flight).
	if _, err := env.DB.Exec(`UPDATE recompute_jobs SET claimed_at = $1::timestamptz, claimed_by = 'worker-1', lease_expires_at = $1::timestamptz + interval '1 minute'`, t0.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	clk.Set(t0.Add(5 * time.Second))
	late := mustIngest(t, svc, inboundEmail(t, "m2", "marco@acme.com", "", t0))

	jobs := loadJobs(t)
	if len(jobs) != 2 {
		t.Fatalf("want in-flight + new pending job, got %+v", jobs)
	}
	inflight, pending := jobs[0], jobs[1]
	if !inflight.claimed || fmt.Sprint(inflight.activity) != fmt.Sprint([]string{first.ActivityID}) {
		t.Errorf("in-flight job modified: %+v", inflight)
	}
	if pending.claimed || fmt.Sprint(pending.activity) != fmt.Sprint([]string{late.ActivityID}) {
		t.Errorf("late activity not in a fresh pending job: %+v", pending)
	}
	if !pending.first.Equal(t0.Add(5*time.Second)) || !pending.due.Equal(t0.Add(5*time.Second+testDebounce)) {
		t.Errorf("pending job timing wrong: %+v", pending)
	}

	// A further activity coalesces into that new pending job, not the in-flight one.
	clk.Advance(time.Second)
	mustIngest(t, svc, inboundEmail(t, "m3", "marco@acme.com", "", t0))
	jobs = loadJobs(t)
	if len(jobs) != 2 || len(jobs[0].activity) != 1 || len(jobs[1].activity) != 2 {
		t.Fatalf("coalescing after in-flight wrong: %+v", jobs)
	}
}

func TestConcurrentDeliveriesOfOneEventCreateOneActivity(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})
	ev := inboundEmail(t, "m1", "marco@acme.com", "", t0)

	const n = 12
	results := make([]ingest.Result, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = svc.Ingest(context.Background(), ev)
		}()
	}
	wg.Wait()

	fresh := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("delivery %d: %v", i, errs[i])
		}
		if !results[i].Duplicate {
			fresh++
		}
		if results[i].ActivityID != results[0].ActivityID {
			t.Fatalf("deliveries disagree on the activity id")
		}
	}
	if fresh != 1 {
		t.Errorf("%d deliveries reported as new, want exactly 1", fresh)
	}
	if got := queryString(t, `SELECT delivery_count::text FROM source_events`); got != fmt.Sprint(n) {
		t.Errorf("delivery_count = %s, want %d", got, n)
	}
	if c := tableCounts(t); c["source_events"] != 1 || c["activities"] != 1 || c["recompute_jobs"] != 1 {
		t.Errorf("counts after concurrent replay: %v", c)
	}
}

func TestConcurrentDistinctEventsForOneAccountShareOneJob(t *testing.T) {
	resetDB(t)
	seedAccount(t, "Acme", "acme.com")
	svc := newService(t, clock.NewFixed(t0), ingest.Options{})

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.Ingest(context.Background(), inboundEmail(t, fmt.Sprintf("m%02d", i), "marco@acme.com", "", t0))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	jobs := loadJobs(t)
	if len(jobs) != 1 || len(jobs[0].activity) != n {
		t.Fatalf("want one job holding %d ids, got %d jobs: %+v", n, len(jobs), jobs)
	}
	if got := count(t, "activities"); got != n {
		t.Fatalf("activities = %d", got)
	}
	if strings.Count(fmt.Sprint(jobs[0].activity), "-") == 0 {
		t.Fatal("job holds no uuids")
	}
}
