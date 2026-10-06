// Package readmodel serves the read side of the core REST API (contracts/openapi/core.yaml): an
// account's state, diffs, signals and timeline, and a run's lineage. It only reads. The tables it
// reads that are written by later work packages (state_diffs, signals, agent_runs: HAR-106) may be
// empty, and an empty table is an empty list, never an error.
package readmodel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// Limits of the list endpoints (core.yaml components.parameters.Limit).
const (
	DefaultLimit = 50
	MaxLimit     = 200
	// maxCorrelated bounds the correlated activities of one trace.
	maxCorrelated = 100
	// maxTieGroup bounds a page made of activities that all share one occurred_at.
	maxTieGroup = 1000
	maxSummary  = 1000
)

var (
	// ErrNotFound means the account or run does not exist.
	ErrNotFound = errors.New("readmodel: not found")
	// ErrInvalid means a caller-supplied argument is out of range.
	ErrInvalid = errors.New("readmodel: invalid argument")
	// ErrNoState means the account exists but has no state (computed yet, or as of that time).
	ErrNoState = errors.New("readmodel: no state computed")
	// ErrNoStateBefore means the account has no state version strictly before the requested world time.
	ErrNoStateBefore = errors.New("readmodel: no state computed before that world time")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidUUID reports whether s is a lowercase canonical uuid, the only id form the API accepts.
func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

// Reader reads the HAR-96 store.
type Reader struct {
	db *sql.DB
}

// New returns a Reader on db.
func New(db *sql.DB) (*Reader, error) {
	if db == nil {
		return nil, errors.New("readmodel: database is required")
	}
	return &Reader{db: db}, nil
}

// snapshot runs fn in one read-only repeatable-read transaction, so a trace is one consistent view.
func (r *Reader) snapshot(ctx context.Context, fn func(claimstore.DB) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("readmodel: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

func accountExists(ctx context.Context, db claimstore.DB, accountID string) error {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = $1::uuid`, accountID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("readmodel: check account: %w", err)
	}
	return nil
}

func requireUUID(kind, id string) error {
	if !ValidUUID(id) {
		return fmt.Errorf("readmodel: %s id is not a uuid: %w", kind, ErrNotFound)
	}
	return nil
}

// normalizeLimit applies the default and rejects values outside 1..MaxLimit.
func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultLimit, nil
	}
	if limit < 1 || limit > MaxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d: %w", MaxLimit, ErrInvalid)
	}
	return limit, nil
}

// utc converts a driver timestamp to UTC so responses do not depend on the session zone.
func utc(t time.Time) time.Time { return t.UTC() }

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// uuidList renders ids for `string_to_array($n, ',')::uuid[]`; every id must be a uuid.
func uuidList(ids []string) (string, error) {
	for _, id := range ids {
		if !ValidUUID(id) {
			return "", errors.New("readmodel: id list holds a non-uuid")
		}
	}
	return strings.Join(ids, ","), nil
}
