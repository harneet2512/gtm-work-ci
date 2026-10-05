package signalstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/trigger"
)

// StoredEvaluation is a persisted trigger evaluation.
type StoredEvaluation struct {
	ID       string
	Eligible bool
	// Inserted is false when the diff already had an evaluation of this workflow (a retried recompute);
	// the existing decision is returned unchanged.
	Inserted bool
}

// InsertEvaluation persists the decision, eligible or not, with its reason codes and the signals behind
// it. One diff has at most one evaluation per workflow (unique index, migration 0016).
func InsertEvaluation(ctx context.Context, db claimstore.DB, accountID, diffID string, e trigger.Evaluation, signalIDs []string, at time.Time) (StoredEvaluation, error) {
	var out StoredEvaluation
	err := db.QueryRowContext(ctx, `
INSERT INTO trigger_evaluations (account_id, workflow, eligible, reason_codes, explanation, signal_ids, state_diff_id, evaluated_at)
VALUES ($1::uuid, $2, $3, $4::text[], $5, $6::uuid[], $7::uuid, $8)
ON CONFLICT (state_diff_id, workflow) WHERE state_diff_id IS NOT NULL DO NOTHING RETURNING id::text, eligible`,
		accountID, e.Workflow, e.Eligible, UUIDArray(e.ReasonCodes), e.Explanation, UUIDArray(signalIDs), diffID, at.UTC()).Scan(&out.ID, &out.Eligible)
	if err == nil {
		out.Inserted = true
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("signalstore: insert trigger evaluation for diff %s: %w", diffID, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text, eligible FROM trigger_evaluations WHERE state_diff_id = $1::uuid AND workflow = $2`,
		diffID, e.Workflow).Scan(&out.ID, &out.Eligible); err != nil {
		return out, fmt.Errorf("signalstore: read existing evaluation of diff %s: %w", diffID, err)
	}
	return out, nil
}

// OpenRunExists reports whether the account has an unfinished run of the workflow (agent_runs_one_open_uniq).
func OpenRunExists(ctx context.Context, db claimstore.DB, accountID string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM agent_runs WHERE account_id = $1::uuid AND workflow = $2
                AND status IN ('pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited'))`,
		accountID, trigger.Workflow).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("signalstore: check open run of %s: %w", accountID, err)
	}
	return exists, nil
}

// LastEligibleAt is when the account's latest eligible evaluation was made, or nil.
func LastEligibleAt(ctx context.Context, db claimstore.DB, accountID string) (*time.Time, error) {
	var at sql.NullTime
	err := db.QueryRowContext(ctx, `SELECT max(evaluated_at) FROM trigger_evaluations WHERE account_id = $1::uuid AND workflow = $2 AND eligible`,
		accountID, trigger.Workflow).Scan(&at)
	if err != nil {
		return nil, fmt.Errorf("signalstore: read last eligible evaluation of %s: %w", accountID, err)
	}
	if !at.Valid {
		return nil, nil
	}
	t := at.Time.UTC()
	return &t, nil
}
