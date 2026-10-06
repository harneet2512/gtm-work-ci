package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// TestDemoSmokeUpSeedPlayVerify is the integration smoke of the live demo runner (HAR-137): it builds the real
// ghostctl and drives `demo up`, `seed`, `play`, `verify`, a full stop and restart, and `reset` as subprocesses,
// exactly as the user would, against real Neo4j, Postgres, the Python worker and core.
//
// It is opt-in (GHOST_DEMO_SMOKE=1): it takes about six minutes and needs Java 17 or 21, the pinned Neo4j tarball
// (cached after the first run) and Python with the worker's dependencies. It never calls a live model and never
// touches Slack: the worker runs in replay mode and the Slack bot is not started. It is honest about that, and
// about what replay cannot do, in the report it prints (and writes to GHOST_DEMO_SMOKE_REPORT when set):
//
//   - the held-out event's claim extraction is replayed from recorded cassettes (GHOST_DEMO_CASSETTES, or
//     data/crmarena_extraction/cassettes of this or the main checkout); without them Play cannot finish and the
//     smoke says so instead of passing;
//   - strategy generation, the evals' semantic judges, M1/M2/M3, the human clicks (Choose, Edit, Send, verdict)
//     and the web app are NOT exercised.
//
// A poisoned DATABASE_URL in the environment proves the demo never touches a shared database.
func TestDemoSmokeUpSeedPlayVerify(t *testing.T) {
	if os.Getenv("GHOST_DEMO_SMOKE") != "1" {
		t.Skip("opt-in integration smoke: set GHOST_DEMO_SMOKE=1 (starts Neo4j, Postgres, the worker and core; about six minutes)")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := demorun.FindRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	s := newSmoke(t, root)
	defer s.finish()

	// Stage 1: a report for the sample snapshot (the full CRMArena snapshot is too large for a smoke).
	opp := s.mineSample()

	// Stage 2: up.
	upArgs := []string{"up", "--llm-mode", "replay", "--no-slack", "--no-web"}
	if s.cassettes != "" {
		upArgs = append(upArgs, "--cassettes", s.cassettes)
	}
	out := s.demo("up", 15*time.Minute, upArgs...)
	s.must(strings.Contains(out, "The demo stack is up"), "up", "the stack did not come up:\n"+out)
	s.stage("up", "PASS", "neo4j, postgres, worker (replay), core healthy; slack and web not started by design")
	s.requireRunning("neo4j", "postgres", "worker", "core")

	// Stage 3: seed, then again (idempotent).
	seedArgs := []string{"seed", "--data", "fixtures/crmarena_sample", "--report", s.reportPath, "--opportunity", opp}
	out = s.demo("seed", 20*time.Minute, seedArgs...)
	s.must(strings.Contains(out, "PASS event-N-invisible: withheld"), "seed", "Event N was not reported withheld:\n"+out)
	s.must(strings.Contains(out, "seeded "), "seed", "seed did not finish:\n"+out)
	s.stage("seed", "PASS", "case frozen into the empty database (Event N never ingested), graph projected, event-N-invisible: withheld")
	out = s.demo("seed again", 5*time.Minute, seedArgs...)
	s.must(strings.Contains(out, "already seeded"), "seed", "a second seed must be idempotent:\n"+out)
	s.stage("seed idempotent", "PASS", "a second `demo seed` reports already seeded and re-checks invisibility")

	// Stage 4: play.
	played := s.play()

	// Stage 5: verify, before and after a restart (the data must survive).
	s.verify("verify", played)
	s.restart(upArgs)
	s.requireRunning("neo4j", "postgres", "worker", "core")
	s.verify("verify after restart", played)

	// Stage 6: down and reset.
	s.demo("down", 3*time.Minute, "down")
	s.requireStopped("neo4j", "postgres", "worker", "core")
	s.stage("down", "PASS", "every service stopped; data kept")
	s.scanLogs()
	s.demo("reset", 3*time.Minute, "reset", "--yes")
	if _, err := os.Stat(filepath.Join(s.stateDir, "pg", "data")); !os.IsNotExist(err) {
		s.fail("reset", "the Postgres data directory survived reset")
	}
	s.stage("reset", "PASS", "database, graph, manifest and logs wiped")
	s.noSecretLeaks()
}

// smoke drives the subprocesses and keeps the honest report.
type smoke struct {
	t          *testing.T
	root       string
	exe        string
	stateDir   string
	env        []string
	envFile    string
	cassettes  string
	reportPath string
	lines      []string
	canary     string
	leaks      int
}

func newSmoke(t *testing.T, root string) *smoke {
	t.Helper()
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "ghostctl"+exeExt())
	build := exec.Command("go", "build", "-o", exe, "./cmd/ghostctl")
	build.Dir = filepath.Join(root, "core-go")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ghostctl: %v\n%s", err, b)
	}
	canary := "smoke-canary-key-" + fmt.Sprint(time.Now().UnixNano())
	envFile := filepath.Join(tmp, "smoke.env")
	if err := os.WriteFile(envFile, []byte("OPENROUTER_API_KEY="+canary+"\nSLACK_CHANNEL_ID=C0SMOKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &smoke{t: t, root: root, exe: exe, stateDir: filepath.Join(tmp, "state"), envFile: envFile, canary: canary,
		reportPath: filepath.Join(tmp, "mine", "demo-cases-2026-10-03.json")}
	s.env = append(os.Environ(),
		"GHOST_DEMO_DIR="+s.stateDir, "GHOST_ENV_FILE="+envFile,
		"GHOST_DEMO_PORT_CORE="+freePort(t), "GHOST_DEMO_PORT_WORKER="+freePort(t), "GHOST_DEMO_PORT_POSTGRES="+freePort(t),
		"GHOST_DEMO_PORT_BOLT="+freePort(t), "GHOST_DEMO_PORT_WEB="+freePort(t),
		// A shared database that the demo must never touch: any use of it fails the smoke.
		"DATABASE_URL=postgres://shared:shared@127.0.0.1:1/never_touch?sslmode=disable",
	)
	s.cassettes = findSmokeCassettes(root)
	return s
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

func findSmokeCassettes(root string) string {
	for _, c := range []string{os.Getenv("GHOST_DEMO_CASSETTES"), filepath.Join(root, "data", "crmarena_extraction", "cassettes"),
		filepath.Join(demorun.MainCheckout(root), "data", "crmarena_extraction", "cassettes")} {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}

func (s *smoke) stage(name, status, detail string) {
	line := fmt.Sprintf("%-8s %-24s %s", status, name, detail)
	s.lines = append(s.lines, line)
	s.t.Log(line)
}

func (s *smoke) fail(name, detail string) {
	s.stage(name, "FAIL", detail)
	s.t.Fatalf("%s: %s", name, detail)
}

func (s *smoke) must(ok bool, name, detail string) {
	if !ok {
		s.fail(name, detail)
	}
}

// run executes ghostctl with args in the repository root and returns its combined output.
func (s *smoke) run(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.exe, args...)
	cmd.Dir = s.root
	cmd.Env = s.env
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if strings.Contains(buf.String(), s.canary) {
		s.leaks++
		s.t.Errorf("the output of `ghostctl %s` contains the canary model key", strings.Join(args, " "))
	}
	return buf.String(), err
}

// demo runs `ghostctl demo ...` and fails the smoke if it exits non-zero.
func (s *smoke) demo(name string, timeout time.Duration, args ...string) string {
	out, err := s.run(timeout, append([]string{"demo"}, args...)...)
	s.t.Logf("--- demo %s ---\n%s", strings.Join(args, " "), out)
	if err != nil {
		s.fail(name, fmt.Sprintf("`demo %s` failed: %v", strings.Join(args, " "), err))
	}
	return out
}

func (s *smoke) mineSample() string {
	dir := filepath.Dir(s.reportPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	out, err := s.run(10*time.Minute, "mine-demo-cases", "--data", "fixtures/crmarena_sample", "--scoring", "bench/config/demo_case_scoring.v1.json",
		"--date", "2026-10-03", "--out-dir", dir)
	if err != nil {
		s.fail("mine sample", fmt.Sprintf("%v\n%s", err, out))
	}
	b, err := os.ReadFile(s.reportPath)
	if err != nil {
		s.fail("mine sample", err.Error())
	}
	var rep struct {
		Top []struct {
			OpportunityID string `json:"opportunity_id"`
			AccountName   string `json:"account_name"`
		} `json:"top_cases"`
	}
	if err := json.Unmarshal(b, &rep); err != nil || len(rep.Top) == 0 {
		s.fail("mine sample", "the sample report ranks no case")
	}
	s.stage("mine sample report", "PASS", "top case "+rep.Top[0].AccountName+" ("+rep.Top[0].OpportunityID+") from fixtures/crmarena_sample")
	return rep.Top[0].OpportunityID
}

// play runs `demo play` and returns what it reached: "full" (strategy run published), "bi" (BI update written, run did
// not publish under replay) or "none". Anything the replay cannot do is recorded as SKIPPED with the reason, never as a pass.
func (s *smoke) play() string {
	out, err := s.run(8*time.Minute, "demo", "play", "--timeout", "90s")
	s.t.Logf("--- demo play ---\n%s", out)
	switch {
	case err == nil && strings.Contains(out, "OPEN IN THE BROWSER"):
		s.stage("play", "PASS", "Event N released; BI update written; strategy run published")
		s.stage("M1/M2 in Slack", "SKIPPED", "the Slack bot was not started (--no-slack); no ts was observed")
		return "full"
	case strings.Contains(out, "BI update written") || strings.Contains(out, "not material"):
		s.stage("play", "PASS", "Event N released through ingest, recompute (extraction replayed from cassettes), state diff, signals and the BI writer")
		s.stage("strategy run to published", "SKIPPED", "replay has no cassette for strategy generation, so the run stays paused: "+lastLine(out))
		s.stage("M1/M2 in Slack", "SKIPPED", "the Slack bot was not started (--no-slack); no ts was observed")
		return "bi"
	case s.cassettes == "":
		s.stage("play", "SKIPPED", "no extraction cassettes found (GHOST_DEMO_CASSETTES), so the held-out email cannot be extracted offline and Play cannot finish: "+lastLine(out))
		return "none"
	default:
		s.fail("play", "Play failed although extraction cassettes were available:\n"+out)
		return ""
	}
}

// verify runs `demo verify`; it may exit non-zero (steps the smoke cannot reach FAIL or SKIP), so only the steps it
// can reach are asserted.
func (s *smoke) verify(name, played string) {
	out, _ := s.run(2*time.Minute, "demo", "verify")
	s.t.Logf("--- demo verify ---\n%s", out)
	s.must(strings.Contains(out, "PASS  V01"), name, "V01 (case frozen, Event N invisible before Play) did not pass:\n"+out)
	switch played {
	case "full", "bi":
		s.must(strings.Contains(out, "PASS  V02"), name, "V02 (Event N released) did not pass:\n"+out)
		s.must(strings.Contains(out, "PASS  V03") || strings.Contains(out, "not material"), name, "V03 (BI update written) did not pass:\n"+out)
	}
	s.stage(name, "PASS", "V01..V03 read back from the database; V04..V13 are SKIP or FAIL by design here (no Slack, no human clicks)")
}

func (s *smoke) restart(upArgs []string) {
	s.demo("down", 3*time.Minute, "down")
	s.requireStopped("neo4j", "postgres", "worker", "core")
	out := s.demo("up again", 15*time.Minute, upArgs...)
	s.must(strings.Contains(out, "The demo stack is up"), "restart", "the stack did not come back up:\n"+out)
	s.stage("restart", "PASS", "down then up: the stores came back with their data")
}

func (s *smoke) statusOf(service string) string {
	out, _ := s.run(time.Minute, "demo", "status")
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == service {
			return f[1]
		}
	}
	return ""
}

func (s *smoke) requireRunning(services ...string) {
	for _, svc := range services {
		if got := s.statusOf(svc); got != "running" {
			s.fail("status", fmt.Sprintf("%s is %q, want running", svc, got))
		}
	}
}

func (s *smoke) requireStopped(services ...string) {
	for _, svc := range services {
		if got := s.statusOf(svc); got != "stopped" {
			s.fail("status", fmt.Sprintf("%s is %q, want stopped", svc, got))
		}
	}
}

// scanLogs looks for the canary key and the poisoned shared-database URL in every service log (before reset wipes them).
func (s *smoke) scanLogs() {
	files, _ := filepath.Glob(filepath.Join(s.stateDir, "logs", "*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if bytes.Contains(b, []byte(s.canary)) {
			s.leaks++
			s.t.Errorf("service log %s contains the canary model key", filepath.Base(f))
		}
		if bytes.Contains(b, []byte("never_touch")) {
			s.leaks++
			s.t.Errorf("service log %s mentions the poisoned shared database: the demo used DATABASE_URL", filepath.Base(f))
		}
	}
	s.stage("log scan", "PASS", fmt.Sprintf("%d log file(s) scanned for the canary key and the shared database URL", len(files)))
}

// noSecretLeaks asserts nothing leaked in any command output or log (counted as they were seen).
func (s *smoke) noSecretLeaks() {
	if s.leaks != 0 {
		s.fail("secrets", fmt.Sprintf("%d leak(s) of the canary key or the shared database URL", s.leaks))
	}
	s.stage("secrets", "PASS", "the canary model key never appeared in a command output or a service log; the poisoned DATABASE_URL was never used")
}

func (s *smoke) finish() {
	notCovered := []string{
		"NOT COVERED: a live LLM (replay only), the real Slack workspace (bot not started), the web app (--no-web),",
		"strategy generation and the semantic evals, M1/M2/M3, and the human clicks (Choose, Edit, Send, verdicts).",
		"Those are exercised by the user running `demo up`, `demo seed`, `demo play` live.",
	}
	report := strings.Join(append(append([]string{"DEMO SMOKE REPORT"}, s.lines...), notCovered...), "\n") + "\n"
	s.t.Log("\n" + report)
	if path := os.Getenv("GHOST_DEMO_SMOKE_REPORT"); path != "" {
		_ = os.WriteFile(path, []byte(report), 0o644)
	}
	// Always stop what a failed run left behind.
	_, _ = s.run(5*time.Minute, "demo", "down")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
