package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codespaceRepo is demoRepo with the Slack credential file pointed at nothing, so no test reads a real credential file.
func codespaceRepo(t *testing.T) {
	t.Helper()
	demoRepo(t)
	t.Setenv("GHOST_SLACK_CRED_FILE", filepath.Join(t.TempDir(), "no-credentials.md"))
	// Never the real demo home (D:\ghost-demo on Windows): the runner state goes to a temp directory.
	t.Setenv("GHOST_DEMO_DIR", "")
	t.Setenv("GHOST_NEO4J_CACHE", "")
	t.Setenv("GHOST_DEMO_HOME", t.TempDir())
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "SLACK_CHANNEL_ID"} {
		t.Setenv(name, "")
	}
}

func TestCodespaceRejectsMissingAndUnknownSubcommands(t *testing.T) {
	codespaceRepo(t)
	var out bytes.Buffer
	for _, args := range [][]string{{"codespace"}, {"codespace", "explode"}, {"codespace", "up", "--nope"}} {
		if err := run(args, &out); err == nil || !strings.Contains(err.Error(), "usage: ghostctl codespace") {
			t.Errorf("run(%v) = %v, want the usage", args, err)
		}
	}
}

func TestCodespaceStatusOnAnEmptyRepoSaysSetupIsNotFinished(t *testing.T) {
	codespaceRepo(t)
	t.Setenv("OPENROUTER_API_KEY", "")
	var out bytes.Buffer
	if err := run([]string{"codespace", "status"}, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	var st struct {
		Ready          bool     `json:"ready"`
		Phase          string   `json:"phase"`
		Message        string   `json:"message"`
		MissingSecrets []string `json:"missing_secrets"`
		Cases          []struct {
			Slot   string `json:"slot"`
			Seeded bool   `json:"seeded"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("status is not JSON: %v\n%s", err, out.String())
	}
	if st.Ready || len(st.Cases) != 2 || st.Cases[0].Seeded || st.Cases[1].Seeded {
		t.Fatalf("status = %+v", st)
	}
	// Missing secrets outrank setup in the message, and only names are reported.
	if st.Phase != "needs-secrets" || !strings.Contains(st.Message, "OPENROUTER_API_KEY") {
		t.Fatalf("status = %+v", st)
	}
	for _, v := range st.MissingSecrets {
		if strings.Contains(v, "=") {
			t.Fatalf("a missing secret must be a name, got %q", v)
		}
	}
}

func TestCodespaceResetAndCaseNeedTheirArguments(t *testing.T) {
	codespaceRepo(t)
	var out bytes.Buffer
	if err := run([]string{"codespace", "reset"}, &out); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("reset without --yes = %v", err)
	}
	if err := run([]string{"codespace", "case"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("case without a slot = %v", err)
	}
	if err := run([]string{"codespace", "case", "case9"}, &out); err == nil || !strings.Contains(err.Error(), "unknown case") {
		t.Fatalf("unknown case = %v", err)
	}
	if err := run([]string{"codespace", "case", "case1"}, &out); err == nil || !strings.Contains(err.Error(), "not seeded") {
		t.Fatalf("an unseeded case must refuse before touching anything: %v", err)
	}
}

func TestEnsureWebBuildTrustsACurrentBuildAndFailsLoudlyOtherwise(t *testing.T) {
	root := t.TempDir() // not a git checkout: the sources version is unknown, an existing build is trusted
	var out bytes.Buffer
	build := filepath.Join(root, "web", ".next")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(build, "BUILD_ID"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureWebBuild(context.Background(), root, "npm-does-not-exist", &out); err != nil || !strings.Contains(out.String(), "web build is current") {
		t.Fatalf("current build: %v %q", err, out.String())
	}
	if err := os.Remove(filepath.Join(build, "BUILD_ID")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := ensureWebBuild(context.Background(), root, "npm-does-not-exist", &out)
	if err == nil || !strings.Contains(err.Error(), "npm run build") || !strings.Contains(out.String(), "building the web app") {
		t.Fatalf("missing build with no npm: %v %q", err, out.String())
	}
}
