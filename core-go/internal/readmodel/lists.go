package readmodel

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// AccountPage is GET /accounts: one page of AccountSummary items and the cursor of the next page.
type AccountPage struct {
	Items      []Account `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

// RunPage is GET /runs: one page of AgentRun items, newest first, and the cursor of the next page.
type RunPage struct {
	Items      []Run   `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// RunFilter selects the runs of GET /runs. Zero values mean "no filter" / the default limit / the first page.
type RunFilter struct {
	AccountID string
	Status    string
	Limit     int
	Cursor    string
}

// runStatuses is agent_run.v1.json status; an unknown filter value is a 400, never an empty list.
var runStatuses = map[string]bool{
	"pending": true, "context_built": true, "drafted": true, "awaiting_human": true, "approved": true, "edited": true,
	"rejected": true, "ignored": true, "executed": true, "recorded": true, "failed": true, "cancelled": true,
}

const (
	accountCursorKind = "accounts"
	runCursorKind     = "runs"
	cursorSep         = "\x00"
)

// EncodeCursor makes an opaque keyset cursor: the list kind and the last row's sort key.
func EncodeCursor(kind string, key ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + cursorSep + strings.Join(key, cursorSep)))
}

// DecodeCursor reverses EncodeCursor for one list kind with n key parts; anything else is ErrInvalid.
func DecodeCursor(kind, cursor string, n int) ([]string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("cursor is not valid: %w", ErrInvalid)
	}
	parts := strings.Split(string(raw), cursorSep)
	if len(parts) != n+1 || parts[0] != kind {
		return nil, fmt.Errorf("cursor does not belong to this list: %w", ErrInvalid)
	}
	return parts[1:], nil
}

// ListAccounts returns one page of accounts ordered by name then id. cursor is the previous page's NextCursor
// ("" for the first page); limit 0 selects DefaultLimit. ErrInvalid: limit out of range or a bad cursor.
func (r *Reader) ListAccounts(ctx context.Context, limit int, cursor string) (AccountPage, error) {
	n, err := normalizeLimit(limit)
	if err != nil {
		return AccountPage{}, err
	}
	var after []string
	if cursor != "" {
		if after, err = DecodeCursor(accountCursorKind, cursor, 2); err != nil {
			return AccountPage{}, err
		}
		if !ValidUUID(after[1]) {
			return AccountPage{}, fmt.Errorf("cursor does not belong to this list: %w", ErrInvalid)
		}
	}
	var page AccountPage
	// One snapshot and a fixed number of statements per page, however many accounts it holds.
	err = r.snapshot(ctx, func(db claimstore.DB) error {
		ids, names, err := accountKeys(ctx, db, n+1, after)
		if err != nil {
			return err
		}
		more := len(ids) > n
		if more {
			ids, names = ids[:n], names[:n]
		}
		accounts, err := loadAccounts(ctx, db, ids)
		if err != nil {
			return err
		}
		page = AccountPage{Items: make([]Account, 0, len(ids))}
		for _, id := range ids {
			a, ok := accounts[id]
			if !ok {
				return ErrNotFound
			}
			page.Items = append(page.Items, a)
		}
		if more {
			next := EncodeCursor(accountCursorKind, names[n-1], ids[n-1])
			page.NextCursor = &next
		}
		return nil
	})
	return page, err
}

// accountKeys reads up to n (id, name) keys strictly after the cursor key (name, id).
func accountKeys(ctx context.Context, db claimstore.DB, n int, after []string) (ids, names []string, err error) {
	query, args := `SELECT id::text, name FROM accounts ORDER BY name, id LIMIT $1`, []any{n}
	if after != nil {
		query = `SELECT id::text, name FROM accounts WHERE (name, id) > ($2, $3::uuid) ORDER BY name, id LIMIT $1`
		args = append(args, after[0], after[1])
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("readmodel: list accounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, fmt.Errorf("readmodel: scan account key: %w", err)
		}
		ids, names = append(ids, id), append(names, name)
	}
	return ids, names, rows.Err()
}

// ListRuns returns one page of agent runs, newest first. ErrInvalid: an unknown status, a malformed account id, a
// limit out of range or a bad cursor.
func (r *Reader) ListRuns(ctx context.Context, f RunFilter) (RunPage, error) {
	n, err := normalizeLimit(f.Limit)
	if err != nil {
		return RunPage{}, err
	}
	if f.Status != "" && !runStatuses[f.Status] {
		return RunPage{}, fmt.Errorf("unknown run status: %w", ErrInvalid)
	}
	if f.AccountID != "" && !ValidUUID(f.AccountID) {
		return RunPage{}, fmt.Errorf("account_id is not a uuid: %w", ErrInvalid)
	}
	var at time.Time
	var afterID string
	if f.Cursor != "" {
		key, err := DecodeCursor(runCursorKind, f.Cursor, 2)
		if err != nil {
			return RunPage{}, err
		}
		if at, err = time.Parse(time.RFC3339Nano, key[0]); err != nil || !ValidUUID(key[1]) {
			return RunPage{}, fmt.Errorf("cursor does not belong to this list: %w", ErrInvalid)
		}
		afterID = key[1]
	}
	var page RunPage
	err = r.snapshot(ctx, func(db claimstore.DB) error {
		var err error
		page, err = listRuns(ctx, db, f, n, at, afterID)
		return err
	})
	return page, err
}

func listRuns(ctx context.Context, db claimstore.DB, f RunFilter, n int, at time.Time, afterID string) (RunPage, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text, created_at FROM agent_runs
 WHERE ($1 = '' OR account_id = $1::uuid) AND ($2 = '' OR status = $2)
   AND ($3 = '' OR (created_at, id) < ($4::timestamptz, $3::uuid))
 ORDER BY created_at DESC, id DESC LIMIT $5`, f.AccountID, f.Status, afterID, at, n+1)
	if err != nil {
		return RunPage{}, fmt.Errorf("readmodel: list runs: %w", err)
	}
	var ids []string
	var created []time.Time
	for rows.Next() {
		var id string
		var c time.Time
		if err := rows.Scan(&id, &c); err != nil {
			_ = rows.Close()
			return RunPage{}, fmt.Errorf("readmodel: scan run key: %w", err)
		}
		ids, created = append(ids, id), append(created, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return RunPage{}, fmt.Errorf("readmodel: list runs: %w", err)
	}
	_ = rows.Close()
	more := len(ids) > n
	if more {
		ids, created = ids[:n], created[:n]
	}
	runs, err := loadRuns(ctx, db, ids)
	if err != nil {
		return RunPage{}, err
	}
	page := RunPage{Items: make([]Run, 0, len(ids))}
	for _, id := range ids {
		run, ok := runs[id]
		if !ok {
			return RunPage{}, ErrNotFound
		}
		page.Items = append(page.Items, run)
	}
	if more {
		next := EncodeCursor(runCursorKind, created[n-1].UTC().Format(time.RFC3339Nano), ids[n-1])
		page.NextCursor = &next
	}
	return page, nil
}

// NormalizeLimit applies the list default (DefaultLimit for 0) and rejects values outside 1..MaxLimit with ErrInvalid,
// for the other list endpoints that page the same way (internal/controlplane).
func NormalizeLimit(limit int) (int, error) { return normalizeLimit(limit) }
