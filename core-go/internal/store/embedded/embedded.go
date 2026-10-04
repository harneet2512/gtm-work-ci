// Package embedded starts a private, throwaway embedded Postgres. It is production code (ghostctl's
// demo-case commands replay whole datasets and must never touch a shared database); the test helper
// storetest builds on it, not the other way round, so no binary imports a package that imports "testing".
package embedded

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

// Env is a started, migrated private database.
type Env struct {
	DB       *sql.DB
	Migrator *store.Migrator
	URL      string
	stop     func() error
}

// Close releases the connection and stops the embedded Postgres.
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

// Start always starts a private embedded Postgres, whatever TEST_DATABASE_URL and DATABASE_URL say, and
// migrates it to the latest version. Close stops it.
func Start(ctx context.Context) (*Env, error) {
	dbURL, stop, err := Launch()
	if err != nil {
		return nil, err
	}
	env := &Env{URL: dbURL, stop: stop}
	if env.DB, err = store.Open(ctx, dbURL); err != nil {
		_ = env.Close()
		return nil, err
	}
	if env.Migrator, err = store.NewMigrator(env.DB); err != nil {
		_ = env.Close()
		return nil, err
	}
	if err := env.Migrator.Up(ctx); err != nil {
		_ = env.Close()
		return nil, err
	}
	return env, nil
}

// Launch starts the embedded Postgres only (no migration) and returns its DSN and a stop function.
func Launch() (string, func() error, error) {
	port, err := freePort()
	if err != nil {
		return "", nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", nil, fmt.Errorf("embedded: cache dir: %w", err)
	}
	root := filepath.Join(os.TempDir(), fmt.Sprintf("ghost-embedded-pg-%d", port))
	var logs bytes.Buffer
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).
		Port(uint32(port)).
		Database("ghost_test").
		Username("ghost").
		Password("ghost").
		RuntimePath(root).
		// Per-run binaries dir: concurrent test processes (parallel agents, -p>1) must not
		// extract into a shared folder whose DLLs another process has loaded. The downloaded
		// archive stays cached and shared.
		BinariesPath(filepath.Join(root, "bin")).
		CachePath(filepath.Join(cache, "ghost-embedded-pg", "cache")).
		StartTimeout(90 * time.Second).
		Logger(&logs))
	if err := pg.Start(); err != nil {
		_ = pg.Stop()
		_ = os.RemoveAll(root)
		return "", nil, fmt.Errorf("embedded: start embedded postgres: %w\n%s", err, logs.String())
	}
	dsn := fmt.Sprintf("postgres://ghost:ghost@127.0.0.1:%d/ghost_test?sslmode=disable", port)
	stop := func() error {
		err := pg.Stop()
		_ = os.RemoveAll(root)
		return err
	}
	return dsn, stop, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("embedded: free port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
