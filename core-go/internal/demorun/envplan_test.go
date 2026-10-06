package demorun

import (
	"strings"
	"testing"
)

const (
	neonURL = "postgres://neon-user:neon-secret@ep-prod.neon.tech/prod?sslmode=require"
	orKey   = "sk-or-v1-secretsecretsecret"
)

func testConfig() Config {
	return Config{
		Layout:  NewLayout("/repo"),
		Ports:   Ports{Core: 8080, Worker: 8090, Web: 3000, Postgres: 15432, Bolt: 17687},
		DotEnv:  Env{"DATABASE_URL": neonURL, "OPENROUTER_API_KEY": orKey, "GHOST_MODEL": "openrouter/qwen/qwen3:free", "SLACK_CHANNEL_ID": "C0DEMO", "GHOST_LLM_MAX_RPM": "12", "GHOST_ORCHESTRATOR": "off", "GHOST_ORCHESTRATOR_SCOPE": "all"},
		Process: Env{},
		Secrets: Env{"GHOST_API_TOKEN": "api-token-0123456789abcdef0123456789abcdef", "GHOST_RUN_TOKEN_SECRET": "run-secret-0123456789abcdef0123456789abcdef", "NEO4J_PASSWORD": "neo-pass-1234"},
		Slack:   Env{"SLACK_BOT_TOKEN": "xoxb-bot", "SLACK_APP_TOKEN": "xapp-app"},
		LLMMode: "live",
	}
}

func TestCoreEnvNeverTouchesTheDatabaseInDotEnv(t *testing.T) {
	env := testConfig().CoreEnv()
	if got := env["DATABASE_URL"]; got == neonURL || !strings.Contains(got, "127.0.0.1:15432") {
		t.Fatal("core must point at the demo Postgres, whatever .env holds (the Neon URL must never be used)")
	}
	if env["NEO4J_URI"] != "bolt://127.0.0.1:17687" || env["NEO4J_PASSWORD"] != "neo-pass-1234" {
		t.Fatal("core must point at the demo Neo4j")
	}
}

