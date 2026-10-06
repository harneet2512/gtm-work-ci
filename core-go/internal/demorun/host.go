package demorun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph/neo4jtest"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

const hostPoll = 250 * time.Millisecond

// HostConfig is what a hosted store reads from its environment (set by the plan, never on the command line).
type HostConfig struct {
	Dir       string
	Port      int
	StopFile  string
	ReadyFile string // written once the store is up (and, for Postgres, migrated); optional
	Password  string // Neo4j only
}

// ParseHostConfig reads GHOST_DEMO_HOST_DIR, _PORT, _STOPFILE and NEO4J_PASSWORD. Errors name variables only.
func ParseHostConfig(getenv func(string) string) (HostConfig, error) {
	cfg := HostConfig{Dir: getenv("GHOST_DEMO_HOST_DIR"), StopFile: getenv("GHOST_DEMO_HOST_STOPFILE"),
		ReadyFile: getenv("GHOST_DEMO_HOST_READYFILE"), Password: getenv("NEO4J_PASSWORD")}
	if cfg.Dir == "" || cfg.StopFile == "" {
		return HostConfig{}, errors.New("demorun: GHOST_DEMO_HOST_DIR and GHOST_DEMO_HOST_STOPFILE are required")
	}
	port, err := strconv.Atoi(getenv("GHOST_DEMO_HOST_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return HostConfig{}, errors.New("demorun: GHOST_DEMO_HOST_PORT is missing or not a TCP port")
	}
	cfg.Port = port
	return cfg, nil
}

// RunHost runs one store (postgres or neo4j) in this process until ctx ends or the stop file appears, then stops
// it cleanly. It is the body of `ghostctl demo-host <kind>`; the supervisor starts it detached. Postgres is
// migrated once it is up, so the schema exists before core or `demo seed` touch it.
func RunHost(ctx context.Context, kind string, getenv func(string) string, log io.Writer) error {
	if kind != SvcPostgres && kind != SvcNeo4j {
		return fmt.Errorf("demorun: unknown host kind %q (want postgres or neo4j)", kind)
	}
	cfg, err := ParseHostConfig(getenv)
	if err != nil {
		return err
	}
	_ = os.Remove(cfg.StopFile)
	if cfg.ReadyFile != "" {
		_ = os.Remove(cfg.ReadyFile) // a marker from an earlier run must not make this one look ready
		defer os.Remove(cfg.ReadyFile)
	}
	say := func(format string, args ...any) {
		if log != nil {
			fmt.Fprintf(log, format+"\n", args...)
		}
	}
	var h hostHandle
	switch kind {
	case SvcPostgres:
		h, err = startPostgresHost(ctx, cfg, say)
	default:
		h, err = startNeo4jHost(ctx, cfg, say)
	}
	if err != nil {
		return err
	}
	if cfg.ReadyFile != "" {
		if err := os.WriteFile(cfg.ReadyFile, []byte("ready\n"), 0o600); err != nil {
			_ = h.stop()
			return fmt.Errorf("demorun: write the ready marker: %w", err)
		}
	}
	say("%s ready; waiting for %s or a signal", kind, cfg.StopFile)
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() { _ = WaitForStop(waitCtx, cfg.StopFile, hostPoll); close(stopped) }()
	select {
	case <-stopped:
	case err := <-h.died:
		// The server went away under the host (killed, crashed): exit so the supervisor sees a dead service at once
		// instead of a live host in front of nothing.
		_ = h.stop()
		return fmt.Errorf("demorun: %s died while the demo was running: %v", kind, err)
	}
	say("%s stopping", kind)
	if err := h.stop(); err != nil {
		return fmt.Errorf("demorun: stop %s: %w", kind, err)
	}
	say("%s stopped", kind)
	return nil
}

// hostHandle is a running store: how to stop it and a channel that yields when it dies on its own.
type hostHandle struct {
	stop func() error
	died <-chan error
}

func startPostgresHost(ctx context.Context, cfg HostConfig, say func(string, ...any)) (hostHandle, error) {
	opts := embedded.PersistentOptions{Dir: cfg.Dir, Port: cfg.Port}
	// A previous host killed hard can leave its server running; stop it so the port and data dir are free.
	if err := StopLeftoverPostgres(cfg.Dir); err != nil {
		say("warning: %v", err)
	}
	dsn, stop, err := embedded.LaunchPersistent(opts)
	if err != nil {
		return hostHandle{}, err
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		_ = stop()
		return hostHandle{}, err
	}
	m, err := store.NewMigrator(db)
	if err == nil {
		err = m.Up(ctx)
	}
	if err != nil {
		_ = db.Close()
		_ = stop()
		return hostHandle{}, fmt.Errorf("demorun: migrate the demo database: %w", err)
	}
	say("postgres migrated and listening on 127.0.0.1:%d", cfg.Port)
	died := make(chan error, 1)
	stopWatch := make(chan struct{})
	go watchPostgres(db, postgresWatchEvery, stopWatch, died)
	return hostHandle{stop: func() error { close(stopWatch); _ = db.Close(); return stop() }, died: died}, nil
}

// watchPostgres pings the server and reports when it stops answering (three misses in a row). pg_ctl daemonizes
// Postgres, so the host has no process handle to wait on.
func watchPostgres(db *sql.DB, every time.Duration, stop <-chan struct{}, died chan<- error) {
	misses := 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(every):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			misses = 0
			continue
		}
		if misses++; misses >= 3 {
			died <- err
			return
		}
	}
}

// postgresWatchEvery is how often the host pings its server.
const postgresWatchEvery = 5 * time.Second

func startNeo4jHost(ctx context.Context, cfg HostConfig, say func(string, ...any)) (hostHandle, error) {
	if os.Getenv("JAVA_HOME") == "" {
		if home, ok := neo4jtest.FindJavaHome(ctx); ok {
			_ = os.Setenv("JAVA_HOME", home)
		}
	}
	env, err := neo4jtest.StartPersistent(ctx, neo4jtest.PersistentOptions{Dir: cfg.Dir, BoltPort: cfg.Port, Password: cfg.Password})
	if err != nil {
		return hostHandle{}, err
	}
	say("neo4j listening on 127.0.0.1:%d", cfg.Port)
	died := make(chan error, 1)
	go func() {
		if err, ok := <-env.Done(); ok {
			died <- err
		}
	}()
	return hostHandle{stop: env.Close, died: died}, nil
}
