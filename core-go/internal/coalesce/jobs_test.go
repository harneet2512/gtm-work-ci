package coalesce_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func resetAll(t *testing.T) {
	t.Helper()
	err := storetest.Purge(context.Background(), env.DB, `TRUNCATE state_diffs, state_history, account_state, recompute_jobs, claims, extraction_cache, unresolved_activities,
		activity_participants, activities, source_events, entity_source_mappings, relationships, opportunities, people, accounts RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

func newAccount(t *testing.T, name string) string {
	t.Helper()
	return scalar(t, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, name)
}

func newActivity(t *testing.T, account string, n int, at time.Time) string {
	t.Helper()
	obj := fmt.Sprintf("obj-%d-%s", n, account[:8])
	ev := scalar(t, `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', $1::text, 'received', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id::text`, obj)
	return scalar(t, `INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', $2, $3, $4::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb) RETURNING id::text`, ev, obj, at, account)
}

// insertJob creates a pending job; claimedBy != "" makes it in flight with the given lease expiry.
func insertJob(t *testing.T, account string, due time.Time, acts []string, claimedBy string, lease time.Time) int64 {
	t.Helper()
	if claimedBy == "" {
		return mustInt(t, `INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids) VALUES ($1::uuid, $2, $3, $4::uuid[]) RETURNING id::text`,
			account, due, due.Add(-time.Second), uuidArray(acts))
	}
	return mustInt(t, `INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids, claimed_at, claimed_by, lease_expires_at, attempts)
		VALUES ($1::uuid, $2, $3, $4::uuid[], $5, $6, $7, 1) RETURNING id::text`, account, due, due.Add(-time.Second), uuidArray(acts), t0, claimedBy, lease)
}

func mustInt(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if _, err := fmt.Sscan(scalar(t, q, args...), &n); err != nil {
		t.Fatal(err)
	}
	return n
}

func uuidArray(ids []string) string { return "{" + strings.Join(ids, ",") + "}" }

func newService(t *testing.T, clk clock.Clock, opts coalesce.Options) *coalesce.Service {
	t.Helper()
	opts.Clock = clk
	s, err := coalesce.New(env.DB, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaimTakesADueJobAndLeasesIt(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1, a2 := newActivity(t, acct, 1, t0), newActivity(t, acct, 2, t0)
	id := insertJob(t, acct, t0, []string{a1, a2}, "", time.Time{})
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{WorkerID: "w1", Lease: 5 * time.Minute})

	job, err := s.Claim(context.Background())
	if err != nil || job == nil {
		t.Fatalf("job=%v err=%v", job, err)
	}
	got := append([]string(nil), job.ActivityIDs...)
	sort.Strings(got)
	want := []string{a1, a2}
	sort.Strings(want)
	if job.ID != id || job.AccountID != acct || fmt.Sprint(got) != fmt.Sprint(want) || job.Attempts != 1 {
		t.Fatalf("job = %+v", job)
	}
	row := scalar(t, `SELECT claimed_by || '|' || (claimed_at = $1)::text || '|' || (lease_expires_at = $2)::text || '|' || attempts::text FROM recompute_jobs WHERE id = $3`,
		clk.Now(), clk.Now().Add(5*time.Minute), id)
	if row != "w1|true|true|1" {
		t.Fatalf("row = %s", row)
	}
	if again, err := s.Claim(context.Background()); err != nil || again != nil {
		t.Fatalf("a claimed job must not be claimed twice: %v %v", again, err)
	}
}

func TestClaimIgnoresJobsThatAreNotDue(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	insertJob(t, acct, t0.Add(time.Hour), []string{newActivity(t, acct, 1, t0)}, "", time.Time{})
	s := newService(t, clock.NewFixed(t0), coalesce.Options{})
	if job, err := s.Claim(context.Background()); err != nil || job != nil {
		t.Fatalf("job=%v err=%v", job, err)
	}
}

func TestClaimWaitsForTheInFlightJobOfTheSameAccount(t *testing.T) {
	resetAll(t)
	acct, other := newAccount(t, "Acme"), newAccount(t, "Beta")
	insertJob(t, acct, t0, []string{newActivity(t, acct, 1, t0)}, "someone-else", t0.Add(time.Hour))
	pending := insertJob(t, acct, t0, []string{newActivity(t, acct, 2, t0)}, "", time.Time{})
	otherJob := insertJob(t, other, t0.Add(time.Second), []string{newActivity(t, other, 3, t0)}, "", time.Time{})
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{})

	job, err := s.Claim(context.Background())
	if err != nil || job == nil || job.ID != otherJob {
		t.Fatalf("the account with a live in-flight job must be skipped, got %+v %v", job, err)
	}
	if again, _ := s.Claim(context.Background()); again != nil {
		t.Fatalf("pending job %d claimed while its account is in flight: %+v", pending, again)
	}
	if _, err := env.DB.Exec(`DELETE FROM recompute_jobs WHERE claimed_by = 'someone-else'`); err != nil {
		t.Fatal(err)
	}
	if job, _ := s.Claim(context.Background()); job == nil || job.ID != pending {
		t.Fatalf("after the in-flight job finished the pending one is due: %+v", job)
	}
}

func TestClaimOrdersByDueTime(t *testing.T) {
	resetAll(t)
	a, b := newAccount(t, "A"), newAccount(t, "B")
	late := insertJob(t, a, t0.Add(10*time.Second), []string{newActivity(t, a, 1, t0)}, "", time.Time{})
	early := insertJob(t, b, t0.Add(2*time.Second), []string{newActivity(t, b, 2, t0)}, "", time.Time{})
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{})
	first, _ := s.Claim(context.Background())
	second, _ := s.Claim(context.Background())
	if first == nil || second == nil || first.ID != early || second.ID != late {
		t.Fatalf("%+v %+v", first, second)
	}
}