func TestCoreEnvForcesTheDemoOrchestratorAndSafeRunMode(t *testing.T) {
	env := testConfig().CoreEnv()
	want := map[string]string{
		"GHOST_ORCHESTRATOR": "on", "GHOST_ORCHESTRATOR_SCOPE": "play",
		"GHOST_RUN_MODE": "dry_run", "GHOST_ALLOW_EXTERNAL_WRITES": "false",
		"WORKER_URL": "http://127.0.0.1:8090", "CORE_ADDR": "127.0.0.1:8080",
		"GHOST_REPLAY_EVENTS": "/repo/.demo/replay-events",
	}
	for k, v := range want {
		if got := filepathSlash(env[k]); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestCoreEnvDefaultsTheKnowledgeRulesForTheEpisodeView(t *testing.T) {
	c := testConfig()
	if got := filepathSlash(c.CoreEnv()["GHOST_KNOWLEDGE_RULES"]); got != "contracts/knowledge/lifecycle.v1.json" {
		t.Fatalf("core's /replay episode view needs the knowledge rules, got %q", got)
	}
	c.DotEnv["GHOST_KNOWLEDGE_RULES"] = "custom/rules.json"
	if got := c.CoreEnv()["GHOST_KNOWLEDGE_RULES"]; got != "custom/rules.json" {
		t.Fatalf("the user's value must win, got %q", got)
	}
	if _, on := c.CoreEnv()["GHOST_TRANSITION_RULES"]; on {
		t.Fatal("the transition detector stays off unless the user sets it: the demo history was mined with it off")
	}
}

func TestPacingDefaultsApplyOnlyWhenTheUserDidNotSetThem(t *testing.T) {
	c := testConfig()
	if got := c.WorkerEnv()["GHOST_LLM_MAX_RPM"]; got != "12" {
		t.Fatalf("the user's GHOST_LLM_MAX_RPM must win, got %q", got)
	}
	delete(c.DotEnv, "GHOST_LLM_MAX_RPM")
	if got := c.WorkerEnv()["GHOST_LLM_MAX_RPM"]; got != "15" {
		t.Fatalf("free-tier default is 15 per minute, got %q", got)
	}
	if got := c.CoreEnv()["GHOST_PROVIDER_RPM"]; got != "15" {
		t.Fatalf("core's worker bucket should default to the same ceiling, got %q", got)
	}
	c.DotEnv["GHOST_PROVIDER_RPM"] = "5"
	if got := c.CoreEnv()["GHOST_PROVIDER_RPM"]; got != "5" {
		t.Fatalf("an explicit GHOST_PROVIDER_RPM must win, got %q", got)
	}
}

func TestWorkerEnvCarriesTheModeAndKeyButNoDatabaseOrSlack(t *testing.T) {
	c := testConfig()
	env := c.WorkerEnv()
	if env["GHOST_LLM_MODE"] != "live" || env["OPENROUTER_API_KEY"] != orKey || env["GHOST_MODEL"] != "openrouter/qwen/qwen3:free" {
		t.Fatal("worker is missing its model settings")
	}
	if env["CORE_URL"] != "http://127.0.0.1:8080" {
		t.Fatalf("worker must call the demo core, got %q", env["CORE_URL"])
	}
	for _, leaked := range []string{"DATABASE_URL", "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "GHOST_API_TOKEN", "NEO4J_PASSWORD"} {
		if _, ok := env[leaked]; ok {
			t.Errorf("the worker must not receive %s", leaked)
		}
	}
	c.LLMMode = "replay"
	if got := c.WorkerEnv()["GHOST_LLM_MODE"]; got != "replay" {
		t.Fatalf("replay mode not applied: %q", got)
	}
}

func TestSlackEnvPostsToTheDemoChannelWithOutboxOn(t *testing.T) {
	env := testConfig().SlackEnv()
	if env["SLACK_CHANNEL_ID"] != "C0DEMO" || env["SLACK_OUTBOX"] != "on" || env["SLACK_BOT_TOKEN"] != "xoxb-bot" {
		t.Fatal("slack env incomplete")
	}
	if env["CORE_URL"] != "http://127.0.0.1:8080" || env["GHOST_WEB_URL"] != "http://127.0.0.1:3000" {
		t.Fatal("slack must point at the demo core and web")
	}
	if env["SLACK_ALLOW_ALL_USERS"] != "1" {
		t.Fatal("without SLACK_ALLOWED_USER_IDS the demo falls back to allow-all (a single-user demo workspace)")
	}
	if !strings.HasSuffix(filepathSlash(env["GHOST_SLACK_AUDIT_FILE"]), ".demo/logs/slack-audit.jsonl") {
		t.Fatalf("audit file = %q", env["GHOST_SLACK_AUDIT_FILE"])
	}
	c := testConfig()
	c.DotEnv["SLACK_ALLOWED_USER_IDS"] = "U123"
	e2 := c.SlackEnv()
	if _, all := e2["SLACK_ALLOW_ALL_USERS"]; all || e2["SLACK_ALLOWED_USER_IDS"] != "U123" {
		t.Fatal("an explicit allowlist must be honoured and must not be widened")
	}
}

func TestSlackEnvIsEmptyOfTokensWhenSlackIsDisabled(t *testing.T) {
	c := testConfig()
	c.NoSlack = true
	if len(c.SlackEnv()) != 0 {
		t.Fatal("--no-slack must hand nothing to a Slack process")
	}
	if got := c.CoreEnv()["SLACK_BOT_TOKEN"]; got != "" {
		t.Fatal("core must never see Slack tokens")
	}
}

func TestWebEnvIsMinimal(t *testing.T) {
	env := testConfig().WebEnv()
	if env["CORE_URL"] != "http://127.0.0.1:8080" || env["GHOST_API_TOKEN"] == "" {
		t.Fatal("web needs core's URL and the API token")
	}
	for _, leaked := range []string{"OPENROUTER_API_KEY", "DATABASE_URL", "SLACK_BOT_TOKEN", "NEO4J_PASSWORD"} {
		if _, ok := env[leaked]; ok {
			t.Errorf("the web app must not receive %s", leaked)
		}
	}
}

func TestToolEnvTargetsTheDemoDatabaseAndGraph(t *testing.T) {
	env := testConfig().ToolEnv()
	if !strings.Contains(env["DATABASE_URL"], "127.0.0.1:15432") || env["NEO4J_URI"] != "bolt://127.0.0.1:17687" {
		t.Fatal("ghostctl tools must use the demo stores")
	}
}

func TestProcessEnvBeatsDotEnvAndForcedValuesBeatBoth(t *testing.T) {
	c := testConfig()
	c.DotEnv["GHOST_WORKSPACE_ID"] = "from-dotenv"
	c.Process = Env{"GHOST_WORKSPACE_ID": "from-process", "GHOST_ORCHESTRATOR": "off"}
	env := c.CoreEnv()
	if env["GHOST_WORKSPACE_ID"] != "from-process" {
		t.Fatal("the real environment must override .env, as core's own loader does")
	}
	if env["GHOST_ORCHESTRATOR"] != "on" {
		t.Fatal("the demo's forced settings override both")
	}
}

func TestDescribeNeverContainsValues(t *testing.T) {
	c := testConfig()
	for name, env := range map[string]Env{"core": c.CoreEnv(), "worker": c.WorkerEnv(), "slack": c.SlackEnv(), "web": c.WebEnv()} {
		out := env.String()
		for _, secret := range []string{orKey, "xoxb-bot", "neo-pass-1234", neonURL} {
			if strings.Contains(out, secret) {
				t.Errorf("%s env description leaked a value", name)
			}
		}
	}
}

func filepathSlash(s string) string { return strings.ReplaceAll(s, "\\", "/") }
