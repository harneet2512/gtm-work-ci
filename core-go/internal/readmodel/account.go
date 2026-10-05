package readmodel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
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
	var out Account
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		accounts, err := loadAccounts(ctx, db, []string{accountID})
		if err != nil {
			return err
		}
		a, ok := accounts[accountID]
		if !ok {
			return ErrNotFound
		}
		out = a
		return nil
	})
	return out, err
}

// idList is the argument of `= ANY(string_to_array($1, ',')::uuid[])`: one statement for many ids.
func idList(ids []string) string { return strings.Join(ids, ",") }

// loadAccounts reads the AccountSummary of every account in ids with four statements however many there are (the
// account, its current state, its newest activity and its open run), keyed by id. An unknown id is absent.
func loadAccounts(ctx context.Context, db claimstore.DB, ids []string) (map[string]Account, error) {
	out := map[string]Account{}
	if len(ids) == 0 {
		return out, nil
	}
	list := idList(ids)
	rows, err := db.QueryContext(ctx, `SELECT id::text, name, domain FROM accounts WHERE id = ANY(string_to_array($1, ',')::uuid[])`, list)
	if err != nil {
		return nil, fmt.Errorf("readmodel: read accounts: %w", err)
	}
	for rows.Next() {
		var a Account
		var domain sql.NullString
		if err := rows.Scan(&a.ID, &a.Name, &domain); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("readmodel: scan account: %w", err)
		}
		a.Domain = domainOr(domain)
		out[a.ID] = a
	}
	if err := closeRows(rows, "accounts"); err != nil {
		return nil, err
	}
	if err := fillHeadlines(ctx, db, list, out); err != nil {
		return nil, err
	}
	if err := fillLastActivity(ctx, db, list, out); err != nil {
		return nil, err
	}
	return out, fillOpenRuns(ctx, db, list, out)
}

func closeRows(rows *sql.Rows, what string) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("readmodel: read %s: %w", what, err)
	}
	return nil
}

func fillHeadlines(ctx context.Context, db claimstore.DB, list string, out map[string]Account) error {
	rows, err := db.QueryContext(ctx, `SELECT account_id::text, state FROM account_state WHERE account_id = ANY(string_to_array($1, ',')::uuid[])`, list)
	if err != nil {
		return fmt.Errorf("readmodel: read account states: %w", err)
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return fmt.Errorf("readmodel: scan account state: %w", err)
		}
		headline, err := headlineOf(id, raw)
		if err != nil {
			_ = rows.Close()
			return err
		}
		a := out[id]
		a.Stage, a.Health, a.Motion, a.LastMeaningfulChange = headline["stage"], headline["health"], headline["motion"], headline["last_meaningful_change"]
		out[id] = a
	}
	return closeRows(rows, "account states")
}

func fillLastActivity(ctx context.Context, db claimstore.DB, list string, out map[string]Account) error {
	rows, err := db.QueryContext(ctx, `SELECT account_id::text, max(occurred_at) FROM activities
 WHERE account_id = ANY(string_to_array($1, ',')::uuid[]) GROUP BY account_id`, list)
	if err != nil {
		return fmt.Errorf("readmodel: read last activities: %w", err)
	}
	for rows.Next() {
		var id string
		var at *time.Time
		if err := rows.Scan(&id, &at); err != nil {
			_ = rows.Close()
			return fmt.Errorf("readmodel: scan last activity: %w", err)
		}
		a := out[id]
		a.LastActivityAt = at
		out[id] = a
	}
	return closeRows(rows, "last activities")
}

func fillOpenRuns(ctx context.Context, db claimstore.DB, list string, out map[string]Account) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT ON (account_id) account_id::text, id::text FROM agent_runs
 WHERE account_id = ANY(string_to_array($1, ',')::uuid[]) AND status IN (`+openRunStatuses+`)
 ORDER BY account_id, created_at DESC`, list)
	if err != nil {
		return fmt.Errorf("readmodel: read open runs: %w", err)
	}
	for rows.Next() {
		var id, run string
		if err := rows.Scan(&id, &run); err != nil {
			_ = rows.Close()
			return fmt.Errorf("readmodel: scan open run: %w", err)
		}
		a := out[id]
		a.OpenRunID = &run
		out[id] = a
	}
	return closeRows(rows, "open runs")
}

// headlineOf reads stage/health/motion/last_meaningful_change values out of an account_state document; "unknown"
// reads as null.
func headlineOf(accountID string, raw []byte) (map[string]*string, error) {
	out := map[string]*string{}
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
