package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// demoRepo is a throwaway repository root (CLAUDE.md next to core-go/) that the demo commands run in, so no test
// writes a .demo directory into the real checkout.
func demoRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("GHOST_ENV_FILE", "")
	return root
}

func TestDemoRejectsMissingAndUnknownSubcommands(t *testing.T) {
	var out bytes.Buffer
	for _, args := range [][]string{{"demo"}, {"demo", "explode"}, {"demo-host"}, {"demo-host", "a", "b"}, {"demo-host", "redis"}} {
		if err := run(args, &out); err == nil {
			t.Errorf("run(%v) should fail", args)
		}
	}
}

func TestDemoUpValidatesFlagsBeforeDoingAnything(t *testing.T) {
	root := demoRepo(t)
	var out bytes.Buffer
	err := run([]string{"demo", "up", "--llm-mode", "bogus"}, &out)
	if err == nil || !strings.Contains(err.Error(), "llm-mode") {
		t.Fatalf("want an --llm-mode error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".demo")); !os.IsNotExist(statErr) {
		t.Fatal("a rejected command must not create .demo")
	}
}

func TestDemoUpLiveModeNeedsAModelKeyAndNamesIt(t *testing.T) {
	demoRepo(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	var out bytes.Buffer
	err := run([]string{"demo", "up", "--no-slack", "--no-web"}, &out)
	if err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("live mode without a key must say which variable is missing, got %v", err)
	}
	if strings.Contains(out.String(), "sk-") {
		t.Fatal("no key value may ever be printed")
	}
}

func TestDemoUpWithSlackNeedsTheChannelIdAndNamesIt(t *testing.T) {
	demoRepo(t)
	t.Setenv("OPENROUTER_API_KEY", "k")
	t.Setenv("SLACK_CHANNEL_ID", "")
	var out bytes.Buffer
	err := run([]string{"demo", "up", "--no-web"}, &out)
	if err == nil || !strings.Contains(err.Error(), "SLACK_CHANNEL_ID") {
		t.Fatalf("a Slack run without a channel id must name the variable, got %v", err)
	}
}

func TestDemoStatusListsEverySurfaceAndNeverPrintsSecrets(t *testing.T) {
	root := demoRepo(t)
	var out bytes.Buffer
	if err := run([]string{"demo", "status"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []string{"neo4j", "postgres", "worker", "core", "slackbot", "web"} {
		if !strings.Contains(out.String(), svc) {
			t.Errorf("status lacks %s:\n%s", svc, out.String())
		}
	}
	secrets, err := os.ReadFile(filepath.Join(root, ".demo", "secrets.env"))
	if err != nil {
		t.Fatalf("secrets file: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(secrets)), "\n") {
		if _, v, ok := strings.Cut(line, "="); ok && v != "" && strings.Contains(out.String(), v) {
			t.Fatal("status printed a secret value")
		}
	}
}

func TestDemoResetRefusesWithoutYes(t *testing.T) {
	demoRepo(t)
	var out bytes.Buffer
	if err := run([]string{"demo", "reset"}, &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("reset must need --yes, got %v", err)
	}
}

func TestDemoSeedPlayVerifyExplainWhatIsMissing(t *testing.T) {
	demoRepo(t)
	var out bytes.Buffer
	if err := run([]string{"demo", "seed"}, &out); err == nil || !strings.Contains(err.Error(), "demo up") {
		t.Fatalf("seed without stores must say `demo up`, got %v", err)
	}
	if err := run([]string{"demo", "seed", "--case", "nope"}, &out); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("an unknown case must be named, got %v", err)
	}
	for _, sub := range []string{"play", "verify"} {
		if err := run([]string{"demo", sub}, &out); err == nil || !strings.Contains(err.Error(), "demo seed") {
			t.Fatalf("%s before seed must point at `demo seed`, got %v", sub, err)
		}
	}
}

func TestDemoLogsRejectsBadServiceNames(t *testing.T) {
	demoRepo(t)
	var out bytes.Buffer
	if err := run([]string{"demo", "logs"}, &out); err == nil {
		t.Fatal("logs needs a service")
	}
	if err := run([]string{"demo", "logs", "../../etc/passwd"}, &out); err == nil {
		t.Fatal("path traversal must be refused")
	}
	if err := run([]string{"demo", "logs", "core"}, &out); err == nil || !strings.Contains(err.Error(), "no log") {
		t.Fatalf("a service that never ran has no log: %v", err)
	}
}

func TestLoadDotEnvPrefersGhostEnvFileThenRootThenMainCheckout(t *testing.T) {
	main := t.TempDir()
	wt := filepath.Join(main, ".claude", "worktrees", "agent-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, ".env"), []byte("FROM=main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOST_ENV_FILE", "")
	env, path, err := loadDotEnv(wt)
	if err != nil || env["FROM"] != "main" || path != filepath.Join(main, ".env") {
		t.Fatalf("a worktree falls back to the main checkout's .env: %v %q %v", env.Names(), path, err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".env"), []byte("FROM=worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if env, _, _ := loadDotEnv(wt); env["FROM"] != "worktree" {
		t.Fatal("the checkout's own .env wins over the main checkout's")
	}
	explicit := filepath.Join(t.TempDir(), "x.env")
	if err := os.WriteFile(explicit, []byte("FROM=explicit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOST_ENV_FILE", explicit)
	if env, _, _ := loadDotEnv(wt); env["FROM"] != "explicit" {
		t.Fatal("GHOST_ENV_FILE wins over both")
	}
}
