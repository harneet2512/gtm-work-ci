// Package runs creates AgentRuns from eligible trigger evaluations, records their typed steps, executes
// the final step through an Executor that cannot write externally in dry_run mode, and reads a run's
// trace back to the activities and state that caused it (HAR-96 §11, §18).
package runs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

// Run modes (agent_run.v1.json runMode).
const (
	DryRun = "dry_run"
	Live   = "live"
)

// StepNames are the typed steps of the post-interaction follow-up workflow, in order
// (agent_run.v1.json steps[].step).
var StepNames = []string{"build_context", "draft", "crm_intent", "await_human", "execute"}

// ErrOpenRun means the account already has an unfinished run of the workflow.
var ErrOpenRun = errors.New("runs: the account already has an open run")

// New describes a run to create from an eligible evaluation.
type New struct {
	AccountID          string
	OpportunityID      string // "" when the account has none
	Mode               string
	EvaluationID       string
	TriggerActivityIDs []string
	CorrelationID      string // "" when unknown
	StateVersion       int
}

// Create inserts the run and its pending steps in the caller's transaction. The database accepts only an
// eligible evaluation (composite foreign key) and one open run per account. Creating the run of an
// evaluation that already has one returns that run with created=false.
func Create(ctx context.Context, db claimstore.DB, n New) (id string, created bool, err error) {
	if n.Mode != DryRun && n.Mode != Live {
		return "", false, fmt.Errorf("runs: run mode %q is not dry_run or live", n.Mode)
	}
	if len(n.TriggerActivityIDs) == 0 {
		return "", false, errors.New("runs: a run needs at least one trigger activity")
	}
	err = db.QueryRowContext(ctx, `
INSERT INTO agent_runs (account_id, opportunity_id, workflow, run_mode, trigger_evaluation_id, trigger_activity_ids, correlation_id, state_version)
VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6::uuid[], $7::uuid, $8)
ON CONFLICT DO NOTHING RETURNING id::text`,
		n.AccountID, nullable(n.OpportunityID), trigger.Workflow, n.Mode, n.EvaluationID,
		signalstore.UUIDArray(n.TriggerActivityIDs), nullable(n.CorrelationID), n.StateVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return existing(ctx, db, n)
	}
	if err != nil {
		return "", false, fmt.Errorf("runs: insert run for evaluation %s: %w", n.EvaluationID, err)
	}
	for i, step := range StepNames {
		if _, err := db.ExecContext(ctx, `
INSERT INTO agent_run_steps (agent_run_id, seq, step, run_mode, status) VALUES ($1::uuid, $2, $3, $4, 'pending')`,
			id, i+1, step, n.Mode); err != nil {
			return "", false, fmt.Errorf("runs: insert step %s of run %s: %w", step, id, err)
		}
	}
	return id, true, nil
}

// existing resolves a conflicting insert: the evaluation's own run, or an open run of the account.
func existing(ctx context.Context, db claimstore.DB, n New) (string, bool, error) {
	var id, account, mode string
	err := db.QueryRowContext(ctx, `SELECT id::text, account_id::text, run_mode FROM agent_runs WHERE trigger_evaluation_id = $1::uuid`,
		n.EvaluationID).Scan(&id, &account, &mode)
	if err == nil {
		if account != n.AccountID || mode != n.Mode {
			return "", false, fmt.Errorf("runs: evaluation %s already has run %s of another account or mode", n.EvaluationID, id)
		}
		return id, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("runs: read run of evaluation %s: %w", n.EvaluationID, err)
	}
	var open bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agent_runs WHERE account_id = $1::uuid AND workflow = $2
	  AND status IN ('pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited'))`, n.AccountID, trigger.Workflow).Scan(&open); err != nil {
		return "", false, fmt.Errorf("runs: check open run of %s: %w", n.AccountID, err)
	}
	if !open {
		return "", false, fmt.Errorf("runs: run for evaluation %s conflicted with nothing we know of", n.EvaluationID)
	}
	return "", false, ErrOpenRun
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
