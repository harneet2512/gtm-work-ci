package demorun

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestParseHostConfigReadsTheEnvironment(t *testing.T) {
	env := map[string]string{"GHOST_DEMO_HOST_DIR": "/d", "GHOST_DEMO_HOST_PORT": "15432", "GHOST_DEMO_HOST_STOPFILE": "/s", "NEO4J_PASSWORD": "pw-long-enough"}
	cfg, err := ParseHostConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dir != "/d" || cfg.Port != 15432 || cfg.StopFile != "/s" || cfg.Password != "pw-long-enough" {
		t.Fatalf("config wrong: dir=%q port=%d stop=%q", cfg.Dir, cfg.Port, cfg.StopFile)
	}
}

func TestParseHostConfigRejectsMissingOrBadValuesWithoutEchoingThem(t *testing.T) {
	good := map[string]string{"GHOST_DEMO_HOST_DIR": "/d", "GHOST_DEMO_HOST_PORT": "15432", "GHOST_DEMO_HOST_STOPFILE": "/s"}
	for name, mutate := range map[string]func(map[string]string){
		"no dir":   func(m map[string]string) { delete(m, "GHOST_DEMO_HOST_DIR") },
		"no port":  func(m map[string]string) { delete(m, "GHOST_DEMO_HOST_PORT") },
		"bad port": func(m map[string]string) { m["GHOST_DEMO_HOST_PORT"] = "sekret-not-a-port" },
		"no stop":  func(m map[string]string) { delete(m, "GHOST_DEMO_HOST_STOPFILE") },
	} {
		m := map[string]string{}
		for k, v := range good {
			m[k] = v
		}
		mutate(m)
		_, err := ParseHostConfig(func(k string) string { return m[k] })
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		if strings.Contains(err.Error(), "sekret-not-a-port") {
			t.Errorf("%s: the error echoed a value: %v", name, err)
		}
	}
}

func TestRunHostRejectsUnknownKinds(t *testing.T) {
	err := RunHost(t.Context(), "redis", func(string) string { return "" }, nil)
	if err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("want an unknown-kind error, got %v", err)
	}
}

func TestWatchPostgresReportsAServerThatStopsAnswering(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://nobody:nothing@127.0.0.1:1/never?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	died := make(chan error, 1)
	go watchPostgres(db, 10*time.Millisecond, make(chan struct{}), died)
	select {
	case err := <-died:
		if err == nil {
			t.Fatal("a death must carry the ping error")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("an unreachable server must be reported after three misses")
	}
}

func TestWatchPostgresStopsWhenAskedAndNeverReports(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://nobody:nothing@127.0.0.1:1/never?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	died, stop := make(chan error, 1), make(chan struct{})
	close(stop)
	go watchPostgres(db, 10*time.Millisecond, stop, died)
	select {
	case err := <-died:
		t.Fatalf("a stopped watcher must not report: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
