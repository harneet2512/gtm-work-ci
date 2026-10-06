package demosmoke_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demosmoke"
	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
)

var (
	once sync.Once
	base string
	rerr error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if base != "" {
		_ = os.RemoveAll(base)
	}
	os.Exit(code)
}

// completedRun runs the no-human flow once per test binary and returns a private copy of its artefacts.
func completedRun(t *testing.T) string {
	t.Helper()
	once.Do(func() {
		if base, rerr = os.MkdirTemp("", "demosmoke-run-"); rerr == nil {
			rerr = demosmoke.RunNoHuman(context.Background(), base)
		}
	})
	if rerr != nil {
		t.Fatalf("no-human run: %v", rerr)
	}
	dir := t.TempDir()
	for _, f := range []string{demosmoke.CoreLogFile, demosmoke.SlackAuditFile} {
		b, err := os.ReadFile(filepath.Join(base, f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func verify(t *testing.T, dir string, opts ...func(*demosmoke.VerifyOptions)) []demosmoke.Check {
	t.Helper()
	o := demosmoke.VerifyOptions{Dir: dir, Getenv: func(string) string { return "" }}
	for _, f := range opts {
		f(&o)
	}
	checks, err := demosmoke.Verify(o)
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

func failedNames(cs []demosmoke.Check) []string {
	var names []string
	for _, c := range demosmoke.Failed(cs) {
		names = append(names, c.Name)
	}
	return names
}

func TestTheNoHumanFlowPassesEveryCheck(t *testing.T) {
	dir := completedRun(t)

	checks := verify(t, dir)

	if bad := failedNames(checks); len(bad) != 0 {
		t.Fatalf("failed checks: %v", bad)
	}
	if len(checks) < 14 {
		t.Fatalf("only %d checks ran", len(checks))
	}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCatchesAFourthVisibleMessage(t *testing.T) {
	dir := completedRun(t)
	appendFile(t, filepath.Join(dir, demosmoke.SlackAuditFile), `{"at":"2999-01-01T00:00:00Z","method":"chat.postMessage","channel":"C1","ts":"9.9","ok":true}`+"\n")

	bad := failedNames(verify(t, dir))

	if !contains(bad, "visible bot messages <= 3") || !contains(bad, "M1, M2 and M3 were all posted") {
		t.Fatalf("failed checks = %v", bad)
	}
}

func TestVerifyCatchesAnUpdateOfAMessageThisRunDidNotPost(t *testing.T) {
	dir := completedRun(t)
	appendFile(t, filepath.Join(dir, demosmoke.SlackAuditFile), `{"at":"2999-01-01T00:00:00Z","method":"chat.update","channel":"C1","ts":"8.8","ok":true}`+"\n")

	if bad := failedNames(verify(t, dir)); !contains(bad, "every update edits a message this run posted") {
		t.Fatalf("failed checks = %v", bad)
	}
}

func TestVerifyCatchesRegenerationAndDuplicatePersistence(t *testing.T) {
	dir := completedRun(t)
	path := filepath.Join(dir, demosmoke.CoreLogFile)
	var log fakecore.Log
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatal(err)
	}
	log.StrategiesSHA256 = append(log.StrategiesSHA256, "regenerated")
	log.Effects = append(log.Effects, log.Effects[0]) // a second recorded send
	b, _ := json.Marshal(log)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	bad := failedNames(verify(t, dir))

	if !contains(bad, "no draft regeneration on View or Choose") || !contains(bad, "send was executed once (record-only effect)") {
		t.Fatalf("failed checks = %v", bad)
	}
}

func TestVerifyFindsTokensByShapeAndByValueWithoutPrintingThem(t *testing.T) {
	for name, content := range map[string]string{
		"slack shape": "connected with xoxb-1234567890-abcdefghij",
		"known value": "the core token was hunter2hunter2",
	} {
		t.Run(name, func(t *testing.T) {
			dir := completedRun(t)
			if err := os.WriteFile(filepath.Join(dir, "slackbot.log"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			checks := verify(t, dir, func(o *demosmoke.VerifyOptions) {
				o.SecretEnv = []string{"GHOST_API_TOKEN"}
				o.Getenv = func(string) string { return "hunter2hunter2" }
			})

			bad := demosmoke.Failed(checks)
			if len(bad) != 1 || bad[0].Name != "no token appears in any log" || !strings.Contains(bad[0].Detail, "slackbot.log") {
				t.Fatalf("failed checks = %+v", bad)
			}
			if strings.Contains(bad[0].Detail, "hunter2") || strings.Contains(bad[0].Detail, "xoxb-") {
				t.Fatalf("the report leaks the secret: %s", bad[0].Detail)
			}
		})
	}
}

func TestPartialRunsAreAcceptedOnlyWhenAsked(t *testing.T) {
	dir := completedRun(t)
	var kept []string
	raw, _ := os.ReadFile(filepath.Join(dir, demosmoke.SlackAuditFile))
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.Contains(line, `"ts":"1700000000.000103"`) { // drop M3 and its update
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, demosmoke.SlackAuditFile), []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if bad := failedNames(verify(t, dir)); !contains(bad, "M1, M2 and M3 were all posted") {
		t.Fatalf("a missing M3 must fail by default: %v", bad)
	}
	partial := verify(t, dir, func(o *demosmoke.VerifyOptions) { o.AllowPartial = true })
	if bad := failedNames(partial); len(bad) != 0 {
		t.Fatalf("a run that stopped before M3 must pass with --allow-partial: %v", bad)
	}
}

func TestVerifyErrorsOnMissingArtefacts(t *testing.T) {
	if _, err := demosmoke.Verify(demosmoke.VerifyOptions{Dir: t.TempDir(), Getenv: func(string) string { return "" }}); err == nil {
		t.Fatal("an empty run directory was verified")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// The same flow answered with Don't learn this: every check still passes, and the one verdict core accepted is
// an explicit no_learning, with no correction text.
func TestTheNoHumanFlowAnsweredWithDontLearnThisPassesEveryCheck(t *testing.T) {
	dir := t.TempDir()
	if err := demosmoke.RunNoHumanAnswering(context.Background(), dir, demosmoke.AnswerNoLearning); err != nil {
		t.Fatalf("no-human run: %v", err)
	}

	if bad := failedNames(verify(t, dir)); len(bad) != 0 {
		t.Fatalf("failed checks: %v", bad)
	}
	raw, err := os.ReadFile(filepath.Join(dir, demosmoke.CoreLogFile))
	if err != nil {
		t.Fatal(err)
	}
	var log fakecore.Log
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatal(err)
	}
	var verdicts []map[string]any
	for _, e := range log.Entries {
		if e.Method == "POST" && strings.HasSuffix(e.Path, "/judgment-verdict") && e.Status == 200 {
			var req map[string]any
			if err := json.Unmarshal(e.Request, &req); err != nil {
				t.Fatal(err)
			}
			verdicts = append(verdicts, req)
		}
	}
	if len(verdicts) != 1 || verdicts[0]["verdict"] != "no_learning" || verdicts[0]["corrected_statement"] != nil {
		t.Fatalf("verdict requests = %v, want exactly one explicit no_learning", verdicts)
	}
}

func TestAnUnknownMessageThreeAnswerIsRefused(t *testing.T) {
	if err := demosmoke.RunNoHumanAnswering(context.Background(), t.TempDir(), "approve"); err == nil {
		t.Fatal("an unknown answer ran")
	}
}

// The same flow answered with Correct: every check still passes, and the one verdict core accepted is an explicit
// confirmed, with no correction text.
func TestTheNoHumanFlowAnsweredWithCorrectPassesEveryCheck(t *testing.T) {
	dir := t.TempDir()
	if err := demosmoke.RunNoHumanAnswering(context.Background(), dir, demosmoke.AnswerCorrect); err != nil {
		t.Fatalf("no-human run: %v", err)
	}
	if bad := failedNames(verify(t, dir)); len(bad) != 0 {
		t.Fatalf("failed checks: %v", bad)
	}
	raw, err := os.ReadFile(filepath.Join(dir, demosmoke.CoreLogFile))
	if err != nil {
		t.Fatal(err)
	}
	var log fakecore.Log
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatal(err)
	}
	var verdicts []map[string]any
	for _, e := range log.Entries {
		if e.Method == "POST" && strings.HasSuffix(e.Path, "/judgment-verdict") && e.Status == 200 {
			var req map[string]any
			if err := json.Unmarshal(e.Request, &req); err != nil {
				t.Fatal(err)
			}
			verdicts = append(verdicts, req)
		}
	}
	if len(verdicts) != 1 || verdicts[0]["verdict"] != "confirmed" || verdicts[0]["corrected_statement"] != nil {
		t.Fatalf("verdict requests = %v, want exactly one explicit confirmed", verdicts)
	}
}
