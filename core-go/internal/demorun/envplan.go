package demorun

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/store/embedded"
)

// Free-tier pacing (OpenRouter `:free` models allow about 20 requests a minute and throttle upstream).
const freeTierRPM = "15"

// defaultKnowledgeRules is relative to the repository root, where core runs.
const defaultKnowledgeRules = "contracts/knowledge/lifecycle.v1.json"

// demoDeadlines are the worker's model-call budgets for the demo and the record run (seconds). A thinking model (qwen3.8-flash) takes
// 53 to 80 s a call and more under load, so the defaults (60 s a call, 90 s to 180 s overall) cut calls short: "model call hung,
// retrying once ... overall deadline exceeded" paused the first record run. One call may take 300 s; each overall deadline covers its
// calls (strategies makes three); core's per-call budgets in workerclient (390 s, 480 s, 390 s) exceed them so the worker answers first.
var demoDeadlines = map[string]string{
	"LLM_TIMEOUT_S": "300", "LLM_DEADLINE_S": "360", "DRAFT_DEADLINE_S": "360", "JUDGE_DEADLINE_S": "450",
	// One /v1/judge request is 6 to 9 parallel judge calls and a round judges 3 candidates at once: with the default 8 slots the second and
	// third wave of thinking-model calls waited past the deadline ("judge failed: ProviderError" on the first record).
	"MAX_CONCURRENCY":       "24",
	"STRATEGIES_DEADLINE_S": "360", "EXTRACT_DEADLINE_S": "300",
}

// defaultTransitionRules is the rule set the demo runs with when GHOST_TRANSITION_RULES is unset (.env.example sets the same path). The
// demo needs StateTransitions: M1 "what changed", the candidate policy and the B4 and D1 gates read them. Set it to "off" to run without.
const defaultTransitionRules = "contracts/transitions/rules.v1.json"

// TransitionRules is the transition rule set the demo's core, freeze and graph tools use: GHOST_TRANSITION_RULES, else the repository's
// rule set; "" (detector off) only when the variable is explicitly "off".
func (c Config) TransitionRules() string {
	switch v := c.merged()["GHOST_TRANSITION_RULES"]; v {
	case "":
		return defaultTransitionRules
	case "off":
		return ""
	default:
		return v
	}
}

// Config is everything the per-service environments are derived from. DotEnv is the repository .env,
// Process the real environment (it beats .env, like core's own loader), Secrets the generated tokens and Slack
// the resolved Slack runtime tokens. No method prints a value: Env formats names only.
type Config struct {
	Layout  Layout
	Ports   Ports
	DotEnv  Env
	Process Env
	Secrets Env
	Slack   Env
	LLMMode string // live | replay | record, for the worker
	// ReplayCassettes, with LLMMode replay, is a directory of recorded CRMArena extraction cassettes that the masked
	// replay worker (bench/data/crmarena_replay_worker.py) serves; empty means the stock worker with worker-py/cassettes.
	ReplayCassettes string
	// KnownMisses (HAR-124) is the miss directory or known_extraction_misses.json the replay worker allows as "no claims";
	// any other cassette miss stays an error. Empty: no miss is allowed.
	KnownMisses string
	NoSlack     bool
	NoWeb       bool
	// Database, when set, is the database inside the demo Postgres that core and the tools use instead of the
	// default ghost_demo: the codespace demo freezes each case into its own database (the freeze needs an empty one).
	Database string
	// WebProd serves the web app from its production build (`npm run start`) instead of the dev server, and
	// WebExtraEnv is added to the web server's environment (server side only).
	WebProd     bool
	WebExtraEnv Env
	// GraphInstances is how many Neo4j instances run (0 or 1: one). The codespace demo runs one per case, so the next case's
	// graph is already built when the second Play moves core to it. Instance i listens on Bolt+i (see GraphSlot).
	GraphInstances int
	// Neo4j password and Postgres DSN are derived, not configured: see DSN and Neo4jURI.
}

// GraphSlot is one Neo4j instance of the demo: its service name, data directory and Bolt port. Slot 0 is the original
// instance (service neo4j, <state>/neo4j, Bolt); slot i is neo4j-case<i+1> in <state>/neo4j-case<i+1> on Bolt+i.
type GraphSlot struct {
	Service string
	Dir     string
	Port    int
}

// GraphSlot returns instance i.
func (c Config) GraphSlot(i int) GraphSlot {
	if i <= 0 {
		return GraphSlot{Service: SvcNeo4j, Dir: c.Layout.Neo4jDir(), Port: c.Ports.Bolt}
	}
	name := fmt.Sprintf("%s-case%d", SvcNeo4j, i+1)
	return GraphSlot{Service: name, Dir: filepath.Join(c.Layout.LiveDir(), name), Port: c.Ports.Bolt + i}
}

func (c Config) graphCount() int {
	if c.GraphInstances < 1 {
		return 1
	}
	return c.GraphInstances
}

