package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

func TestJobsUsage(t *testing.T) {
	for _, args := range [][]string{{"jobs", "bogus"}, {"jobs", "unquarantine"}, {"jobs", "unquarantine", "a", "b"}} {
		if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestFormatJobsNamesAccountsCausesAndAge(t *testing.T) {
	var out bytes.Buffer
	formatJobs(&out,
		[]coalesce.ParkedJob{{JobID: 7, AccountID: "acct-1", AccountName: "Acme", ActivityCount: 3, Attempts: 5, LastError: "diff writer down", Age: 90 * time.Second}},
		[]coalesce.QuarantinedActivity{{ActivityID: "act-9", AccountID: "acct-2", Reason: "worker 422", Permanent: true, Attempts: 1, Age: time.Hour}})
	for _, want := range []string{"parked jobs: 1", "acct-1 (Acme)", "diff writer down", "1m30s", "quarantined activities: 1", "act-9", "permanent", "worker 422", "1h0m0s"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

func TestJobsCommandListsAndUnquarantinesAgainstTestDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)

	var acct, act string
	if err := env.DB.QueryRow(`INSERT INTO accounts (name) VALUES ('Acme') RETURNING id::text`).Scan(&acct); err != nil {
		t.Fatal(err)
	}
	var ev string
	if err := env.DB.QueryRow(`INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
		VALUES ('email', 'm1', 'received', repeat('a', 64), '{}'::jsonb) RETURNING id::text`).Scan(&ev); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.QueryRow(`INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
		VALUES ($1::uuid, 'EmailReceived', 'email', 'm1', now(), $2::uuid, '{"source_system":"email","source_object_id":"m1"}'::jsonb) RETURNING id::text`, ev, acct).Scan(&act); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO quarantined_activities (activity_id, account_id, reason, permanent, attempts) VALUES ($1::uuid, $2::uuid, 'worker 422 invalid', true, 1)`, act, acct); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`INSERT INTO recompute_jobs (account_id, due_at, first_enqueued_at, activity_ids, claimed_at, claimed_by, lease_expires_at, attempts, parked_at, last_error)
		VALUES ($1::uuid, now(), now(), ARRAY[$2::uuid], now(), 'w', 'infinity', 5, now(), 'diff writer down')`, acct, act); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"jobs"}, &out); err != nil {
		t.Fatalf("jobs: %v", err)
	}
	for _, want := range []string{"parked jobs: 1", acct, "diff writer down", "quarantined activities: 1", act, "worker 422 invalid"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "ghost:ghost") {
		t.Errorf("credentials leaked: %s", out.String())
	}
	out.Reset()
	if err := run([]string{"jobs", "unquarantine", act}, &out); err != nil || !strings.Contains(out.String(), "released from quarantine") {
		t.Fatalf("unquarantine: %v %s", err, out.String())
	}
	if err := run([]string{"jobs", "unquarantine", act}, &out); err == nil || !strings.Contains(err.Error(), "not quarantined") {
		t.Fatalf("a second unquarantine must say so: %v", err)
	}
}
