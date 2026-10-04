package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
)

func TestRunTokenKeyIsTheSecretOrDerivedFromTheAPIToken(t *testing.T) {
	const run = "11111111-1111-4111-8111-111111111111"
	now := time.Now()
	derived, err := newRunSigner(config.Config{APIToken: apiToken})
	if err != nil {
		t.Fatal(err)
	}
	again, _ := newRunSigner(config.Config{APIToken: apiToken})
	explicit, err := newRunSigner(config.Config{APIToken: apiToken, RunTokenSecret: strings.Repeat("s", 40)})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := derived.Issue(run, now)
	if got, err := again.Verify(tok, now); err != nil || got != run {
		t.Fatalf("a restart (same config) must accept earlier tokens: %q %v", got, err)
	}
	if _, err := explicit.Verify(tok, now); err == nil {
		t.Fatal("a different GHOST_RUN_TOKEN_SECRET accepted a token of the derived key")
	}
	if _, err := newRunSigner(config.Config{}); err != nil {
		t.Fatalf("an empty API token still derives a key (serve refuses it elsewhere): %v", err)
	}
}

func TestCoreServesRunScopedContextPullsOverHTTP(t *testing.T) {
	cfg := testConfig("127.0.0.1:0")
	base, stop := startCore(t, cfg)
	defer func() { _ = stop() }()

	var account, run string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name) VALUES ('Main wiring') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`WITH ev AS (
 INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
 VALUES ('email', 'wiring', 'received', encode(sha256(convert_to('wiring', 'UTF8')), 'hex'), '{}'::jsonb) RETURNING id),
 act AS (INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
 SELECT id, 'EmailReceived', 'email', 'wiring', now(), $1::uuid, '{"source_system":"email","source_object_id":"x"}'::jsonb FROM ev RETURNING id),
 te AS (INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes)
 VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied']) RETURNING id)
 INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
 SELECT $1::uuid, 'post_interaction_followup', 'dry_run', 'context_built', te.id, ARRAY[act.id] FROM te, act RETURNING id::text`, account).Scan(&run); err != nil {
		t.Fatal(err)
	}

	signer, err := newRunSigner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := signer.Issue(run, time.Now())
	if code, body := request(t, http.MethodGet, base+"/internal/ctx/activities", token, ""); code != 200 || !strings.Contains(body, `"tool":"activities"`) {
		t.Fatalf("context pull with a minted token = %d %s", code, body)
	}
	if code, _ := request(t, http.MethodGet, base+"/internal/ctx/activities", apiToken, ""); code != http.StatusUnauthorized {
		t.Fatalf("the operator token must not open /internal/ctx: %d", code)
	}
	other, _ := runtoken.NewSigner([]byte(strings.Repeat("z", 40)), time.Minute)
	forged, _ := other.Issue(run, time.Now())
	if code, _ := request(t, http.MethodGet, base+"/internal/ctx/activities", forged, ""); code != http.StatusUnauthorized {
		t.Fatalf("a token of another key = %d, want 401", code)
	}
	if code, body := request(t, http.MethodGet, base+"/accounts/"+account+"/timeline", apiToken, ""); code != 200 || !strings.Contains(body, `"items"`) {
		t.Fatalf("timeline over the real core = %d %s", code, body)
	}
	if code, _ := request(t, http.MethodGet, base+"/accounts/"+account+"/timeline", token, ""); code != http.StatusUnauthorized {
		t.Fatalf("a run token must not open operator endpoints: %d", code)
	}
}
