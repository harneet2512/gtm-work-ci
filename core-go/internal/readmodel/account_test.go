package readmodel_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// seedAccount makes an account with one state headline, one activity and one open run, so the summary
// assertions do not depend on the shared world's mutable runs.
func seedAccount(t *testing.T, name string) (accountID, runID string) {
	t.Helper()
	accountID = scalar(t, `INSERT INTO accounts (name, domain) VALUES ($1, $2) RETURNING id::text`, name,
		strings.ToLower(strings.ReplaceAll(name, " ", ""))+".example.test")
	state := `{"headline":{"stage":{"value":"negotiation"},"health":{"value":"unknown"},"motion":{"value":"stalled"},` +
		`"last_meaningful_change":{"value":"buyer went quiet"}}}`
	// The current projection must already exist in history.
	exec(t, `INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, 1, now(), $2::jsonb)`, accountID, state)
	exec(t, `INSERT INTO account_state (account_id, version, as_of, state) VALUES ($1::uuid, 1, now(), $2::jsonb)`, accountID, state)
	act := scalar(t, `WITH se AS (
  INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
  VALUES ('email', $2, 'k', encode(sha256(convert_to($2::text, 'UTF8')), 'hex'), '{}'::jsonb, '2026-01-02T03:04:05Z') RETURNING id)
INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
SELECT id, 'EmailSent', 'email', $2, '2026-01-02T03:04:05Z', $1::uuid,
       ('{"source_system":"email","source_object_id":"' || $2 || '"}')::jsonb FROM se RETURNING id::text`,
		accountID, "obj-"+name)
	evalID := scalar(t, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation)
VALUES ($1::uuid, 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], 'test fixture') RETURNING id::text`, accountID)
	runID = scalar(t, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids, state_version)
VALUES ($1::uuid, 'post_interaction_followup', 'dry_run', 'awaiting_human', $2::uuid, ARRAY[$3::uuid], 1) RETURNING id::text`,
		accountID, evalID, act)
	return accountID, runID
}

func TestAccountSummaryReadsTheHeadlineTheLastActivityAndTheOpenRun(t *testing.T) {
	accountID, runID := seedAccount(t, "Solo Account Summary")

	a, err := reader(t).Account(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != accountID || a.Name != "Solo Account Summary" || a.Domain == nil || *a.Domain != "soloaccountsummary.example.test" {
		t.Fatalf("identity = %+v", a)
	}
	if a.Stage == nil || *a.Stage != "negotiation" {
		t.Fatalf("stage = %v, want the headline value", a.Stage)
	}
	if a.Health != nil {
		t.Fatalf("health = %v, want null (a headline value of 'unknown' reads as null)", *a.Health)
	}
	if a.Motion == nil || *a.Motion != "stalled" || a.LastMeaningfulChange == nil || *a.LastMeaningfulChange != "buyer went quiet" {
		t.Fatalf("headline = %+v", a)
	}
	wantAt, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if a.LastActivityAt == nil || !a.LastActivityAt.Equal(wantAt) {
		t.Fatalf("last_activity_at = %v", a.LastActivityAt)
	}
	if a.OpenRunID == nil || *a.OpenRunID != runID {
		t.Fatalf("open_run_id = %v, want %s", a.OpenRunID, runID)
	}
}

func TestAccountSummaryIsMostlyNullWithoutStateOrRuns(t *testing.T) {
	accountID := scalar(t, `INSERT INTO accounts (name) VALUES ('Bare Account') RETURNING id::text`)

	a, err := reader(t).Account(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Bare Account" || a.Domain != nil {
		t.Fatalf("identity = %+v", a)
	}
	if a.Stage != nil || a.Health != nil || a.Motion != nil || a.LastMeaningfulChange != nil ||
		a.LastActivityAt != nil || a.OpenRunID != nil {
		t.Fatalf("an account without state or runs must read all-null: %+v", a)
	}
}

func TestAccountSummaryDropsTheOpenRunWhenItCloses(t *testing.T) {
	accountID, runID := seedAccount(t, "Closing Account")

	exec(t, `UPDATE agent_runs SET status = 'rejected', updated_at = now() WHERE id = $1::uuid`, runID)
	a, err := reader(t).Account(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if a.OpenRunID != nil {
		t.Fatalf("a closed run still reads as open_run_id %s", *a.OpenRunID)
	}
}

func TestAccountSummaryErrors(t *testing.T) {
	for _, id := range []string{missing, "not-a-uuid"} {
		if _, err := reader(t).Account(context.Background(), id); !errors.Is(err, readmodel.ErrNotFound) {
			t.Errorf("account %q: %v, want ErrNotFound", id, err)
		}
	}
}
