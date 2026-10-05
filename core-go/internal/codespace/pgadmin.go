package codespace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for the admin connection
)

// Admin manages databases inside the demo Postgres cluster. Reset is a file-level copy (CREATE DATABASE ... TEMPLATE),
// so restoring a case to its frozen Event N-1 takes seconds and does not replay the history.
type Admin interface {
	Exists(ctx context.Context, name string) (bool, error)
	Create(ctx context.Context, name string) error
	// Drop removes a database, ending any session on it first. A database that does not exist is not an error.
	Drop(ctx context.Context, name string) error
	// Clone creates target as an exact copy of source, ending sessions on source first (a copy needs none).
	Clone(ctx context.Context, target, source string) error
}

// AdminDSN is dsn pointed at the maintenance database `postgres`, the one CREATE and DROP DATABASE run from.
func AdminDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("codespace: the database URL is not a postgres URL")
	}
	u.Path = "/postgres"
	return u.String(), nil
}

// DSNFor is dsn pointed at another database of the same server.
func DSNFor(dsn, database string) (string, error) {
	if !databaseName.MatchString(database) {
		return "", fmt.Errorf("codespace: %q is not a database name", database)
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("codespace: the database URL is not a postgres URL")
	}
	u.Path = "/" + database
	return u.String(), nil
}

// quoteIdent quotes a database name for SQL. Names are validated first, so the quoting is a second line of defence.
func quoteIdent(name string) (string, error) {
	if !databaseName.MatchString(name) {
		return "", fmt.Errorf("codespace: %q is not a valid database name", name)
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`, nil
}

// PGAdmin is the real Admin over pgx. Each call opens its own short-lived connection to `postgres`.
type PGAdmin struct{ DSN string }

func (a PGAdmin) open(ctx context.Context) (*sql.DB, error) {
	dsn, err := AdminDSN(a.DSN)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("codespace: open the admin connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("codespace: the demo Postgres is not answering: %w", err)
	}
	return db, nil
}

// Exists implements Admin.
func (a PGAdmin) Exists(ctx context.Context, name string) (bool, error) {
	if !databaseName.MatchString(name) {
		return false, fmt.Errorf("codespace: %q is not a valid database name", name)
	}
	db, err := a.open(ctx)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var one int
	err = db.QueryRowContext(ctx, `SELECT 1 FROM pg_database WHERE datname = $1`, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("codespace: look up database %s: %w", name, err)
	}
	return true, nil
}

// Create implements Admin.
func (a PGAdmin) Create(ctx context.Context, name string) error {
	return a.run(ctx, "create", name, func(q string) string { return "CREATE DATABASE " + q })
}

// Drop implements Admin.
func (a PGAdmin) Drop(ctx context.Context, name string) error {
	if err := a.terminate(ctx, name); err != nil {
		return err
	}
	return a.run(ctx, "drop", name, func(q string) string { return "DROP DATABASE IF EXISTS " + q })
}

// Clone implements Admin.
func (a PGAdmin) Clone(ctx context.Context, target, source string) error {
	src, err := quoteIdent(source)
	if err != nil {
		return err
	}
	if err := a.terminate(ctx, source); err != nil {
		return err
	}
	return a.run(ctx, "clone", target, func(q string) string { return "CREATE DATABASE " + q + " TEMPLATE " + src })
}

func (a PGAdmin) run(ctx context.Context, verb, name string, sqlFor func(quoted string) string) error {
	q, err := quoteIdent(name)
	if err != nil {
		return err
	}
	db, err := a.open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, sqlFor(q)); err != nil {
		return fmt.Errorf("codespace: %s database %s: %w", verb, name, err)
	}
	return nil
}

// terminate ends every other session on a database so it can be dropped or used as a template.
func (a PGAdmin) terminate(ctx context.Context, name string) error {
	if !databaseName.MatchString(name) {
		return fmt.Errorf("codespace: %q is not a valid database name", name)
	}
	db, err := a.open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name); err != nil {
		return fmt.Errorf("codespace: end the sessions on %s: %w", name, err)
	}
	return nil
}

var _ Admin = PGAdmin{}