// GraphDirs are the data directories of every Neo4j instance (the Stop sweep looks for JVMs under each).
func (c Config) GraphDirs() []string {
	dirs := make([]string, 0, c.graphCount())
	for i := 0; i < c.graphCount(); i++ {
		dirs = append(dirs, c.GraphSlot(i).Dir)
	}
	return dirs
}

// ForGraph is the config of the case served by instance i: core and the tools read that instance's graph.
func (c Config) ForGraph(i int) Config {
	c.Ports.Bolt = c.GraphSlot(i).Port
	return c
}

// DSN is the demo Postgres connection string (loopback, the demo's own credentials).
func (c Config) DSN() string {
	return embedded.PersistentOptions{Dir: c.Layout.PGDir(), Port: c.Ports.Postgres, Database: c.Database}.DSN()
}

// CacheDir is where the worker's cache mode stores every model answer (persistent, outside the repository).
func (c Config) CacheDir() string { return filepath.Join(c.Layout.Dir(), "cassettes") }

// CoreURL is where core listens.
func (c Config) CoreURL() string { return fmt.Sprintf("http://127.0.0.1:%d", c.Ports.Core) }

// WebURL is where the web app listens.
func (c Config) WebURL() string { return fmt.Sprintf("http://127.0.0.1:%d", c.Ports.Web) }

// Neo4jURI is the demo Bolt URI.
func (c Config) Neo4jURI() string { return fmt.Sprintf("bolt://127.0.0.1:%d", c.Ports.Bolt) }

// merged is .env overridden by the real environment, then the generated secrets for anything still missing.
func (c Config) merged() Env { return Merge(c.Secrets, c.DotEnv, c.Process) }

// Merged returns one effective variable (.env under the real environment under nothing else), for
// decisions like "is a model key set" that must not print the value.
func (c Config) Merged(name string) string { return c.merged()[name] }

// pick returns the entries of base whose name is listed or starts with one of the prefixes (a trailing *).
func pick(base Env, allow ...string) Env {
	out := Env{}
	for k, v := range base {
		for _, a := range allow {
			if k == a || (strings.HasSuffix(a, "*") && strings.HasPrefix(k, strings.TrimSuffix(a, "*"))) {
				out[k] = v
				break
			}
		}
	}
	return out
}

func orDefault(e Env, name, def string) {
	if e[name] == "" {
		e[name] = def
	}
}

// CoreEnv is the overlay for the core process. DATABASE_URL and NEO4J_* are forced to the demo stores whatever
// .env holds, so the demo can never write to a shared (Neon) database; the orchestrator is forced to
// on/play so only a Play-triggered run generates, and external writes are structurally off (dry_run).
func (c Config) CoreEnv() Env {
	base := c.merged()
	env := pick(base,
		"GHOST_*", "COALESCE_*", "WORKER_TIMEOUT_MS", "EXTRACT_DEADLINE_S", "PROVIDER_BREAKER_*")
	// Core never needs Slack tokens or model keys.
	for _, k := range []string{"GHOST_SLACK_AUDIT_FILE", "GHOST_LLM_MODE", "GHOST_MODEL", "GHOST_FALLBACK_MODEL", "GHOST_LLM_MAX_RPM"} {
		delete(env, k)
	}
	env["GHOST_API_TOKEN"] = base["GHOST_API_TOKEN"]
	env["GHOST_RUN_TOKEN_SECRET"] = base["GHOST_RUN_TOKEN_SECRET"]
	orDefault(env, "GHOST_PROVIDER_RPM", freeTierRPM)
	// The episode view (GET /replay/manifests/{id}/episodes, the web /replay page) fails closed without the knowledge
	// lifecycle rules, and core only defaults them for the orchestrator; .env.example sets the same path.
	orDefault(env, "GHOST_KNOWLEDGE_RULES", defaultKnowledgeRules)
	orDefault(env, "EXTRACT_DEADLINE_S", demoDeadlines["EXTRACT_DEADLINE_S"]) // core's WORKER_TIMEOUT_MS follows it
	// A run is resumed after a transient failure (a slow provider, a context pull) and every resume pulls its context again: the
	// production budget of 50 pulls and 5 attempts 30 s apart ran out within minutes ("run failed permanently: retry budget spent",
	// "core returned HTTP 429": the pull budget). The demo allows 15 attempts 120 s apart (30 minutes, past the judge deadline) and 600 pulls.
	orDefault(env, "GHOST_ORCHESTRATOR_RETRY_S", "120")
	orDefault(env, "GHOST_ORCHESTRATOR_MAX_ATTEMPTS", "15")
	orDefault(env, "GHOST_CTX_MAX_PULLS", "600")
	orDefault(env, "COALESCE_LEASE_MS", "600000") // core refuses a lease shorter than one worker call (5m30s with the deadline above)
	delete(env, "GHOST_TRANSITION_RULES")
	if rules := c.TransitionRules(); rules != "" {
		env["GHOST_TRANSITION_RULES"] = rules
	}
	for k, v := range map[string]string{
		"DATABASE_URL":                c.DSN(),
		"NEO4J_URI":                   c.Neo4jURI(),
		"NEO4J_USER":                  "neo4j",
		"NEO4J_PASSWORD":              c.Secrets["NEO4J_PASSWORD"],
		"NEO4J_DATABASE":              "neo4j",
		"CORE_ADDR":                   fmt.Sprintf("127.0.0.1:%d", c.Ports.Core),
		"WORKER_URL":                  fmt.Sprintf("http://127.0.0.1:%d", c.Ports.Worker),
		"GHOST_ORCHESTRATOR":          "on",
		"GHOST_ORCHESTRATOR_SCOPE":    "play",
		"GHOST_RUN_MODE":              "dry_run",
		"GHOST_ALLOW_EXTERNAL_WRITES": "false",
		"GHOST_REPLAY_EVENTS":         c.Layout.ReplayEventsDir(),
	} {
		env[k] = v
	}
	return env
}

