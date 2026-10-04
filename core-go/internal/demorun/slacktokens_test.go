package demorun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fakeBot = "xoxb-1111-2222-fakebottoken"
	fakeApp = "xapp-1-A0FAKE-3333-fakeapptoken"
)

func writeCred(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cred.md")
	body := "notes\nconfig xoxe.xoxp-1-notused\nbot: " + fakeBot + "\napp = " + fakeApp + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSlackTokensPreferEnvThenDotEnvThenCredentialFile(t *testing.T) {
	cred := writeCred(t)

	// 1. process env wins over .env and the file.
	got, err := ResolveSlackTokens(Env{"SLACK_BOT_TOKEN": "xoxb-proc"}, Env{"SLACK_BOT_TOKEN": "xoxb-dotenv", "SLACK_APP_TOKEN": "xapp-dotenv"}, cred)
	if err != nil {
		t.Fatal(err)
	}
	if got["SLACK_BOT_TOKEN"] != "xoxb-proc" || got["SLACK_APP_TOKEN"] != "xapp-dotenv" {
		t.Fatalf("precedence wrong: %v", got.Names())
	}

	// 2. nothing in env or .env: the credential file supplies both, by prefix.
	got, err = ResolveSlackTokens(Env{}, Env{}, cred)
	if err != nil {
		t.Fatal(err)
	}
	if got["SLACK_BOT_TOKEN"] != fakeBot || got["SLACK_APP_TOKEN"] != fakeApp {
		t.Fatalf("credential file not used: %v", got.Names())
	}
	if _, leaked := got["SLACK_CONFIG_TOKEN"]; leaked {
		t.Fatal("the config token is never handed to a runtime process")
	}
}

func TestSlackTokensMissingErrorNamesVariablesNeverValues(t *testing.T) {
	_, err := ResolveSlackTokens(Env{}, Env{}, filepath.Join(t.TempDir(), "absent.md"))
	if err == nil {
		t.Fatal("no token anywhere must be an error")
	}
	msg := err.Error()
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"} {
		if !strings.Contains(msg, name) {
			t.Errorf("error should name %s: %s", name, msg)
		}
	}
}

func TestSlackTokensCredentialFileMatchesPrefixOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.md")
	if err := os.WriteFile(p, []byte("xoxb-only-the-bot\nxapp-\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveSlackTokens(Env{}, Env{}, p)
	if err == nil || !strings.Contains(err.Error(), "SLACK_APP_TOKEN") || strings.Contains(err.Error(), "SLACK_BOT_TOKEN") {
		t.Fatalf("only the app token should be reported missing, got %v", err)
	}
}
