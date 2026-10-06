package demorun

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for the Postgres health check
)

// Tooling holds the resolved executables the plan launches.
type Tooling struct {
	Host     string // copy of ghostctl that hosts Postgres and Neo4j (`demo-host`)
	Core     string
	Slackbot string
	Python   string // interpreter with the worker's dependencies
	Npm      string // npm (npm.cmd on Windows)
}

// Service names, in start order.
const (
	SvcNeo4j    = "neo4j"
	SvcPostgres = "postgres"
	SvcWorker   = "worker"
	SvcCore     = "core"
	SvcSlackbot = "slackbot"
	SvcWeb      = "web"
)

const slackConnectedMarker = "slack socket connected"

// Specs returns the services in start order: stores first, then the worker, core, and the surfaces that talk
// to core. Disabled surfaces are omitted.
func (c Config) Specs(t Tooling) []Spec {
	l, p := c.Layout, c.Ports
	hostEnv := func(dir string, port int, svc string) Env {
		return Env{"GHOST_DEMO_HOST_DIR": dir, "GHOST_DEMO_HOST_PORT": strconv.Itoa(port),
			"GHOST_DEMO_HOST_STOPFILE": filepath.Join(l.PIDDir(), svc+".stop")}
	}
	pgReady := filepath.Join(l.PIDDir(), SvcPostgres+".ready")
	pgEnv := hostEnv(l.PGDir(), p.Postgres, SvcPostgres)
	pgEnv["GHOST_DEMO_HOST_READYFILE"] = pgReady
	neo4j := func(slot GraphSlot) Spec {
		env := hostEnv(slot.Dir, slot.Port, slot.Service)
		env["NEO4J_PASSWORD"] = c.Secrets["NEO4J_PASSWORD"]
		for _, k := range []string{"GHOST_NEO4J_CACHE", "JAVA_HOME"} {
			if v := c.merged()[k]; v != "" {
				env[k] = v
			}
		}
		return Spec{Name: slot.Service, Dir: l.Root, Path: t.Host, Args: []string{"demo-host", SvcNeo4j}, Env: env, Port: slot.Port,
			Health: TCPCheck(fmt.Sprintf("127.0.0.1:%d", slot.Port)), URL: fmt.Sprintf("bolt://127.0.0.1:%d", slot.Port),
			Wait: WaitOptions{Timeout: 6 * time.Minute, Interval: time.Second}, Graceful: 20 * time.Second}
	}
	var specs []Spec
	for i := 0; i < c.graphCount(); i++ {
		specs = append(specs, neo4j(c.GraphSlot(i)))
	}
	specs = append(specs, []Spec{
		{Name: SvcPostgres, Dir: l.Root, Path: t.Host, Args: []string{"demo-host", SvcPostgres}, Env: pgEnv, Port: p.Postgres,
			Health: PostgresReady(c.DSN(), pgReady), URL: fmt.Sprintf("postgres://127.0.0.1:%d", p.Postgres),
			Wait: WaitOptions{Timeout: 5 * time.Minute, Interval: time.Second}, Graceful: 60 * time.Second,
			Cleanup: func() error { return StopLeftoverPostgres(l.PGDir()) }},
		c.workerSpec(t),
		{Name: SvcCore, Dir: l.Root, Path: t.Core, Env: c.CoreEnv(), Port: p.Core,
			Health: HTTPCheck(c.CoreURL()+"/healthz", nil), URL: c.CoreURL(),
			Wait: WaitOptions{Timeout: 3 * time.Minute, Interval: time.Second}},
	}...)
	if !c.NoSlack {
		specs = append(specs, Spec{Name: SvcSlackbot, Dir: l.Root, Path: t.Slackbot, Env: c.SlackEnv(),
			Health: LogContainsSinceStart(l.LogFile(SvcSlackbot), slackConnectedMarker), URL: "Slack Socket Mode (#gtm-ai-demo)",
			Wait: WaitOptions{Timeout: 90 * time.Second, Interval: time.Second}})
	}
	if !c.NoWeb {
		webCmd := "dev"
		if c.WebProd {
			webCmd = "start"
		}
		specs = append(specs, Spec{Name: SvcWeb, Dir: filepath.Join(l.Root, "web"), Path: t.Npm,
			Args: []string{"run", webCmd, "--", "-H", "127.0.0.1", "-p", strconv.Itoa(p.Web)}, Env: c.WebEnv(), Port: p.Web,
			Health: HTTPAliveCheck(c.WebURL() + "/"), URL: c.WebURL(),
			Wait: WaitOptions{Timeout: 4 * time.Minute, Interval: 2 * time.Second, CheckTimeout: 90 * time.Second}})
	}
	return specs
}

