package strategystore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// linkSendResults records which eval results are the send-time re-evaluation of the final artifact of the
// decision (HAR-97 E11). eval_runs rows are content-addressed, and a refused send persists its own results
// before the human repairs the draft, so the batch that was actually sent cannot be told from the table alone:
// the recomputation read (GET /runs/{run_id}/recomputation) joins through this link. It runs in the send
// transaction, so a send that rolls back links nothing. Each row also stores the version and digest of the
// account state the evaluation read: the proof behind "account state preserved".
func linkSendResults(ctx context.Context, tx *sql.Tx, decisionID string, sev sendEval) error {
	ids := make([]string, 0, len(sev.Results))
	for _, r := range sev.Results {
		ids = append(ids, r.ID)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO send_eval_results (human_strategy_decision_id, eval_run_id, state_version, state_hash)
 SELECT $1::uuid, id, $3::int, $4::text FROM unnest($2::uuid[]) AS id ON CONFLICT DO NOTHING`,
		decisionID, signalstore.UUIDArray(ids), sev.StateVersion, sev.StateHash); err != nil {
		return fmt.Errorf("strategystore: link the send-time evals of decision %s: %w", decisionID, err)
	}
	return nil
}
