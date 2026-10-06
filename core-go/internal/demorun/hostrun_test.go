package demorun

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRunHostPostgresMigratesServesAndStopsOnTheStopFile runs the real Postgres host: it must come up migrated and
// persistent, answer queries, and exit cleanly (data kept) when the stop file appears, exactly as the supervisor
// asks it to on `demo down`.
func TestRunHostPostgresMigratesServesAndStopsOnTheStopFile(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Postgres")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dir := t.TempDir()
	stopFile := filepath.Join(dir, "postgres.stop")
	env := map[string]string{"GHOST_DEMO_HOST_DIR": filepath.Join(dir, "pg"), "GHOST_DEMO_HOST_PORT": strconv.Itoa(port), "GHOST_DEMO_HOST_STOPFILE": stopFile,
		"GHOST_DEMO_HOST_READYFILE": filepath.Join(dir, "postgres.ready")}
	var log bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- RunHost(context.Background(), SvcPostgres, func(k string) string { return env[k] }, &log)
	}()

	cfg := Config{Layout: NewLayout(dir), Ports: Ports{Postgres: port}}
	cfg.Layout.StateDir = dir
	dsn := cfg.DSN()
	if err := WaitHealthy(context.Background(), "postgres", PostgresReady(dsn, filepath.Join(dir, "postgres.ready")), WaitOptions{Timeout: 3 * time.Minute, Interval: 500 * time.Millisecond}); err != nil {
		t.Fatalf("the host never became healthy: %v\n%s", err, log.String())
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM demo_manifests`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the host must have migrated the schema (demo_manifests): n=%d err=%v", n, err)
	}
	_ = db.Close()

	if err := os.WriteFile(stopFile, []byte("stop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunHost: %v\n%s", err, log.String())
		}
	case <-time.After(2 * time.Minute):
		t.Fatalf("the host did not stop on the stop file\n%s", log.String())
	}
	if !strings.Contains(log.String(), "postgres stopped") {
		t.Fatalf("log should record the stop:\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "postgres.ready")); !os.IsNotExist(err) {
		t.Fatal("the ready marker must be removed when the host stops")
	}
	if _, err := os.Stat(filepath.Join(dir, "pg", "data", "PG_VERSION")); err != nil {
		t.Fatalf("the data directory must survive a stop: %v", err)
	}
}

// stubNpm puts an `npm` stub that exits with code on PATH and returns its directory.
func stubNpm(t *testing.T, code int) {
	t.Helper()
	dir := t.TempDir()
	name := npmNames()[0]
	var body string
	if runtime.GOOS == "windows" {
		body = "@echo off\r\nexit /b " + strconv.Itoa(code) + "\r\n"
	} else {
		body = "#!/bin/sh\nexit " + strconv.Itoa(code) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPrepareWebFindsNpmAndInstallsOnlyWhenNodeModulesIsMissing(t *testing.T) {
	root := t.TempDir()
	l := NewLayout(root)
	if err := os.MkdirAll(filepath.Join(root, "web", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	stubNpm(t, 1) // would fail if it were run
	var said []string
	npm, err := prepareWeb(context.Background(), l, func(f string, a ...any) { said = append(said, f) })
	if err != nil || npm == "" || len(said) != 0 {
		t.Fatalf("with node_modules present npm must not run: %q %v %v", npm, err, said)
	}
	// Without node_modules `npm ci` runs: a failing install is a ServiceError naming web.
	if err := os.RemoveAll(filepath.Join(root, "web", "node_modules")); err != nil {
		t.Fatal(err)
	}
	_, err = prepareWeb(context.Background(), l, func(string, ...any) {})
	var se *ServiceError
	if err == nil || !errors.As(err, &se) || se.Service != "web" {
		t.Fatalf("a failed npm ci must be a ServiceError for web, got %v", err)
	}
	stubNpm(t, 0)
	if _, err := prepareWeb(context.Background(), l, func(string, ...any) {}); err != nil {
		t.Fatalf("a working npm ci: %v", err)
	}
}

func TestPrepareWebWithoutNpmNamesTheFix(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := prepareWeb(context.Background(), NewLayout(t.TempDir()), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "npm") || !strings.Contains(err.Error(), "--no-web") {
		t.Fatalf("want an npm error that offers --no-web, got %v", err)
	}
}

func TestInstallHostCopiesTheRunningBinaryAndLeavesALockedOneAlone(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := os.MkdirAll(l.BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	sup := Supervisor{Layout: l, PIDs: PIDStore{Dir: l.PIDDir(), Alive: func(int) bool { return false }}}
	dst, err := installHost(l, sup)
	if err != nil {
		t.Fatalf("installHost: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Size() == 0 {
		t.Fatalf("host binary missing: %v", err)
	}
	// With a running host and an existing copy, the copy is reused untouched (Windows locks a running exe).
	if err := os.WriteFile(dst, []byte("locked-marker"), 0o755); err != nil {
		t.Fatal(err)
	}
	sup.PIDs = PIDStore{Dir: l.PIDDir(), Alive: func(int) bool { return true }}
	if err := sup.PIDs.Write(Record{Service: SvcPostgres, PID: 4242}); err != nil {
		t.Fatal(err)
	}
	if again, err := installHost(l, sup); err != nil || again != dst {
		t.Fatalf("installHost = %q %v", again, err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "locked-marker" {
		t.Fatal("a copy that a running host holds must not be overwritten")
	}
}
