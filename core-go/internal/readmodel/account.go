package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Account is GET /accounts/{account_id} (core.yaml AccountSummary): the account's headline for list and
// detail reads. Only id and name are contract-required; the rest is null until state or activity exists.
type Account struct {
	ID                   string     `json:"id"`
	Name                 string     `json:"name"`
	Domain               *string    `json:"domain"`
	Stage                *string    `json:"stage"`
	Health               *string    `json:"health"`
	Motion               *string    `json:"motion"`
	LastMeaningfulChange *string    `json:"last_meaningful_change"`
	LastActivityAt       *time.Time `json:"last_activity_at"`
	OpenRunID            *string    `json:"open_run_id"`
}

// openRunStatuses is the "unfinished run" set of the agent_runs_one_open_uniq partial index
// (migration 0004): open_run_id is the newest of those, at most one per workflow.
const openRunStatuses = `'pending', 'context_built', 'drafted', 'awaiting_human', 'approved', 'edited'`

// Account returns the AccountSummary of one account. ErrNotFound: no such account. The state-backed
// fields (stage, health, motion, last_meaningful_change) read the current projection's headline; a
// headline value of "unknown" or a missing state leaves the field null.
func (r *Reader) Account(ctx context.Context, accountID string) (Account, error) {
	if err := requireUUID("account", accountID); err != nil {
		return Account{}, err
	}
	out := Account{ID: accountID}
	var domain sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT name, domain FROM accounts WHERE id = $1::uuid`, accountID).Scan(&out.Name, &domain)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("readmodel: read account %s: %w", accountID, err)
	}
	out.Domain = domainOr(domain)
	headline, err := r.headline(ctx, accountID)
	if err != nil {
		return Account{}, err
	}
	out.Stage, out.Health, out.Motion, out.LastMeaningfulChange = headline["stage"], headline["health"], headline["motion"], headline["last_meaningful_change"]
	if err := r.db.QueryRowContext(ctx,
		`SELECT max(occurred_at) FROM activities WHERE account_id = $1::uuid`, accountID).Scan(&out.LastActivityAt); err != nil {
		return Account{}, fmt.Errorf("readmodel: last activity of %s: %w", accountID, err)
	}
	var open sql.NullString
	err = r.db.QueryRowContext(ctx, `SELECT id::text FROM agent_runs WHERE account_id = $1::uuid AND status IN (`+openRunStatuses+`)
ORDER BY created_at DESC LIMIT 1`, accountID).Scan(&open)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("readmodel: open run of %s: %w", accountID, err)
	}
	out.OpenRunID = domainOr(open)
	return out, nil
}

// headline reads stage/health/motion/last_meaningful_change values out of the current account_state
// document; "unknown" and a missing state both read as null.
func (r *Reader) headline(ctx context.Context, accountID string) (map[string]*string, error) {
	out := map[string]*string{}
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT state FROM account_state WHERE account_id = $1::uuid`, accountID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("readmodel: read state of %s: %w", accountID, err)
	}
	var doc struct {
		Headline map[string]struct {
			Value *string `json:"value"`
		} `json:"headline"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("readmodel: decode state of %s: %w", accountID, err)
	}
	for _, f := range []string{"stage", "health", "motion", "last_meaningful_change"} {
		if v, ok := doc.Headline[f]; ok && v.Value != nil && *v.Value != "unknown" {
			out[f] = v.Value
		}
	}
	return out, nil
}

// domainOr is the nullable-string helper shared by domain and open_run_id.
func domainOr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
