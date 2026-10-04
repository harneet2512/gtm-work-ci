package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exec(t *testing.T, env map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, func(k string) string { return env[k] }, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":        {},
		"unknown command":   {"play"},
		"no-human w/o out":  {"no-human"},
		"verify w/o dir":    {"verify"},
		"verify bad flag":   {"verify", "--nope"},
		"no-human bad flag": {"no-human", "--nope"},
	} {
		if code, _, _ := exec(t, nil, args...); code != 2 {
			t.Errorf("%s: exit code %d, want 2", name, code)
		}
	}
}

func TestNoHumanThenVerifyEndToEnd(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")

	if code, _, stderr := exec(t, nil, "no-human", "--out", dir); code != 0 {
		t.Fatalf("no-human: %d %s", code, stderr)
	}
	code, out, _ := exec(t, map[string]string{"GHOST_API_TOKEN": "a-token-that-is-not-logged"}, "verify", "--dir", dir, "--secret-env", "GHOST_API_TOKEN,UNSET_ONE")

	if code != 0 || !strings.Contains(out, "all 14 checks passed") || strings.Contains(out, "FAIL") {
		t.Fatalf("verify: %d\n%s", code, out)
	}
	// a leak turns the exit code non-zero and the report names the file, not the secret
	if err := os.WriteFile(filepath.Join(dir, "slackbot.log"), []byte("token a-token-that-is-not-logged"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = exec(t, map[string]string{"GHOST_API_TOKEN": "a-token-that-is-not-logged"}, "verify", "--dir", dir, "--secret-env", "GHOST_API_TOKEN")
	if code != 1 || !strings.Contains(out, "FAIL  no token appears in any log") || strings.Contains(out, "a-token-that-is-not-logged") {
		t.Fatalf("verify with a leak: %d\n%s", code, out)
	}
}

func TestVerifyOfAnEmptyDirectoryIsAnError(t *testing.T) {
	if code, _, stderr := exec(t, nil, "verify", "--dir", t.TempDir()); code != 1 || stderr == "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
}
