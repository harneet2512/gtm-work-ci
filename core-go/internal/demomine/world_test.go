package demomine

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// bareConn is a connection on which foreign keys are not enforced, so a test can plant one row of a table
// without building its whole parent graph.
func bareConn(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.ExecContext(context.Background(), `SET session_replication_role = replica`); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRequireEmptyRefusesEveryNonEmptyTable(t *testing.T) {
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	ctx := context.Background()
	if err := RequireEmpty(ctx, env.DB); err != nil {
		t.Fatalf("a fresh database was refused: %v", err)
	}
	plant := map[string]string{
		"source_events": `INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload)
			VALUES ('crm', 'o', 'k', repeat('a', 64), '{}'::jsonb)`,
		"accounts": `INSERT INTO accounts (name) VALUES ('existing')`,
		"agent_runs": `INSERT INTO agent_runs (account_id, workflow, run_mode, trigger_evaluation_id, trigger_activity_ids)
			VALUES (gen_random_uuid(), 'post_interaction_followup', 'dry_run', gen_random_uuid(), ARRAY[gen_random_uuid()])`,
	}
	for table, stmt := range plant {
		conn := bareConn(t, env.DB)
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("plant %s: %v", table, err)
		}
		err := RequireEmpty(ctx, env.DB)
		if err == nil || !strings.Contains(err.Error(), table) {
			t.Errorf("a database with a row in %s must be refused naming the table, got %v", table, err)
		}
		if _, err := conn.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := RequireEmpty(ctx, env.DB); err != nil {
		t.Fatalf("the emptied database was refused: %v", err)
	}
}

// plantRun inserts an open dry run whose trigger evaluation belongs to the given state diff.
func plantRun(t *testing.T, c *sql.Conn, diffID string) string {
	t.Helper()
	var evalID, runID string
	ctx := context.Background()
	if err := c.QueryRowContext(ctx, `INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, state_diff_id)
		VALUES (gen_random_uuid(), 'post_interaction_followup', true, ARRAY['eligible_customer_replied'], $1::uuid) RETURNING id::text`, diffID).Scan(&evalID); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(ctx, `INSERT INTO agent_runs (account_id, workflow, run_mode, status, trigger_evaluation_id, trigger_activity_ids)
		VALUES (gen_random_uuid(), 'post_interaction_followup', 'dry_run', 'pending', $1::uuid, ARRAY[gen_random_uuid()]) RETURNING id::text`, evalID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	return runID
}

func TestCloseRunsOnlyTouchesTheRunsOfThatStateDiff(t *testing.T) {
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	ctx := context.Background()
	conn := bareConn(t, env.DB)
	const mine, other = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	ours, unrelated := plantRun(t, conn, mine), plantRun(t, conn, other)
	replayTime := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)

	if err := closeRuns(ctx, env.DB, mine, replayTime); err != nil {
		t.Fatal(err)
	}
	read := func(id string) (status string, updated time.Time) {
		if err := env.DB.QueryRow(`SELECT status, updated_at FROM agent_runs WHERE id = $1::uuid`, id).Scan(&status, &updated); err != nil {
			t.Fatal(err)
		}
		return status, updated
	}
	if st, at := read(ours); st != "cancelled" || !at.Equal(replayTime) {
		t.Errorf("the pipeline's run: status %s updated_at %s, want cancelled at the replay clock %s", st, at, replayTime)
	}
	st, at := read(unrelated)
	if st != "pending" {
		t.Errorf("an unrelated open run was changed to %s", st)
	}
	if at.Equal(replayTime) {
		t.Error("an unrelated run's updated_at was rewritten")
	}
}
