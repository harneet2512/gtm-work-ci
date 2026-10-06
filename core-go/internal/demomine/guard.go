package demomine

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// worldTables are the tables a replay writes first and everything else hangs off: if any holds a row, the
// database is not an empty world and a freeze into it would mix the replay with foreign data.
var worldTables = []string{"source_events", "accounts", "agent_runs"}

// openRunStatuses are the agent_runs statuses that still make an account ineligible (open_run_exists).
const openRunStatuses = `'pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited'`

// RequireEmpty refuses a database that already holds source events, accounts or agent runs. --into-database
// writes a whole replay world; it must never be pointed at a database somebody already uses.
func RequireEmpty(ctx context.Context, db *sql.DB) error {
	for _, table := range worldTables { // fixed names, never user input
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+`)`).Scan(&exists); err != nil {
			return fmt.Errorf("demomine: check that %s is empty: %w", table, err)
		}
		if exists {
			return fmt.Errorf("demomine: refusing to materialize into a database that is not empty (%s has rows): "+
				"point DATABASE_URL at a fresh database, one per frozen case", table)
		}
	}
	return nil
}

// closeRuns cancels the dry-run placeholders the pipeline opened for one state diff, so an open run does not
// make the next event ineligible (open_run_exists): every event is judged on what it changed. It touches no
// run of any other diff, and stamps updated_at with the replay clock (at), never the wall clock.
func closeRuns(ctx context.Context, db *sql.DB, stateDiffID string, at time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE agent_runs SET status = 'cancelled', updated_at = $2::timestamptz
 WHERE run_mode = 'dry_run' AND status IN (`+openRunStatuses+`)
   AND trigger_evaluation_id IN (SELECT id FROM trigger_evaluations WHERE state_diff_id = $1::uuid)`, stateDiffID, at.UTC())
	if err != nil {
		return fmt.Errorf("demomine: close the dry runs of state diff %s: %w", stateDiffID, err)
	}
	return nil
}
