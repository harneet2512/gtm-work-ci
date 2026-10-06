package demorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlackTokensPreferEnvThenDotEnv(t *testing.T) {
	got, err := ResolveSlackTokens(Env{"SLACK_BOT_TOKEN": "xoxb-proc"}, Env{"SLACK_BOT_TOKEN": "xoxb-dotenv", "SLACK_APP_TOKEN": "xapp-dotenv"})
	if err != nil {
		t.Fatal(err)
	}
	if got["SLACK_BOT_TOKEN"] != "xoxb-proc" || got["SLACK_APP_TOKEN"] != "xapp-dotenv" {
		t.Fatalf("precedence wrong: %v", got.Names())
	}
}

func TestSlackTokensMissingErrorNamesVariablesNeverValues(t *testing.T) {
	_, err := ResolveSlackTokens(Env{}, Env{"SLACK_BOT_TOKEN": "xoxb-secret-value"})
	if err == nil {
		t.Fatal("a missing token must be an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "SLACK_APP_TOKEN") || strings.Contains(msg, "SLACK_BOT_TOKEN") {
		t.Fatalf("only the missing variable is named: %s", msg)
	}
	if strings.Contains(msg, "xoxb-secret-value") || strings.Contains(msg, "credential file") {
		t.Fatalf("no value and no credential file in the message: %s", msg)
	}
	_, err = ResolveSlackTokens(Env{}, Env{})
	if err == nil || !strings.Contains(err.Error(), "SLACK_BOT_TOKEN, SLACK_APP_TOKEN") {
		t.Fatalf("both are named: %v", err)
	}
}

// The credential file is never read: a file with valid-looking tokens in the places the old code looked changes nothing.
func TestSlackTokensIgnoreAnyCredentialFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, "Desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "bot: xoxb-1111-2222-fakebottoken\napp = xapp-1-A0FAKE-3333-fakeapptoken\n"
	cred := filepath.Join(home, "Desktop", "cloud_"+"access.md")
	if err := os.WriteFile(cred, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOST_SLACK_CRED_FILE", cred)
	if _, err := ResolveSlackTokens(Env{}, Env{}); err == nil {
		t.Fatal("tokens come only from the environment and .env")
	}
}
