package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

const wiredEmail = `{"source_system":"email","source_object_id":"core-wired-1","source_event_key":"received","payload":{
 "kind":"email","message_id":"core-wired-1","thread_id":"t","direction":"inbound",
 "from":{"email":"priya@wired-test.example","name":"Priya"},"to":[{"email":"dana@ghostvendor.com"}],
 "date":"2026-10-03T10:00:00Z","subject":"hi","body_text":"We need the SOC2 report before signing. Thanks"}}`

const workerAnswer = `{"claims":[{"field_path":"blockers","value":"SOC2 report","confidence":0.9,"evidence_quote":"We need the SOC2 report before signing"}],
 "model":"stub-model","extractor_version":"extract-v1","dropped":0}`

func TestRunWiresTheCoalescerSoIngestedActivityBecomesAccountState(t *testing.T) {
	if err := storetest.Purge(context.Background(), env.DB, `TRUNCATE state_history, account_state, recompute_jobs, claims, extraction_cache, unresolved_activities,
		activity_participants, activities, source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO accounts (name, domain) VALUES ('Wired Co', 'wired-test.example')`); err != nil {
		t.Fatal(err)
	}
	// Ingest (WP5) writes graph edges with first_party_record standing; leave no rows behind that block a later migrate-down.
	t.Cleanup(func() {
		_ = storetest.Purge(context.Background(), env.DB, `TRUNCATE relationships, state_history, account_state, recompute_jobs, claims, extraction_cache, unresolved_activities,
			activity_participants, activities, source_events, entity_source_mappings, opportunities, people, accounts RESTART IDENTITY CASCADE`)
	})
	var calls atomic.Int32
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, workerAnswer)
	}))
	defer worker.Close()

	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = worker.URL
	base, stop := startCore(t, cfg)
	if code, body := request(t, http.MethodPost, base+"/ingest", apiToken, wiredEmail); code != http.StatusCreated {
		t.Fatalf("ingest = %d %s", code, body)
	}

	deadline := time.Now().Add(20 * time.Second)
	var blockers string
	for time.Now().Before(deadline) {
		err := env.DB.QueryRow(`SELECT state->'fields'->'blockers'->'value'->0->>'text' FROM account_state`).Scan(&blockers)
		if err == nil && blockers != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if blockers != "SOC2 report" {
		t.Fatalf("the coalescer did not turn the ingested email into state (blockers = %q, worker calls = %d)", blockers, calls.Load())
	}
	if err := stop(); err != nil {
		t.Fatalf("run returned %v after cancel", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("worker calls = %d, want 1", calls.Load())
	}
}

func TestRunStartsWithoutAWorkerURLUsingRuleExtractorsOnly(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = ""
	logger, logs := quietLogger()
	co, err := newCoalescer(env.DB, cfg, logger, newTestBreaker(t), nil)
	if err != nil || co == nil || !strings.Contains(logs.String(), "WORKER_URL is empty") {
		t.Fatalf("co=%v err=%v logs=%s", co, err, logs.String())
	}
	cfg.WorkerURL = "not a url"
	if _, err := newCoalescer(env.DB, cfg, logger, newTestBreaker(t), nil); err == nil {
		t.Fatal("an invalid worker URL must fail startup")
	}
}

func TestLiveModeWithoutTheWriteSwitchWarns(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	cfg.WorkerURL = ""
	cfg.RunMode, cfg.AllowExternalWrites = "live", false
	logger, logs := quietLogger()
	if _, err := newCoalescer(env.DB, cfg, logger, newTestBreaker(t), nil); err != nil || !strings.Contains(logs.String(), "runs stay dry_run") {
		t.Fatalf("err=%v logs=%s", err, logs.String())
	}
}

func TestCoalescePollIsHalfTheDebounceWithinBounds(t *testing.T) {
	for debounce, want := range map[time.Duration]time.Duration{
		0: 50 * time.Millisecond, 60 * time.Millisecond: 50 * time.Millisecond, 3 * time.Second: time.Second, 400 * time.Millisecond: 200 * time.Millisecond,
	} {
		if got := coalescePoll(config.Config{CoalesceDebounce: debounce}); got != want {
			t.Errorf("debounce %v: poll %v, want %v", debounce, got, want)
		}
	}
}

// Runs are structurally dry-run unless both the live mode and the explicit write switch are set.
func TestRunModeIsLiveOnlyWithBothSwitches(t *testing.T) {
	cases := []struct {
		mode  string
		allow bool
		want  string
	}{{"dry_run", false, "dry_run"}, {"dry_run", true, "dry_run"}, {"live", false, "dry_run"}, {"live", true, "live"}}
	for _, c := range cases {
		if got := runMode(config.Config{RunMode: c.mode, AllowExternalWrites: c.allow}); got != c.want {
			t.Errorf("mode %s allow %v: %s, want %s", c.mode, c.allow, got, c.want)
		}
	}
}