func TestConcurrentClaimersNeverShareAJob(t *testing.T) {
	resetAll(t)
	const accounts = 8
	for i := 0; i < accounts; i++ {
		a := newAccount(t, fmt.Sprintf("Acct %d", i))
		insertJob(t, a, t0, []string{newActivity(t, a, i, t0)}, "", time.Time{})
	}
	clk := clock.NewFixed(t0.Add(time.Minute))
	var mu sync.Mutex
	claimed := map[int64]int{}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s, err := coalesce.New(env.DB, coalesce.Options{Clock: clk, WorkerID: fmt.Sprintf("w%d", w)})
			if err != nil {
				t.Error(err)
				return
			}
			for {
				job, err := s.Claim(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				claimed[job.ID]++
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(claimed) != accounts {
		t.Fatalf("claimed %d distinct jobs, want %d", len(claimed), accounts)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Errorf("job %d claimed %d times", id, n)
		}
	}
}

func TestExpiredLeaseWithoutPendingJobIsReclaimed(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1 := newActivity(t, acct, 1, t0)
	id := insertJob(t, acct, t0, []string{a1}, "crashed-worker", t0.Add(time.Minute))
	s := newService(t, clock.NewFixed(t0.Add(2*time.Minute)), coalesce.Options{WorkerID: "w2"})

	job, err := s.Claim(context.Background())
	if err != nil || job == nil || job.ID != id || job.Attempts != 2 || len(job.ActivityIDs) != 1 || job.ActivityIDs[0] != a1 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if got := scalar(t, `SELECT claimed_by FROM recompute_jobs WHERE id = $1`, id); got != "w2" {
		t.Fatalf("claimed_by = %s", got)
	}
}

func TestExpiredLeaseMergesIntoThePendingJobOfTheAccount(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1, a2, a3 := newActivity(t, acct, 1, t0), newActivity(t, acct, 2, t0), newActivity(t, acct, 3, t0)
	insertJob(t, acct, t0, []string{a1, a2}, "crashed-worker", t0.Add(time.Minute))
	pending := insertJob(t, acct, t0, []string{a2, a3}, "", time.Time{})
	s := newService(t, clock.NewFixed(t0.Add(2*time.Minute)), coalesce.Options{WorkerID: "w2"})

	job, err := s.Claim(context.Background())
	if err != nil || job == nil || job.ID != pending {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	got := append([]string(nil), job.ActivityIDs...)
	sort.Strings(got)
	want := []string{a1, a2, a3}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("merged activity ids = %v, want the distinct union %v", got, want)
	}
	if n := scalar(t, `SELECT count(*)::text FROM recompute_jobs`); n != "1" {
		t.Fatalf("jobs = %s, want the expired row merged away", n)
	}
}

func TestJobsThatExhaustedTheirAttemptsAreParkedUntilNewActivityArrives(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1, a2 := newActivity(t, acct, 1, t0), newActivity(t, acct, 2, t0)
	insertJob(t, acct, t0, []string{a1}, "", time.Time{})
	clk := clock.NewFixed(t0.Add(time.Hour))
	s := newService(t, clk, coalesce.Options{MaxAttempts: 2, WorkerID: "w1", RetryDelay: time.Second})

	for attempt := 1; attempt <= 2; attempt++ {
		job, err := s.Claim(context.Background())
		if err != nil || job == nil || job.Attempts != attempt {
			t.Fatalf("attempt %d: %+v %v", attempt, job, err)
		}
		if err := s.Release(context.Background(), *job, fmt.Errorf("worker 502")); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Minute)
	}
	if job, err := s.Claim(context.Background()); err != nil || job != nil {
		t.Fatalf("a parked job was claimed: %+v %v", job, err)
	}
	parked, err := s.Parked(context.Background())
	if err != nil || len(parked) != 1 || parked[0].AccountID != acct || parked[0].AccountName != "Acme" || parked[0].Attempts != 2 ||
		parked[0].ActivityCount != 1 || parked[0].LastError != "worker 502" || parked[0].Age <= 0 || parked[0].ParkedAt.IsZero() {
		t.Fatalf("parked = %+v %v: observability must name the account, the cause and the age", parked, err)
	}
	if got := scalar(t, `SELECT (parked_at IS NOT NULL)::text || '|' || claimed_by FROM recompute_jobs`); got != "true|w1" {
		t.Fatalf("parking is an explicit column, claimed_by keeps the worker: %s", got)
	}
	if got := scalar(t, `SELECT last_error FROM recompute_jobs`); got != "worker 502" {
		t.Fatalf("the failure must stay visible: %s", got)
	}

	insertJob(t, acct, clk.Now(), []string{a2}, "", time.Time{}) // new activity: a fresh pending job
	job, err := s.Claim(context.Background())
	if err != nil || job == nil || job.Attempts != 1 || len(job.ActivityIDs) != 2 {
		t.Fatalf("new activity must un-park the account with a fresh attempt budget and the old activities: %+v %v", job, err)
	}
	if parked, _ := s.Parked(context.Background()); len(parked) != 0 {
		t.Fatalf("parked = %+v after un-parking", parked)
	}
}

