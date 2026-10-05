package demorun

import (
	"context"
	"database/sql"
	"fmt"
)

// maxRunsListed bounds how many of an account's runs one poll reads.
const maxRunsListed = 20

// DemoCore is the PlayCore the live demo uses: core's HTTP API for everything it serves, and Postgres for the one
// thing it does not, the list of the runs a Play opened (GET /runs answers 404 until WP9 lands). Run ids come from
// agent_runs by the same trigger-activity link the run driver uses for GHOST_ORCHESTRATOR_SCOPE=play, and each
// run's phase from GET /runs/{id}.
type DemoCore struct {
	CoreClient
	DB *sql.DB
}

// PlayRuns lists the runs whose trigger activity is the Play's released event, newest first by creation row.
func (d DemoCore) PlayRuns(ctx context.Context, manifestID string) ([]RunInfo, error) {
	rows, err := d.DB.QueryContext(ctx, `
		SELECT r.id::text FROM agent_runs r
		WHERE EXISTS (SELECT 1 FROM demo_plays p WHERE p.manifest_id = $1::uuid AND p.activity_id = ANY (r.trigger_activity_ids))
		ORDER BY r.created_at DESC, r.id DESC LIMIT $2`, manifestID, maxRunsListed)
	if err != nil {
		return nil, fmt.Errorf("list the runs of the Play: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]RunInfo, 0, len(ids))
	for _, id := range ids {
		r, err := d.CoreClient.Run(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read run %s: %w", id, err)
		}
		out = append(out, r)
	}
	return out, nil
}
