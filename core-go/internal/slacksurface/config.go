package slacksurface

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Secret never prints its value, whatever formatting verb or logger is used.
type Secret string

func (Secret) String() string               { return "[redacted]" }
func (Secret) GoString() string             { return "[redacted]" }
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// Environment variable names.
const (
	EnvAppToken  = "SLACK_APP_TOKEN"
	EnvBotToken  = "SLACK_BOT_TOKEN"
	EnvChannelID = "SLACK_CHANNEL_ID"
	EnvCoreURL   = "CORE_URL"
	EnvAPIToken  = "GHOST_API_TOKEN"
	// EnvWebURL is optional: the web UI base URL that View account map opens.
	EnvWebURL = "GHOST_WEB_URL"
	// EnvManifestID is optional: the replay manifest of the demo. Message 1's View trace opens the episode page
	// with it, which resolves Message 1 only through the manifest.
	EnvManifestID = "GHOST_DEMO_MANIFEST_ID"
	// EnvAllowedUsers is optional: comma-separated Slack member ids allowed to act.
	EnvAllowedUsers = "SLACK_ALLOWED_USER_IDS"
	// EnvAllowAll must be exactly "1" to run the listener with no allowlist.
	EnvAllowAll = "SLACK_ALLOW_ALL_USERS"
	// EnvAuditFile is optional: a file the adapter appends one JSON line to for every Slack write it makes
	// (method, channel, ts, block count; no text, no tokens). The smoke test counts messages from it.
	EnvAuditFile = "GHOST_SLACK_AUDIT_FILE"
	// EnvOutbox is optional: "off" stops the listener from reacting to core's strategy_set.published events (the
	// relay then posts nothing by itself and Message 2 only goes out through `slackbot --post-run`). Any other
	// value, or none, leaves the relay on.
	EnvOutbox = "SLACK_OUTBOX"
	// EnvOutboxPollMS is optional: how often the relay asks core for new events, in milliseconds (default 2000).
	EnvOutboxPollMS = "SLACK_OUTBOX_POLL_MS"
)

// Config is the live-mode configuration, read only from the environment.
type Config struct {
	AppToken  Secret
	BotToken  Secret
	ChannelID string
	CoreURL   string
	APIToken  Secret
	WebURL    string
	// ManifestID is GHOST_DEMO_MANIFEST_ID (optional).
	ManifestID string
	// AllowedUsers, when non-empty, limits who may press Choose/Edit/Send and answer the inference.
	AllowedUsers []string
	// AllowAllUsers is the explicit opt-out of the allowlist (SLACK_ALLOW_ALL_USERS=1).
	AllowAllUsers bool
	// AuditFile, when set, receives the audit log of Slack writes (see AuditRecord).
	AuditFile string
	// RelayOff is SLACK_OUTBOX=off: the listener does not post Message 2 when core publishes a strategy set.
	RelayOff bool
	// RelayPoll is how often the relay reads core's outbox (SLACK_OUTBOX_POLL_MS; 0 selects DefaultRelayPoll).
	RelayPoll time.Duration
}

// LoadConfig reads the live-mode settings through getenv (os.Getenv in production). It refuses to
// return a config when anything is missing or malformed, and its error names variables only,
// never their values.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		AppToken:   Secret(strings.TrimSpace(getenv(EnvAppToken))),
		BotToken:   Secret(strings.TrimSpace(getenv(EnvBotToken))),
		ChannelID:  strings.TrimSpace(getenv(EnvChannelID)),
		CoreURL:    strings.TrimSpace(getenv(EnvCoreURL)),
		APIToken:   Secret(strings.TrimSpace(getenv(EnvAPIToken))),
		WebURL:     strings.TrimSpace(getenv(EnvWebURL)),
		ManifestID: strings.TrimSpace(getenv(EnvManifestID)),
		AuditFile:  strings.TrimSpace(getenv(EnvAuditFile)),
	}
	for _, id := range strings.Split(getenv(EnvAllowedUsers), ",") {
		if id = strings.TrimSpace(id); id != "" {
			cfg.AllowedUsers = append(cfg.AllowedUsers, id)
		}
	}
	cfg.AllowAllUsers = strings.TrimSpace(getenv(EnvAllowAll)) == "1"
	cfg.RelayOff = strings.EqualFold(strings.TrimSpace(getenv(EnvOutbox)), "off")
	var problems []string
	if raw := strings.TrimSpace(getenv(EnvOutboxPollMS)); raw != "" {
		ms, err := strconv.Atoi(raw)
		if err != nil || ms < 1 {
			problems = append(problems, EnvOutboxPollMS+" must be a positive number of milliseconds")
		} else {
			cfg.RelayPoll = time.Duration(ms) * time.Millisecond
		}
	}
	need := func(name, val string) {
		if val == "" {
			problems = append(problems, name+" is not set")
		}
	}
	need(EnvAppToken, string(cfg.AppToken))
	need(EnvBotToken, string(cfg.BotToken))
	need(EnvChannelID, cfg.ChannelID)
	need(EnvCoreURL, cfg.CoreURL)
	need(EnvAPIToken, string(cfg.APIToken))
	if cfg.AppToken != "" && !strings.HasPrefix(string(cfg.AppToken), "xapp-") {
		problems = append(problems, EnvAppToken+" must be an app-level token (xapp-...)")
	}
	if cfg.BotToken != "" && !strings.HasPrefix(string(cfg.BotToken), "xoxb-") {
		problems = append(problems, EnvBotToken+" must be a bot token (xoxb-...)")
	}
	if cfg.CoreURL != "" {
		if msg := checkCoreURL(cfg.CoreURL); msg != "" {
			problems = append(problems, EnvCoreURL+" "+msg)
		}
	}
	if len(problems) > 0 {
		return Config{}, errors.New("slacksurface: live mode needs configuration: " + strings.Join(problems, "; "))
	}
	return cfg, nil
}

// RequireAuthorization makes the listener fail closed: it may only start with an allowlist, or with
// the explicit SLACK_ALLOW_ALL_USERS=1 opt-out. Anyone who can see the channel (guests in a shared
// channel included) could otherwise send a real email with one click.
func (c Config) RequireAuthorization() error {
	if len(c.AllowedUsers) > 0 || c.AllowAllUsers {
		return nil
	}
	return errors.New("slacksurface: refusing to start the listener: " + EnvAllowedUsers +
		" is empty; set it to the Slack member ids allowed to act, or set " + EnvAllowAll + "=1 to allow every channel member")
}

// checkCoreURL requires an http(s) URL, and https unless core is on this machine, because the core
// bearer token travels in every request.
func checkCoreURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "must be an http(s) URL"
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "must use https unless core is on localhost"
	}
	return ""
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// String is safe to log: tokens are redacted and any credentials in the core URL are dropped.
func (c Config) String() string {
	core := c.CoreURL
	if u, err := url.Parse(core); err == nil {
		core = u.Redacted()
	}
	return fmt.Sprintf("Config{channel=%s core=%s allowed_users=%d tokens=[redacted]}", c.ChannelID, core, len(c.AllowedUsers))
}
