package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
)

// StateWorldAsOf returns the account's AccountState as the world stood strictly before t (ADR-0019): the
// stored document of the highest version whose as_of is earlier than t, unchanged. ErrNotFound: no such
// account. ErrNoStateBefore: no version qualifies (there is no fallback to a later one).
func (r *Reader) StateWorldAsOf(ctx context.Context, accountID string, t time.Time) (json.RawMessage, error) {
	if err := requireUUID("account", accountID); err != nil {
		return nil, err
	}
	if err := accountExists(ctx, r.db, accountID); err != nil {
		return nil, err
	}
	raw, found, err := coalesce.StateJSONBefore(ctx, r.db, accountID, t)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoStateBefore
	}
	return raw, nil
}

// State returns the account's AccountState document: the current projection, or, with asOf, the
// state the core believed at that time (state_history, known_at basis, via coalesce.StateAt).
// ErrNotFound: no such account. ErrNoState: the account has no state (yet, or at asOf).
func (r *Reader) State(ctx context.Context, accountID string, asOf *time.Time) (json.RawMessage, error) {
	if err := requireUUID("account", accountID); err != nil {
		return nil, err
	}
	if err := accountExists(ctx, r.db, accountID); err != nil {
		return nil, err
	}
	if asOf != nil {
		st, found, err := coalesce.StateAt(ctx, r.db, accountID, *asOf, coalesce.KnownAt)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrNoState
		}
		raw, err := json.Marshal(st)
		if err != nil {
			return nil, fmt.Errorf("readmodel: encode state: %w", err)
		}
		return raw, nil
	}
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT state FROM account_state WHERE account_id = $1::uuid`, accountID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, fmt.Errorf("readmodel: read state of %s: %w", accountID, err)
	}
	return raw, nil
}