// WorkerEnv is the overlay for the Python worker: model settings, pacing and the demo core URL, no stores.
func (c Config) WorkerEnv() Env {
	base := c.merged()
	env := pick(base, "OPENROUTER_API_KEY", "GHOST_MODEL", "GHOST_FALLBACK_MODEL", "GHOST_LLM_*", "LLM_*",
		"EXTRACT_DEADLINE_S", "JUDGE_DEADLINE_S", "PROVIDER_BREAKER_*", "MAX_CONCURRENCY")
	orDefault(env, "GHOST_LLM_MAX_RPM", freeTierRPM)
	for k, v := range demoDeadlines {
		orDefault(env, k, v)
	}
	env["GHOST_LLM_MODE"] = c.LLMMode
	if c.LLMMode == "cache" {
		// Replay-first, record-on-miss: every call is made once and stored under the demo home, outside the repository.
		env["GHOST_LLM_CACHE_DIR"] = c.CacheDir()
		// A judging round is 15 or more thinking-model calls of a minute each; made one at a time they outlast every deadline (the third
		// record attempt). Each call in flight holds a reserve against the spend cap. Replays make no call, so this only matters recording.
		orDefault(env, "GHOST_LLM_RECORD_CONCURRENCY", "6")
	}
	env["CORE_URL"] = c.CoreURL()
	return env
}

// SlackEnv is the overlay for the Slack bot, or empty with --no-slack. Without SLACK_ALLOWED_USER_IDS the demo
// falls back to SLACK_ALLOW_ALL_USERS=1 (a single-user demo workspace) and `demo up` says so.
func (c Config) SlackEnv() Env {
	if c.NoSlack {
		return Env{}
	}
	base := c.merged()
	env := pick(base, "SLACK_CHANNEL_ID", "SLACK_ALLOWED_USER_IDS", "SLACK_OUTBOX_POLL_MS")
	for _, k := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"} {
		env[k] = c.Slack[k]
	}
	env["CORE_URL"] = c.CoreURL()
	env["GHOST_API_TOKEN"] = base["GHOST_API_TOKEN"]
	env["GHOST_WEB_URL"] = c.WebURL()
	env["SLACK_OUTBOX"] = "on"
	env["GHOST_SLACK_AUDIT_FILE"] = filepath.Join(c.Layout.LogDir(), "slack-audit.jsonl")
	if env["SLACK_ALLOWED_USER_IDS"] == "" {
		delete(env, "SLACK_ALLOWED_USER_IDS")
		env["SLACK_ALLOW_ALL_USERS"] = "1"
	}
	return env
}

// WebEnv is the overlay for `npm run dev`: core's URL and the API token, nothing else.
func (c Config) WebEnv() Env {
	base := c.merged()
	env := pick(base, "GHOST_API_TOKEN")
	env["CORE_URL"] = c.CoreURL()
	env["GHOST_REPLAY_EVENTS"] = c.Layout.ReplayEventsDir()
	env["NEXT_TELEMETRY_DISABLED"] = "1"
	for k, v := range c.WebExtraEnv {
		env[k] = v
	}
	return env
}

// ToolEnv is the overlay for in-process ghostctl work (freeze, graph rebuild): the demo stores only.
func (c Config) ToolEnv() Env {
	env := pick(c.merged(), "GHOST_KNOWLEDGE_RULES")
	if rules := c.TransitionRules(); rules != "" {
		env["GHOST_TRANSITION_RULES"] = rules
	}
	env["DATABASE_URL"] = c.DSN()
	env["NEO4J_URI"] = c.Neo4jURI()
	env["NEO4J_USER"] = "neo4j"
	env["NEO4J_PASSWORD"] = c.Secrets["NEO4J_PASSWORD"]
	env["NEO4J_DATABASE"] = "neo4j"
	return env
}
