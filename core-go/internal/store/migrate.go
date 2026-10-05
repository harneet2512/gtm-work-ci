// Package store owns the Postgres connection and schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migrations are embedded so the core binary is self-contained.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// Connection pool limits: bounded so a request burst cannot exhaust the database's
// connection slots (Neon enforces a low per-endpoint limit).
const (
	MaxOpenConns    = 10
	MaxIdleConns    = 5
	ConnMaxLifetime = 30 * time.Minute
	ConnMaxIdleTime = 5 * time.Minute
)

// Open connects to Postgres using a libpq-style URL, applies the pool limits and verifies
// the connection.
func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("store: database URL is empty")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetConnMaxLifetime(ConnMaxLifetime)
	db.SetConnMaxIdleTime(ConnMaxIdleTime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return db, nil
}

// Migrator applies the embedded plain-SQL goose migrations.
type Migrator struct {
	provider *goose.Provider
}

// NewMigrator builds a migrator. A Postgres advisory lock serializes concurrent runs.
func NewMigrator(db *sql.DB) (*Migrator, error) {
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: migrations fs: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("store: migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("store: goose provider: %w", err)
	}
	return &Migrator{provider: provider}, nil
}

// Up applies all pending migrations.
func (m *Migrator) Up(ctx context.Context) error {
	if _, err := m.provider.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate up: %w", err)
	}
	return nil
}

// DownTo rolls back to the given version (0 = empty schema).
func (m *Migrator) DownTo(ctx context.Context, version int64) error {
	if _, err := m.provider.DownTo(ctx, version); err != nil {
		return fmt.Errorf("store: migrate down: %w", err)
	}
	return nil
}

// Version returns the current schema version.
func (m *Migrator) Version(ctx context.Context) (int64, error) {
	v, err := m.provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: version: %w", err)
	}
	return v, nil
}
