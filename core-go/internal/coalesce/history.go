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

// Basis says which clock an as-of read of state_history uses.
type Basis string

const (
	// KnownAt selects by when the state was computed: "what did we believe at T" (computed_at).
	KnownAt Basis = "known_at"
	// WorldAsOf selects by the newest activity folded into the state: "what was true up to T" (as_of).
	WorldAsOf Basis = "world_as_of"
)

// column is the state_history column the basis selects by.
func (b Basis) column() (string, error) {
	switch b {
	case KnownAt:
		return "computed_at", nil
	case WorldAsOf:
		return "as_of", nil
	}
	return "", fmt.Errorf("coalesce: unknown as-of basis %q", b)
}

// StateAt returns the account's state as it stood at t: the latest state_history version whose
// computed_at (KnownAt) or as_of (WorldAsOf) is not after t. found is false when no version
// existed yet. History rows are immutable, so the answer for a given t never changes afterwards.
func StateAt(ctx context.Context, db claimstore.DB, accountID string, t time.Time, basis Basis) (st reducer.AccountState, found bool, err error) {
	column, err := basis.column()
	if err != nil {
		return st, false, err
	}
	var raw []byte
	err = db.QueryRowContext(ctx, `
SELECT state FROM state_history WHERE account_id = $1::uuid AND `+column+` <= $2 ORDER BY `+column+` DESC, version DESC LIMIT 1`,
		accountID, t.UTC()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, false, nil
	}
	if err != nil {
		return st, false, fmt.Errorf("coalesce: read state history of %s at %s: %w", accountID, t.Format(time.RFC3339), err)
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, false, fmt.Errorf("coalesce: decode state history of %s: %w", accountID, err)
	}
	return st, true, nil
}

// StateJSONBefore returns the stored document of the account's state as the world stood strictly before
// t (ADR-0019): the highest version whose as_of (the occurred_at of the newest activity folded into it)
// is earlier than t. Such a version folds nothing that happened at or after t. found is false when no
// version qualifies; there is deliberately no fallback to a later version, which would be the leak.
//
// A version that coalesced a burst has the burst's newest activity as its as_of, so a t inside the burst
// answers with the version before it: the read can under-report and never over-reports.
func StateJSONBefore(ctx context.Context, db claimstore.DB, accountID string, t time.Time) (raw []byte, found bool, err error) {
	err = db.QueryRowContext(ctx, `
SELECT state FROM state_history WHERE account_id = $1::uuid AND as_of < $2 ORDER BY as_of DESC, version DESC LIMIT 1`,
		accountID, t.UTC()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("coalesce: read state history of %s before %s: %w", accountID, t.Format(time.RFC3339Nano), err)
	}
	return raw, true, nil
}

// StateBefore is StateJSONBefore decoded into an AccountState.
func StateBefore(ctx context.Context, db claimstore.DB, accountID string, t time.Time) (st reducer.AccountState, found bool, err error) {
	raw, found, err := StateJSONBefore(ctx, db, accountID, t)
	if err != nil || !found {
		return st, false, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, false, fmt.Errorf("coalesce: decode state history of %s: %w", accountID, err)
	}
	return st, true, nil
}

// StateVersion returns the stored document and as_of of one state_history version (a run is pinned to the
// version it was built on). found is false when the version does not exist.
func StateVersion(ctx context.Context, db claimstore.DB, accountID string, version int) (raw []byte, asOf time.Time, found bool, err error) {
	err = db.QueryRowContext(ctx, `SELECT state, as_of FROM state_history WHERE account_id = $1::uuid AND version = $2`,
		accountID, version).Scan(&raw, &asOf)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, asOf, false, nil
	}
	if err != nil {
		return nil, asOf, false, fmt.Errorf("coalesce: read state version %d of %s: %w", version, accountID, err)
	}
	return raw, asOf.UTC(), true, nil
}