func TestReleaseMakesAFailedJobPendingAgainWithBackoff(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1 := newActivity(t, acct, 1, t0)
	id := insertJob(t, acct, t0, []string{a1}, "", time.Time{})
	clk := clock.NewFixed(t0.Add(time.Minute))
	s := newService(t, clk, coalesce.Options{WorkerID: "w1", RetryDelay: 10 * time.Second})
	job, _ := s.Claim(context.Background())

	if err := s.Release(context.Background(), *job, fmt.Errorf("worker 502")); err != nil {
		t.Fatal(err)
	}
	row := scalar(t, `SELECT (claimed_at IS NULL)::text || '|' || last_error || '|' || (due_at = $1)::text || '|' || attempts::text FROM recompute_jobs WHERE id = $2`,
		clk.Now().Add(10*time.Second), id)
	if row != "true|worker 502|true|1" {
		t.Fatalf("row = %s", row)
	}
	if again, _ := s.Claim(context.Background()); again != nil {
		t.Fatal("a released job must wait out its backoff")
	}
	clk.Advance(11 * time.Second)
	if again, _ := s.Claim(context.Background()); again == nil || again.Attempts != 2 {
		t.Fatalf("after the backoff the job is retried: %+v", again)
	}
}

func TestReleaseMergesIntoAPendingJobThatArrivedMeanwhile(t *testing.T) {
	resetAll(t)
	acct := newAccount(t, "Acme")
	a1, a2 := newActivity(t, acct, 1, t0), newActivity(t, acct, 2, t0)
	insertJob(t, acct, t0, []string{a1}, "", time.Time{})
	s := newService(t, clock.NewFixed(t0.Add(time.Minute)), coalesce.Options{WorkerID: "w1"})
	job, _ := s.Claim(context.Background())
	pending := insertJob(t, acct, t0.Add(time.Hour), []string{a2}, "", time.Time{}) // ingest during the recompute

	if err := s.Release(context.Background(), *job, fmt.Errorf("boom")); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM recompute_jobs`); n != "1" {
		t.Fatalf("jobs = %s", n)
	}
	got := scalar(t, `SELECT array_length(activity_ids, 1)::text || '|' || attempts::text FROM recompute_jobs WHERE id = $1`, pending)
	if got != "2|0" {
		t.Fatalf("pending job = %s, want both activities and a fresh attempt count (new activity un-parks the account)", got)
	}
}
