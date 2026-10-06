package coalesce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// persistOpportunities writes the deals' states in the recompute transaction (ADR-0016). A deal's version
// advances only when its content changed, so an unrelated recompute leaves it, and its history, alone.
func persistOpportunities(ctx context.Context, tx *sql.Tx, deals []reducer.OpportunityState, trigger []string) error {
	for _, deal := range deals {
		prev, err := lockPreviousOpportunity(ctx, tx, deal.OpportunityID)
		if err != nil {
			return err
		}
		if prev != nil {
			same, err := sameContent(*prev, deal)
			if err != nil {
				return err
			}
			if same {
				continue
			}
		}
		deal.Version = 1
		if prev != nil {
			deal.Version = prev.Version + 1
		}
		if err := writeOpportunity(ctx, tx, prev, deal, trigger); err != nil {
			return err
		}
	}
	return nil
}

// lockPreviousOpportunity reads and row-locks the deal's current state; nil when it has none yet.
func lockPreviousOpportunity(ctx context.Context, tx *sql.Tx, opportunityID string) (*reducer.OpportunityState, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT state FROM opportunity_state WHERE opportunity_id = $1::uuid FOR UPDATE`, opportunityID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("coalesce: read previous state of deal %s: %w", opportunityID, err)
	}
	var prev reducer.OpportunityState
	if err := json.Unmarshal(raw, &prev); err != nil {
		return nil, fmt.Errorf("coalesce: decode previous state of deal %s: %w", opportunityID, err)
	}
	return &prev, nil
}

// sameContent compares two states ignoring what a recompute changes without the deal changing: the version
// and computed_at, and as_of for a deal without activity (there it is the recompute time).
func sameContent(prev, next reducer.OpportunityState) (bool, error) {
	a, err := contentKey(prev)
	if err != nil {
		return false, err
	}
	b, err := contentKey(next)
	return a == b, err
}

func contentKey(st reducer.OpportunityState) (string, error) {
	raw, err := json.Marshal(st) // round trip so a stored state and a fresh one are compared in the same shape
	if err != nil {
		return "", fmt.Errorf("coalesce: encode state of deal %s: %w", st.OpportunityID, err)
	}
	var norm reducer.OpportunityState
	if err := json.Unmarshal(raw, &norm); err != nil {
		return "", fmt.Errorf("coalesce: normalize state of deal %s: %w", st.OpportunityID, err)
	}
	norm.Version, norm.ComputedAt = 0, time.Time{}
	if norm.LastActivityID == nil {
		norm.AsOf = time.Time{}
	}
	out, err := json.Marshal(norm)
	return string(out), err
}

// writeOpportunity appends the history row and upserts the current row with an optimistic version check.
func writeOpportunity(ctx context.Context, tx *sql.Tx, prev *reducer.OpportunityState, st reducer.OpportunityState, trigger []string) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("coalesce: encode state of deal %s: %w", st.OpportunityID, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO opportunity_state_history (opportunity_id, account_id, version, as_of, computed_at, trigger_activity_ids, state)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid[], $7::jsonb)`,
		st.OpportunityID, st.AccountID, st.Version, st.AsOf, st.ComputedAt, pgTextArray(trigger), string(raw)); err != nil {
		if isUniqueViolation(err) {
			return ErrVersionConflict
		}
		return fmt.Errorf("coalesce: insert state history of deal %s v%d: %w", st.OpportunityID, st.Version, err)
	}
	last := nullable(st.LastActivityID)
	if prev == nil {
		_, err = tx.ExecContext(ctx, `
INSERT INTO opportunity_state (opportunity_id, account_id, version, as_of, computed_at, last_activity_id, is_open, state)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8::jsonb)`,
			st.OpportunityID, st.AccountID, st.Version, st.AsOf, st.ComputedAt, last, st.IsOpen, string(raw))
		if isUniqueViolation(err) {
			return ErrVersionConflict
		}
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `
UPDATE opportunity_state SET version = $2, as_of = $3, computed_at = $4, last_activity_id = $5::uuid, is_open = $6, state = $7::jsonb
 WHERE opportunity_id = $1::uuid AND version = $8`,
			st.OpportunityID, st.Version, st.AsOf, st.ComputedAt, last, st.IsOpen, string(raw), prev.Version)
		if err == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrVersionConflict
			}
		}
	}
	if err != nil {
		return fmt.Errorf("coalesce: write state of deal %s v%d: %w", st.OpportunityID, st.Version, err)
	}
	return nil
}

// OpportunityState returns the current state of one deal of the account; found is false when the deal has
// none (no claim or activity names it yet). A deal of another account is never returned.
func OpportunityState(ctx context.Context, db claimstore.DB, accountID, opportunityID string) (st reducer.OpportunityState, found bool, err error) {
	var raw []byte
	err = db.QueryRowContext(ctx, `SELECT state FROM opportunity_state WHERE opportunity_id = $1::uuid AND account_id = $2::uuid`,
		opportunityID, accountID).Scan(&raw)
	return decodeOpportunity(raw, err, accountID, opportunityID)
}

// OpportunityStateAt returns the deal's state as it stood at t, on the same two clocks as StateAt: the latest
// version of the deal whose computed_at (KnownAt) or as_of (WorldAsOf) is not after t. found is false when
// no version existed yet or the deal belongs to another account. History rows are immutable.
func OpportunityStateAt(ctx context.Context, db claimstore.DB, accountID, opportunityID string, t time.Time, basis Basis) (st reducer.OpportunityState, found bool, err error) {
	column, err := basis.column()
	if err != nil {
		return st, false, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, `
SELECT state FROM opportunity_state_history WHERE opportunity_id = $1::uuid AND account_id = $2::uuid AND `+column+` <= $3
 ORDER BY `+column+` DESC, version DESC LIMIT 1`, opportunityID, accountID, t.UTC()).Scan(&raw)
	return decodeOpportunity(raw, err, accountID, opportunityID)
}

func decodeOpportunity(raw []byte, queryErr error, accountID, opportunityID string) (st reducer.OpportunityState, found bool, err error) {
	if errors.Is(queryErr, sql.ErrNoRows) {
		return st, false, nil
	}
	if queryErr != nil {
		return st, false, fmt.Errorf("coalesce: read state of deal %s of account %s: %w", opportunityID, accountID, queryErr)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, false, fmt.Errorf("coalesce: decode state of deal %s: %w", opportunityID, err)
	}
	return st, true, nil
}
