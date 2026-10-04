// Package graph is the identity-resolution and relationship layer (WP5): it maps source
// identities onto accounts, people and opportunities (entity_source_mappings), writes the typed
// edges between them (relationships) and plugs into ingest as an ingest.Extension.
package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// DBTX is the subset of *sql.DB and *sql.Tx the package needs, so every function works
// inside an ingest transaction as well as on a plain connection.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Txn is a transaction. Everything that takes advisory locks or needs several statements to be
// atomic (mappings, edge upserts, entity creation) takes a Txn rather than a DBTX: on a pooled
// *sql.DB every statement autocommits, so a transaction-scoped lock would be released at once
// and a check-then-insert would race. *sql.DB has no Commit, so passing it does not compile.
type Txn interface {
	DBTX
	Commit() error
	Rollback() error
}

// closedAtSQL is the valid_to a close writes for the timestamp parameter $n: the given time, or
// just after the row's own start if that is not later, so the interval check always holds.
func closedAtSQL(n int) string {
	return fmt.Sprintf("GREATEST($%d::timestamptz, valid_from + interval '1 microsecond')", n)
}

// lock serializes concurrent writers of the same thing for the rest of the transaction (a
// no-op beyond the statement outside one).
func lock(ctx context.Context, q Txn, scope, key string) error {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scope+"|"+key); err != nil {
		return fmt.Errorf("graph: lock %s: %w", scope, err)
	}
	return nil
}

// optional maps "" to SQL NULL.
func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optionalJSON maps empty evidence to SQL NULL.
func optionalJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// decodeData parses the small jsonb object a node query returns.
func decodeData(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("graph: decode node data: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	return m, nil
}

// rowsAffected wraps RowsAffected with context.
func rowsAffected(res sql.Result, what string) (int, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("graph: %s: rows affected: %w", what, err)
	}
	return int(n), nil
}
