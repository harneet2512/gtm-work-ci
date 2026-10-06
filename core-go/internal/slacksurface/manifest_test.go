package slacksurface

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Ask Cliff (owner-approved HAR-129 contract change, 2026-10-06) widens the Slack app by exactly the DM and
// @mention scopes and the two events that carry them, and nothing else. A scope the app does not use is a
// consent the owner must grant for no reason, so the sets are compared exactly.

type slackManifest struct {
	Features struct {
		AppHome struct {
			MessagesTab         bool `yaml:"messages_tab_enabled"`
			MessagesTabReadOnly bool `yaml:"messages_tab_read_only_enabled"`
		} `yaml:"app_home"`
	} `yaml:"features"`
	OAuth struct {
		Scopes struct {
			Bot  []string `yaml:"bot"`
			User []string `yaml:"user"`
		} `yaml:"scopes"`
	} `yaml:"oauth_config"`
	Settings struct {
		Events struct {
			BotEvents  []string `yaml:"bot_events"`
			UserEvents []string `yaml:"user_events"`
			RequestURL string   `yaml:"request_url"`
		} `yaml:"event_subscriptions"`
		Interactivity struct {
			Enabled bool `yaml:"is_enabled"`
		} `yaml:"interactivity"`
		SocketMode bool `yaml:"socket_mode_enabled"`
	} `yaml:"settings"`
}

func loadManifest(t *testing.T, mutate func(*slackManifest)) slackManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "slack", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m slackManifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&m)
	}
	return m
}

var (
	wantBotScopes = []string{"app_mentions:read", "channels:history", "channels:join", "channels:manage", "channels:read",
		"chat:write", "im:history", "im:read", "im:write"}
	wantBotEvents = []string{"app_mention", "message.im"}
)

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// manifestProblems lists every way the manifest differs from the approved Ask Cliff surface.
func manifestProblems(m slackManifest) []string {
	var problems []string
	if got := sorted(m.OAuth.Scopes.Bot); strings.Join(got, ",") != strings.Join(wantBotScopes, ",") {
		problems = append(problems, "bot scopes are "+strings.Join(got, ",")+", want exactly "+strings.Join(wantBotScopes, ","))
	}
	if len(m.OAuth.Scopes.User) != 0 {
		problems = append(problems, "the app asks for user scopes")
	}
	if got := sorted(m.Settings.Events.BotEvents); strings.Join(got, ",") != strings.Join(wantBotEvents, ",") {
		problems = append(problems, "bot events are "+strings.Join(got, ",")+", want exactly "+strings.Join(wantBotEvents, ","))
	}
	if len(m.Settings.Events.UserEvents) != 0 || m.Settings.Events.RequestURL != "" {
		problems = append(problems, "the app subscribes to user events or names a request URL (Socket Mode needs none)")
	}
	if !m.Features.AppHome.MessagesTab || m.Features.AppHome.MessagesTabReadOnly {
		problems = append(problems, "the Messages tab must be on and not read-only so people can DM Cliff")
	}
	if !m.Settings.SocketMode || !m.Settings.Interactivity.Enabled {
		problems = append(problems, "Socket Mode and interactivity must stay on")
	}
	return problems
}

func TestManifestRequestsExactlyTheAskCliffScopesAndEvents(t *testing.T) {
	if problems := manifestProblems(loadManifest(t, nil)); len(problems) != 0 {
		t.Fatalf("contracts/slack/manifest.yaml: %s", strings.Join(problems, "; "))
	}
}

func TestManifestCheckRejectsAnythingBroader(t *testing.T) {
	cases := map[string]func(*slackManifest){
		"users:read":         func(m *slackManifest) { m.OAuth.Scopes.Bot = append(m.OAuth.Scopes.Bot, "users:read") },
		"chat:write.public":  func(m *slackManifest) { m.OAuth.Scopes.Bot = append(m.OAuth.Scopes.Bot, "chat:write.public") },
		"channels:write":     func(m *slackManifest) { m.OAuth.Scopes.Bot = append(m.OAuth.Scopes.Bot, "channels:write") },
		"groups:history":     func(m *slackManifest) { m.OAuth.Scopes.Bot = append(m.OAuth.Scopes.Bot, "groups:history") },
		"a missing im scope": func(m *slackManifest) { m.OAuth.Scopes.Bot = m.OAuth.Scopes.Bot[:len(m.OAuth.Scopes.Bot)-1] },
		"a channel message event": func(m *slackManifest) {
			m.Settings.Events.BotEvents = append(m.Settings.Events.BotEvents, "message.channels")
		},
		"a user scope":    func(m *slackManifest) { m.OAuth.Scopes.User = []string{"search:read"} },
		"a read-only tab": func(m *slackManifest) { m.Features.AppHome.MessagesTabReadOnly = true },
		"no DM tab":       func(m *slackManifest) { m.Features.AppHome.MessagesTab = false },
		"socket mode off": func(m *slackManifest) { m.Settings.SocketMode = false },
	}
	for name, mutate := range cases {
		if len(manifestProblems(loadManifest(t, mutate))) == 0 {
			t.Errorf("%s: the manifest check accepted it", name)
		}
	}
}
