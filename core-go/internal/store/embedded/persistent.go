package embedded

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// PersistentOptions describe a long-running embedded Postgres whose data survives a restart (the live demo,
// HAR-137). Unlike Launch, nothing here is throwaway: Dir/data is kept when the server stops.
type PersistentOptions struct {
	Dir  string // root: Dir/data (kept), Dir/runtime (rebuilt each start), Dir/bin (extracted binaries)
	Port int    // fixed, so a restarted demo keeps its DSN
	// Database, User and Password default to ghost_demo, ghost and ghost: a loopback-only demo database.
	Database, User, Password string
}

func (o PersistentOptions) validate() error {
	if o.Dir == "" {
		return fmt.Errorf("embedded: persistent Postgres needs a directory")
	}
	if o.Port < 1 || o.Port > 65535 {
		return fmt.Errorf("embedded: persistent Postgres port %d is not a TCP port", o.Port)
	}
	return nil
}

func (o PersistentOptions) names() (db, user, pw string) {
	db, user, pw = o.Database, o.User, o.Password
	if db == "" {
		db = "ghost_demo"
	}
	if user == "" {
		user = "ghost"
	}
	if pw == "" {
		pw = "ghost"
	}
	return db, user, pw
}

// DSN is the connection string of the server these options start.
func (o PersistentOptions) DSN() string {
	db, user, pw := o.names()
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, pw, o.Port, db)
}

// LaunchPersistent starts the embedded Postgres on a fixed port over Dir/data, initializing the cluster only
// when the directory holds none (the library reuses a valid PG_VERSION cluster). It returns the DSN and a stop
// function that shuts Postgres down cleanly and keeps the data. The downloaded archive stays in the shared
// cache that Launch uses.
func LaunchPersistent(o PersistentOptions) (string, func() error, error) {
	if err := o.validate(); err != nil {
		return "", nil, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", nil, fmt.Errorf("embedded: cache dir: %w", err)
	}
	for _, d := range []string{o.Dir, filepath.Join(o.Dir, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", nil, fmt.Errorf("embedded: create %s: %w", d, err)
		}
	}
	db, user, pw := o.names()
	var logs bytes.Buffer
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V17).
		Port(uint32(o.Port)).
		Database(db).Username(user).Password(pw).
		RuntimePath(filepath.Join(o.Dir, "runtime")).
		DataPath(filepath.Join(o.Dir, "data")).
		BinariesPath(filepath.Join(o.Dir, "bin")).
		CachePath(filepath.Join(cache, "ghost-embedded-pg", "cache")).
		StartTimeout(120 * time.Second).
		Logger(&logs))
	if err := pg.Start(); err != nil {
		_ = pg.Stop()
		return "", nil, fmt.Errorf("embedded: start persistent postgres: %w\n%s", err, logs.String())
	}
	stop := func() error {
		err := pg.Stop()
		_ = os.RemoveAll(filepath.Join(o.Dir, "runtime"))
		return err
	}
	return o.DSN(), stop, nil
}

// WipePersistent deletes the data directory (the `demo reset` of the database). Dir must be set so an empty
// option can never delete the working directory; the server must be stopped first.
func WipePersistent(o PersistentOptions) error {
	if o.Dir == "" {
		return fmt.Errorf("embedded: refusing to wipe an empty directory")
	}
	for _, d := range []string{"data", "runtime"} {
		if err := os.RemoveAll(filepath.Join(o.Dir, d)); err != nil {
			return fmt.Errorf("embedded: wipe %s: %w", d, err)
		}
	}
	return nil
}