// workerSpec is the Python worker: the real app under uvicorn (live, record, or replay against worker-py/cassettes),
// or, in replay mode with a cassette directory, the masked replay worker that serves recorded CRMArena extraction
// cassettes offline (no key, no network; a missing cassette is an error, never a live call).
func (c Config) workerSpec(t Tooling) Spec {
	l, p := c.Layout, c.Ports
	spec := Spec{Name: SvcWorker, Dir: filepath.Join(l.Root, "worker-py"), Path: t.Python,
		Args: []string{"-m", "uvicorn", "ghost_worker.app:create_app", "--factory", "--host", "127.0.0.1", "--port", strconv.Itoa(p.Worker)},
		Env:  c.WorkerEnv(), Port: p.Worker, Health: HTTPCheck(fmt.Sprintf("http://127.0.0.1:%d/healthz", p.Worker), nil),
		URL:  fmt.Sprintf("http://127.0.0.1:%d (GHOST_LLM_MODE=%s)", p.Worker, c.LLMMode),
		Wait: WaitOptions{Timeout: 4 * time.Minute, Interval: time.Second}} // the masked replay worker indexes thousands of cassettes at start
	if c.LLMMode == "replay" && c.ReplayCassettes != "" {
		spec.Dir = l.Root
		spec.Args = []string{filepath.Join(l.Root, "bench", "data", "crmarena_replay_worker.py"), "--cassettes", c.ReplayCassettes, "--port", strconv.Itoa(p.Worker)}
		if c.KnownMisses != "" {
			spec.Args = append(spec.Args, "--known-misses", c.KnownMisses)
		}
		spec.URL = fmt.Sprintf("http://127.0.0.1:%d (replay of extraction cassettes: no model is called)", p.Worker)
	}
	return spec
}

// CheckPortFree fails with a *ServiceError when something already listens on the service's port and the service
// is not the one running (the caller only asks for services it is about to start).
func CheckPortFree(spec Spec) error {
	if spec.Port == 0 {
		return nil
	}
	addr := fmt.Sprintf("127.0.0.1:%d", spec.Port)
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = c.Close()
	return &ServiceError{Service: spec.Name, Phase: "start", Err: fmt.Errorf("port %d is already in use by another process; stop it or set GHOST_DEMO_PORT_%s", spec.Port, strings.ToUpper(spec.Name))}
}

// LogContainsSinceStart passes once the log holds marker after its last "--- demo start" header, so a marker
// from an earlier run cannot make a restarted service look healthy.
func LogContainsSinceStart(path, marker string) Check {
	return func(context.Context) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("log not readable yet: %w", err)
		}
		text := string(b)
		if i := strings.LastIndex(text, "--- demo start"); i >= 0 {
			text = text[i:]
		}
		if !strings.Contains(text, marker) {
			return fmt.Errorf("log does not show %q yet", marker)
		}
		return nil
	}
}

// PostgresCheck passes when the demo database answers SELECT 1 (a real query, not just an open port).
func PostgresCheck(dsn string) Check {
	return func(ctx context.Context) error {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return err
		}
		defer db.Close()
		var one int
		if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			return fmt.Errorf("postgres is not answering yet: %v", err)
		}
		return nil
	}
}

// PostgresReady passes when the host has written its ready marker (it does so after the migrations ran) and the
// database answers, so "healthy" means "migrated", not merely "accepting connections".
func PostgresReady(dsn, readyFile string) Check {
	ping := PostgresCheck(dsn)
	return func(ctx context.Context) error {
		if _, err := os.Stat(readyFile); err != nil {
			return fmt.Errorf("postgres is up but its host has not finished migrating yet")
		}
		return ping(ctx)
	}
}

// WaitForStop blocks until the stop file exists or ctx ends (a clean stop either way).
func WaitForStop(ctx context.Context, stopFile string, poll time.Duration) error {
	for {
		if _, err := os.Stat(stopFile); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}

// StopLeftoverPostgres stops a Postgres server whose host process was killed hard (pg_ctl daemonizes the
// server, so it can outlive the host). It does nothing when no postmaster.pid exists.
func StopLeftoverPostgres(pgDir string) error {
	data := filepath.Join(pgDir, "data")
	if _, err := os.Stat(filepath.Join(data, "postmaster.pid")); err != nil {
		return nil
	}
	bin := filepath.Join(pgDir, "bin", "bin", "pg_ctl")
	out, err := exec.Command(bin, "stop", "-m", "fast", "-w", "-D", data).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_ctl stop of a leftover server failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
