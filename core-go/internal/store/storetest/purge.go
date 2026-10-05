package storetest

import (
	"context"
	"database/sql"
	"fmt"
)

// Purge runs a test-reset statement (typically TRUNCATE ... CASCADE) as an explicit purge:
// migration 0021 refuses to delete or truncate state transitions and their history unless
// ghost.purge_transitions is 'on' (ADR-0005, ADR-0012), and a TRUNCATE of accounts cascades to
// them. The setting is transaction-local, so it never leaks into a pooled connection.
func Purge(ctx context.Context, db *sql.DB, stmt string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storetest: begin purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL ghost.purge_transitions = 'on'`); err != nil {
		return fmt.Errorf("storetest: enable purge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("storetest: purge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storetest: commit purge: %w", err)
	}
	return nil
}
