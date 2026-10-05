// Package storetest provides a migrated Postgres for integration tests.
//
// If TEST_DATABASE_URL is set (CI service container, Neon test branch) it is used — but only
// if it clearly names a test database and is not the dev DATABASE_URL, because Start resets
// the schema. Otherwise an embedded Postgres is started on demand and stopped afterwards, so
// local runs need neither Docker nor a resident database server.
package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

// Env is a started database with its migrator.
type Env struct {
	DB       *sql.DB
	Migrator *store.Migrator
	URL      string
	stop     func() error
}

// Close releases the connection and stops embedded Postgres if one was started.
func (e *Env) Close() error {
	var firstErr error
	if e.DB != nil {
		firstErr = e.DB.Close()
	}
	if e.stop != nil {
		if err := e.stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Start returns a database reset to the latest migration. Call from TestMain and always Close.
func Start(ctx context.Context) (env *Env, err error) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	var stop func() error
	if dbURL == "" {
		dbURL, stop, err = embedded.Launch()
		if err != nil {
			return nil, err
		}
	} else if err := checkSafeTestURL(dbURL, os.Getenv("DATABASE_URL")); err != nil {
		return nil, err
	}

	return openEnv(ctx, dbURL, stop)
}

func openEnv(ctx context.Context, dbURL string, stop func() error) (env *Env, err error) {
	env = &Env{URL: dbURL, stop: stop}
	defer func() {
		if err != nil {
			_ = env.Close()
			env = nil
		}
	}()

	if env.DB, err = store.Open(ctx, dbURL); err != nil {
		return env, err
	}
	env.Migrator, err = reset(ctx, env.DB)
	return env, err
}

// reset makes reruns against a shared test database deterministic: drop everything, then migrate up
// with a fresh migrator (goose caches that its version table exists). Walking Down migrations would
// fail on rows an older schema cannot represent.
func reset(ctx context.Context, db *sql.DB) (*store.Migrator, error) {
	if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		return nil, fmt.Errorf("storetest: reset schema: %w", err)
	}
	m, err := store.NewMigrator(db)
	if err != nil {
		return nil, err
	}
	return m, m.Up(ctx)
}

// checkSafeTestURL refuses URLs that could point at real data.
func checkSafeTestURL(testURL, devURL string) error {
	if devURL != "" && testURL == devURL {
		return errors.New("storetest: TEST_DATABASE_URL equals DATABASE_URL; refusing to reset the dev database")
	}
	u, err := url.Parse(testURL)
	if err != nil {
		return fmt.Errorf("storetest: parse TEST_DATABASE_URL: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.Contains(strings.ToLower(name), "test") {
		return fmt.Errorf("storetest: database %q does not contain 'test'; refusing to reset it", name)
	}
	return nil
}

// Tx runs fn inside a transaction that is always rolled back, isolating tests.
func Tx(t *testing.T, db *sql.DB, fn func(tx *sql.Tx)) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	fn(tx)
}

// Main is a TestMain helper: it starts the database, runs the tests and always closes it,
// even when a test panics.
func Main(m *testing.M, set func(*Env)) int {
	env, err := Start(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "start test database: %v\n", err)
		return 1
	}
	defer func() {
		if cerr := env.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "close test database: %v\n", cerr)
		}
	}()
	set(env)
	return m.Run()
}
