package demorun

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadReportReadsAReportAndRejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "demo-cases-2026-10-04.json")
	body, _ := json.Marshal(sampleReport())
	if err := os.WriteFile(good, body, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := LoadReport(good)
	if err != nil || rep.Date != "2026-10-04" || len(rep.Top) != 2 {
		t.Fatalf("LoadReport = %+v, %v", rep.Date, err)
	}
	if _, err := LoadReport(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing report is an error")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(bad); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("a malformed report must name the file, got %v", err)
	}
}

func TestEnvGoStringAndMergedNeverExposeValues(t *testing.T) {
	e := Env{"K": secretValue}
	if s := e.GoString(); strings.Contains(s, secretValue) || !strings.Contains(s, "K") {
		t.Fatalf("GoString = %q", s)
	}
	c := testConfig()
	if got := c.Merged("OPENROUTER_API_KEY"); got != orKey {
		t.Fatal("Merged must return the effective value for decisions like 'is a key set'")
	}
	c.Process = Env{"OPENROUTER_API_KEY": "from-process"}
	if got := c.Merged("OPENROUTER_API_KEY"); got != "from-process" {
		t.Fatal("process beats .env")
	}
}

func TestCopyFileReplacesTheDestinationAtomically(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("dst = %q", b)
	}
	if _, err := os.Stat(dst + ".new"); !os.IsNotExist(err) {
		t.Fatal("the temp copy must not be left behind")
	}
	if err := copyFile(filepath.Join(dir, "none"), dst); err == nil {
		t.Fatal("a missing source is an error")
	}
}

func TestLastLineAndSplitLinesHandleWindowsOutput(t *testing.T) {
	if got := lastLine("one\r\ntwo\r\nERROR: pip failed\r\n"); got != "ERROR: pip failed" {
		t.Fatalf("lastLine = %q", got)
	}
	if got := lastLine(""); got != "" {
		t.Fatalf("lastLine of nothing = %q", got)
	}
	if got := splitLines("a\n\nb"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("splitLines = %q", got)
	}
}

func TestVenvPythonPathIsUnderTheVenv(t *testing.T) {
	p := filepathSlash(VenvPython("/repo/.demo/venv"))
	if !strings.HasPrefix(p, "/repo/.demo/venv/") || !strings.Contains(p, "python") {
		t.Fatalf("VenvPython = %q", p)
	}
	if exeSuffix() != ".exe" && exeSuffix() != "" {
		t.Fatalf("exeSuffix = %q", exeSuffix())
	}
}

func TestStopLeftoverPostgresDoesNothingWithoutAPostmasterPid(t *testing.T) {
	if err := StopLeftoverPostgres(t.TempDir()); err != nil {
		t.Fatalf("no postmaster.pid, nothing to stop: %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "postmaster.pid"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := StopLeftoverPostgres(dir); err == nil || !strings.Contains(err.Error(), "pg_ctl") {
		t.Fatalf("a leftover server without pg_ctl must say so, got %v", err)
	}
}

func TestPostgresCheckFailsAgainstAClosedPort(t *testing.T) {
	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := PostgresCheck("postgres://ghost:ghost@127.0.0.1:1/ghost_demo?sslmode=disable")(c); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Fatalf("want a 'not answering' error, got %v", err)
	}
}

func TestPostgresReadyNeedsTheHostsReadyMarkerNotJustAnOpenDatabase(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "postgres.ready")
	check := PostgresReady("postgres://ghost:ghost@127.0.0.1:1/ghost_demo?sslmode=disable", marker)
	c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := check(c); err == nil || !strings.Contains(err.Error(), "migrating") {
		t.Fatalf("without the marker the database is not ready yet, got %v", err)
	}
	if err := os.WriteFile(marker, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(c); err == nil || !strings.Contains(err.Error(), "not answering") {
		t.Fatalf("with the marker the database must still answer, got %v", err)
	}
	var pg Spec
	for _, s := range testConfig().Specs(testTooling()) {
		if s.Name == SvcPostgres {
			pg = s
		}
	}
	if !strings.HasSuffix(filepathSlash(pg.Env["GHOST_DEMO_HOST_READYFILE"]), "postgres.ready") {
		t.Fatalf("the postgres host must be told where to write its ready marker: %v", pg.Env.Names())
	}
}

func TestLatestBIReadsTheNewestUpdateAndTreats404AsNone(t *testing.T) {
	c := fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/accounts/acct-1/business-intelligence/latest" {
			writeJSON(w, 200, map[string]any{"id": "bi-9", "summary": "Budget moved"})
			return
		}
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "no_update", "message": "none"}})
	})
	if id, sum, err := c.LatestBI(context.Background(), "acct-1"); err != nil || id != "bi-9" || sum != "Budget moved" {
		t.Fatalf("LatestBI = %q %q %v", id, sum, err)
	}
	if id, _, err := c.LatestBI(context.Background(), "acct-2"); err != nil || id != "" {
		t.Fatalf("a 404 means no update, not an error: %q %v", id, err)
	}
}

func TestPlayOptionDefaultsAndTheRealSleepHonourCancellation(t *testing.T) {
	o := PlayOptions{}.withDefaults()
	if o.Timeout != 20*time.Minute || o.Poll != 3*time.Second || o.Now == nil || o.Sleep == nil {
		t.Fatalf("defaults wrong: %+v", o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled sleep must return the context error, got %v", err)
	}
	if err := o.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("a short sleep completes: %v", err)
	}
}

func TestSaveStateReportsAnUnwritableDirectory(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(filepath.Join(blocker, "state.json"), DemoState{}); err == nil {
		t.Fatal("saving under a file must fail")
	}
}

func TestSmallHelpersFormatPredictably(t *testing.T) {
	if orDash("") != "-" || orDash("x") != "x" {
		t.Fatal("orDash")
	}
	if got := clip("abcdef", 3); got != "abc..." {
		t.Fatalf("clip = %q", got)
	}
	if got := clip("ab", 3); got != "ab" {
		t.Fatalf("clip short = %q", got)
	}
	if got := shortHash("abcdef0123456789"); got != "abcdef012345" || shortHash("ab") != "ab" {
		t.Fatalf("shortHash = %q", got)
	}
	if err := (&killError{pid: 7, out: "denied", err: errors.New("x")}); err.Error() == "" || !errors.Is(err, err.Unwrap()) {
		t.Fatalf("killError: %v", err)
	}
	if KillTree(0) != nil || KillTree(-5) != nil {
		t.Fatal("a non-positive pid is a no-op")
	}
	e := &ServiceError{Service: "core", Phase: "start", Err: context.Canceled}
	if !errors.Is(e, context.Canceled) {
		t.Fatal("ServiceError must unwrap")
	}
}
