package coalesce_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstest"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// workerStub is a fake model worker (no live LLM). mode "refuse" answers the worker's non-retryable
// 424 provider_unavailable_nonretryable (what a 402 from the provider becomes), "flaky" a recoverable
// 503 provider_error, "ok" an empty but valid extraction.
type workerStub struct {
	mu   sync.Mutex
	mode string
	hits int
	ok   map[string]int // activity id -> successful (billed) extractions
}

func newWorkerStub(t *testing.T, mode string) (*workerStub, string) {
	t.Helper()
	w := &workerStub{mode: mode, ok: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct {
			Activity struct{ ID string } `json:"activity"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.hits++
		switch w.mode {
		case "refuse":
			rw.WriteHeader(424)
			_, _ = rw.Write([]byte(`{"error":{"code":"provider_unavailable_nonretryable","message":"model provider is unavailable"}}`))
		case "flaky":
			rw.WriteHeader(502)
			_, _ = rw.Write([]byte(`{"error":{"code":"provider_error","message":"model provider failed"}}`))
		default:
			w.ok[body.Activity.ID]++
			_, _ = rw.Write([]byte(`{"claims":[],"model":"stub-model","extractor_version":"extract-v1","dropped":0}`))
		}
	}))
	t.Cleanup(srv.Close)
	return w, srv.URL
}

func (w *workerStub) setMode(m string) { w.mu.Lock(); w.mode = m; w.mu.Unlock() }
func (w *workerStub) calls() int       { w.mu.Lock(); defer w.mu.Unlock(); return w.hits }

// inboundFrom is inbound() for a contact at another customer domain.
func inboundFrom(t *testing.T, domain string, n int, at time.Time, body string) normalize.SourceEvent {
	t.Helper()
	ev := inbound(t, n, at, body)
	raw := strings.ReplaceAll(string(ev.Payload), "acme.com", domain)
	ev.Payload = json.RawMessage(raw)
	ev.SourceObjectID = strings.ReplaceAll(ev.SourceObjectID, "acme.com", domain)
	return ev
}

func seedAccounts(t *testing.T, count int) {
	t.Helper()
	seedBasic(t)
	svc := ingestService(t, clock.NewFixed(t0))
	ingestAll(t, svc, inbound(t, 1, t0.Add(-2*time.Hour), "First point. More"), inbound(t, 2, t0.Add(-time.Hour), "Second point. More"))
	for i := 2; i <= count; i++ {
		domain := fmt.Sprintf("acme%d.com", i)
		acct := scalar(t, `INSERT INTO accounts (name, domain) VALUES ($1, $2) RETURNING id::text`, fmt.Sprintf("Acme %d", i), domain)
		priya := scalar(t, `INSERT INTO people (kind, display_name, primary_email, account_id) VALUES ('contact', 'Priya', $1, $2::uuid) RETURNING id::text`, "priya.shah@"+domain, acct)
		if _, err := env.DB.Exec(`INSERT INTO entity_source_mappings (entity_type, entity_id, source_system, source_key, confidence, method)
			VALUES ('person', $1::uuid, 'email', $2, 1, 'seed')`, priya, "priya.shah@"+domain); err != nil {
			t.Fatal(err)
		}
		ingestAll(t, svc, inboundFrom(t, domain, 10*i+1, t0.Add(-2*time.Hour), "First point. More"), inboundFrom(t, domain, 10*i+2, t0.Add(-time.Hour), "Second point. More"))
	}
}

func breakerService(t *testing.T, workerURL string, threshold int, clk clock.Clock, opts coalesce.Options) (*coalesce.Service, *providerbreaker.Breaker) {
	t.Helper()
	client, err := workerclient.New(workerURL)
	if err != nil {
		t.Fatal(err)
	}
	br, err := providerbreaker.New(threshold, 10*time.Minute, clk, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.Extractor = providerbreaker.Guard{Inner: client, Breaker: br}
	opts.Breaker = br
	return newService(t, clk, opts), br
}

func TestProviderRefusalStormIsOneCallPerJobTripsTheBreakerAndNeverRetries(t *testing.T) {
	seedAccounts(t, 6) // six accounts, two activities each: six jobs
	stub, url := newWorkerStub(t, "refuse")
	clk := clock.NewFixed(t0.Add(time.Minute))
	s, br := breakerService(t, url, 3, clk, coalesce.Options{MaxAttempts: 5, RetryDelay: time.Second, ParkRetry: 30 * time.Minute})

	res, err := s.Drain(context.Background())
	if err == nil || res.Failed != 3 {
		t.Fatalf("three jobs fail before the breaker opens: %+v %v", res, err)
	}
	if got := stub.calls(); got != 3 {
		t.Fatalf("worker calls = %d, want exactly one per failed job (3), none for the jobs the open breaker held back", got)
	}
	if br.State() != providerbreaker.Open {
		t.Fatalf("breaker state = %s", br.State())
	}
	parked, _ := s.Parked(context.Background())
	if len(parked) != 3 || !strings.Contains(parked[0].LastError, "provider unavailable (non-retryable)") || !strings.Contains(parked[0].LastError, "provider_unavailable_nonretryable") {
		t.Fatalf("each refused job must park at once with a clear reason: %+v", parked)
	}
	if q, _ := s.Quarantined(context.Background()); len(q) != 0 {
		t.Fatalf("a provider outage must never quarantine an activity: %+v", q)
	}

	// The storm does not continue: draining again, even after the retry delay, makes no call.
	clk.Advance(5 * time.Minute)
	if _, err := s.Drain(context.Background()); err != nil || stub.calls() != 3 {
		t.Fatalf("an open breaker must pause all model calls: calls=%d err=%v", stub.calls(), err)
	}
}

func TestBreakerResetsByCooldownOrOperatorAndEveryActivityIsBilledOnce(t *testing.T) {
	seedAccounts(t, 4)
	stub, url := newWorkerStub(t, "refuse")
	clk := clock.NewFixed(t0.Add(time.Minute))
	s, br := breakerService(t, url, 2, clk, coalesce.Options{ParkRetry: 30 * time.Minute})
	_, _ = s.Drain(context.Background())
	if br.State() != providerbreaker.Open || stub.calls() != 2 {
		t.Fatalf("state=%s calls=%d", br.State(), stub.calls())
	}

	// Operator action: the provider is funded again; reset resumes the two jobs the breaker held back.
	stub.setMode("ok")
	br.Reset()
	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 2 {
		t.Fatalf("after reset: %+v %v", res, err)
	}
	// The two parked jobs come back after the park back-off, and the breaker stayed closed.
	clk.Advance(31 * time.Minute)
	res, err = s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 2 || br.State() != providerbreaker.Closed {
		t.Fatalf("parked jobs must recover: %+v %v state=%s", res, err, br.State())
	}
	for id, n := range stub.ok {
		if n != 1 {
			t.Fatalf("activity %s was extracted (billed) %d times, want exactly once", id, n)
		}
	}
	if len(stub.ok) != 8 {
		t.Fatalf("extracted activities = %d, want all 8", len(stub.ok))
	}
}

func TestBreakerCooldownLetsOneProbeThroughAndAFailedProbeReopensIt(t *testing.T) {
	seedAccounts(t, 3)
	stub, url := newWorkerStub(t, "refuse")
	clk := clock.NewFixed(t0.Add(time.Minute))
	s, br := breakerService(t, url, 1, clk, coalesce.Options{ParkRetry: time.Hour})
	_, _ = s.Drain(context.Background())
	if stub.calls() != 1 || br.State() != providerbreaker.Open {
		t.Fatalf("calls=%d state=%s", stub.calls(), br.State())
	}
	clk.Advance(11 * time.Minute) // past the cool-down: half-open, one probe
	_, _ = s.Drain(context.Background())
	if stub.calls() != 2 || br.State() != providerbreaker.Open || br.Status().TripsTotal != 2 {
		t.Fatalf("a failed probe must reopen the breaker after one call: calls=%d status=%+v", stub.calls(), br.Status())
	}
}

func TestRecoverableWorkerFailureStillRetriesAndParksOnlyAfterMaxAttempts(t *testing.T) {
	seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-time.Hour), "A point. More"))
	stub, url := newWorkerStub(t, "flaky")
	clk := clock.NewFixed(t0.Add(time.Minute))
	s, br := breakerService(t, url, 5, clk, coalesce.Options{MaxAttempts: 3, RetryDelay: time.Second})

	for attempt := 1; attempt <= 3; attempt++ {
		if res, err := s.Drain(context.Background()); err == nil || res.Failed != 1 {
			t.Fatalf("attempt %d: %+v %v", attempt, res, err)
		}
		if parked, _ := s.Parked(context.Background()); (len(parked) == 1) != (attempt == 3) {
			t.Fatalf("attempt %d: parked=%+v (parks only once attempts are spent)", attempt, parked)
		}
		clk.Advance(2 * time.Second)
	}
	if stub.calls() != 3 || br.State() != providerbreaker.Closed {
		t.Fatalf("calls=%d state=%s: below the threshold the breaker stays closed and the job retried as before", stub.calls(), br.State())
	}
	stub.setMode("ok")
	br.Reset()
}

func TestLeaseIsRenewedWhileSerialModelCallsRunSoTheJobIsNeverDoubleClaimed(t *testing.T) {
	seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)),
		inbound(t, 1, t0.Add(-3*time.Hour), "One. More"), inbound(t, 2, t0.Add(-2*time.Hour), "Two. More"), inbound(t, 3, t0.Add(-time.Hour), "Three. More"))
	clk := clock.NewFixed(t0.Add(time.Minute))
	rival := newService(t, clk, coalesce.Options{WorkerID: "rival", Lease: 5 * time.Minute})
	ex := &claimstest.FakeExtractor{}
	stolen := 0
	// Each model call takes 4 minutes (inside the 5-minute lease); three serial calls take 12 minutes.
	ex.Err = func(claims.ExtractRequest) error {
		clk.Advance(4 * time.Minute)
		if job, err := rival.Claim(context.Background()); err != nil || job != nil {
			stolen++
		}
		return nil
	}
	s := newService(t, clk, coalesce.Options{WorkerID: "owner", Lease: 5 * time.Minute, Extractor: ex})

	res, err := s.Drain(context.Background())
	if err != nil || len(res.Recomputes) != 1 {
		t.Fatalf("the job must finish: %+v %v", res, err)
	}
	if stolen != 0 {
		t.Fatalf("the job was re-claimed %d times while its model calls were still running", stolen)
	}
	if got := len(ex.Calls()); got != 3 {
		t.Fatalf("model calls = %d, want 3 (one per activity, none repeated by a second worker)", got)
	}
}

func TestLostLeaseStopsTheJobBeforeAnotherModelCall(t *testing.T) {
	seedBasic(t)
	ingestAll(t, ingestService(t, clock.NewFixed(t0)), inbound(t, 1, t0.Add(-2*time.Hour), "One. More"), inbound(t, 2, t0.Add(-time.Hour), "Two. More"))
	clk := clock.NewFixed(t0.Add(time.Minute))
	ex := &claimstest.FakeExtractor{}
	ex.Err = func(claims.ExtractRequest) error { // another worker takes the job over during the first call
		_, err := env.DB.Exec(`UPDATE recompute_jobs SET claimed_by = 'thief'`)
		return err
	}
	s := newService(t, clk, coalesce.Options{WorkerID: "owner", Extractor: ex})
	if _, err := s.Drain(context.Background()); err == nil || !strings.Contains(err.Error(), "lease lost") {
		t.Fatalf("a job that lost its lease must stop: %v", err)
	}
	if got := len(ex.Calls()); got != 1 {
		t.Fatalf("model calls = %d, want 1: no further (billed) call after the lease is lost", got)
	}
}
