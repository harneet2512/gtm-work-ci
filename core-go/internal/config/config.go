// Package config loads core-service settings from the environment, falling back to a
// .env file found in the working directory or a parent. Secrets never have defaults.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the validated core-service configuration.
type Config struct {
	DatabaseURL      string
	CoreAddr         string
	WorkerURL        string
	CoalesceDebounce time.Duration
	CoalesceMaxWait  time.Duration
	// CoalesceLease is how long a claimed recompute job is held exclusively (0 selects the coalesce
	// default of 5 minutes). The job renews it before every model call (HAR-135), so it only has to
	// outlast one worker call (WORKER_TIMEOUT_MS), not the account's whole extraction.
	CoalesceLease time.Duration
	// ProviderBreakerThreshold is how many consecutive provider failures open the circuit breaker that pauses
	// every model call (PROVIDER_BREAKER_THRESHOLD, default 5; HAR-135).
	ProviderBreakerThreshold int
	// ProviderBreakerCooldown is how long it stays open before one probe call is allowed
	// (PROVIDER_BREAKER_COOLDOWN_S, default 300 s). `ghostctl breaker reset` closes it sooner.
	ProviderBreakerCooldown time.Duration
	// ProviderRPM is GHOST_PROVIDER_RPM: the ceiling on core's model-worker calls per minute (a token bucket
	// in providerbreaker.WorkerGuard in front of the orchestrator's strategies/judge/revise calls). 0 is
	// unlimited. Set it to the provider key's ceiling so a run does not spend its retry budget racing it.
	ProviderRPM int
	// WorkerExtractDeadline mirrors the worker's EXTRACT_DEADLINE_S (the same variable): the longest the
	// worker keeps one /v1/extract call alive before it answers or fails.
	WorkerExtractDeadline time.Duration
	// WorkerTimeout is the core's per-call HTTP timeout to the worker (WORKER_TIMEOUT_MS). It must outlast
	// the worker's deadline, or a slow call is abandoned by core while the worker still works on (and pays for) it.
	WorkerTimeout       time.Duration
	RunMode             string // "dry_run" or "live"
	AllowExternalWrites bool
	// APIToken is the bearer token guarding POST /ingest (GHOST_API_TOKEN). It is optional for
	// commands that do not serve HTTP; use ValidateForServe before starting the server.
	APIToken string
	// TransitionRulesPath is the transition rule set file (GHOST_TRANSITION_RULES, ADR-0012). Empty turns the
	// transition detector off; the core logs that at startup.
	TransitionRulesPath string
	// ReplayEventsPath is the replay dataset Play releases the held-out event from: a SourceEvent JSON file, or a
	// directory of them (GHOST_REPLAY_EVENTS, HAR-124). Empty leaves Play answering 503 replay_source_unavailable.
	ReplayEventsPath string
	// ReplayHistoricalEnd splits an episode replay into the historical-learning window 1..h and the held-out
	// live window h+1..N (GHOST_REPLAY_HISTORICAL_END, HAR-129 §B). Nil means "all of history": h = N-1.
	ReplayHistoricalEnd *int
	// KnowledgeRulesPath is the knowledge lifecycle rules file the episode view's knowledge-as-of read replays
	// (GHOST_KNOWLEDGE_RULES, HAR-117/HAR-129). Empty leaves the view answering 503 knowledge_unavailable —
	// it fails closed rather than answer with knowledge it cannot age.
	KnowledgeRulesPath string
	// RunTokenSecret signs the run-scoped tokens of GET /internal/ctx/{tool}
	// (GHOST_RUN_TOKEN_SECRET, at least MinRunTokenSecretLen characters). Optional: when empty the
	// signing key is derived, domain-separated, from APIToken.
	RunTokenSecret string
	// Orchestrator drives opened runs to a published StrategySet in the background (GHOST_ORCHESTRATOR*).
	Orchestrator Orchestrator
}

// MinRunTokenSecretLen is the shortest accepted GHOST_RUN_TOKEN_SECRET.
const MinRunTokenSecretLen = 32

// minDistinctChars is the fewest different characters a token may be made of: it rejects "aaaa..."
// and "abababab..." while accepting any random hex or base64 token.
const minDistinctChars = 8

// hasEntropy is a cheap floor against guessable secrets, not a strength estimate.
func hasEntropy(secret string) bool {
	seen := map[rune]bool{}
	for _, r := range secret {
		seen[r] = true
	}
	return len(seen) >= minDistinctChars
}

// ValidateForServe checks the settings that only the HTTP server needs.
func (c Config) ValidateForServe() error {
	if c.APIToken == "" {
		return errors.New("config: GHOST_API_TOKEN is required to serve (see .env.example)")
	}
	if !hasEntropy(c.APIToken) {
		return fmt.Errorf("config: GHOST_API_TOKEN is too repetitive; use a random token (openssl rand -hex 32)")
	}
	if c.RunMode == "live" && c.RunTokenSecret == "" {
		return errors.New("config: GHOST_RUN_TOKEN_SECRET is required when GHOST_RUN_MODE=live")
	}
	// Without its own secret the run-token key derives from the API token, and an agent that holds
	// a run token could guess a weak API token offline.
	if c.RunTokenSecret == "" && len(c.APIToken) < MinRunTokenSecretLen {
		return fmt.Errorf("config: GHOST_API_TOKEN must be at least %d characters unless GHOST_RUN_TOKEN_SECRET is set", MinRunTokenSecretLen)
	}
	return nil
}

// Defaults of the worker call budget. The worker deadline is worker-py settings.py extract_deadline_s.
const (
	DefaultWorkerExtractDeadline = 180 * time.Second
	// WorkerTimeoutMargin is how far past the worker's deadline the default core timeout sits.
	WorkerTimeoutMargin = 30 * time.Second
)

// ValidateWorkerBudget refuses a worker call budget that cannot work: a core timeout that does not
// outlast the worker's own deadline, or a job lease shorter than one call's timeout (the lease would
// expire inside a single call). lease is the effective lease (the coalesce default when unset). It
// is a no-op without a worker URL, because core then runs the rule extractors only.
func (c Config) ValidateWorkerBudget(lease time.Duration) error {
	if c.WorkerURL == "" {
		return nil
	}
	deadline, timeout := c.EffectiveWorkerBudget()
	if timeout <= deadline {
		return fmt.Errorf("config: WORKER_TIMEOUT_MS (%s) must exceed the worker's extract deadline EXTRACT_DEADLINE_S (%s)",
			timeout, deadline)
	}
	if lease < timeout {
		return fmt.Errorf("config: COALESCE_LEASE_MS (%s) must be at least one worker call timeout WORKER_TIMEOUT_MS (%s)",
			lease, timeout)
	}
	return nil
}

// EffectiveWorkerBudget returns the worker deadline and the core call timeout, with the defaults for
// values a hand-built Config left at zero (Load always sets both).
func (c Config) EffectiveWorkerBudget() (deadline, timeout time.Duration) {
	deadline, timeout = c.WorkerExtractDeadline, c.WorkerTimeout
	if deadline <= 0 {
		deadline = DefaultWorkerExtractDeadline
	}
	if timeout <= 0 {
		timeout = deadline + WorkerTimeoutMargin
	}
	return deadline, timeout
}

// Load reads configuration and validates it.
func Load() (Config, error) {
	env := mergedEnv()
	debounce, err := millis(env, "COALESCE_DEBOUNCE_MS", 3000)
	if err != nil {
		return Config{}, err
	}
	maxWait, err := millis(env, "COALESCE_MAX_WAIT_MS", 15000)
	if err != nil {
		return Config{}, err
	}
	lease, err := millis(env, "COALESCE_LEASE_MS", 0)
	if err != nil {
		return Config{}, err
	}
	deadline, err := seconds(env, "EXTRACT_DEADLINE_S", DefaultWorkerExtractDeadline)
	if err != nil {
		return Config{}, err
	}
	workerTimeout, err := millis(env, "WORKER_TIMEOUT_MS", int((deadline+WorkerTimeoutMargin)/time.Millisecond))
	if err != nil {
		return Config{}, err
	}
	cooldown, err := seconds(env, "PROVIDER_BREAKER_COOLDOWN_S", DefaultProviderBreakerCooldown)
	if err != nil {
		return Config{}, err
	}
	threshold, err := positiveInt(env, "PROVIDER_BREAKER_THRESHOLD", DefaultProviderBreakerThreshold)
	if err != nil {
		return Config{}, err
	}
	historicalEnd, err := optionalInt(env, "GHOST_REPLAY_HISTORICAL_END")
	if err != nil {
		return Config{}, err
	}
	rpm, err := nonNegativeInt(env, "GHOST_PROVIDER_RPM", 0)
	if err != nil {
		return Config{}, err
	}
	orch, err := loadOrchestrator(env)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Orchestrator:             orch,
		ProviderBreakerThreshold: threshold, ProviderBreakerCooldown: cooldown,
		ProviderRPM:           rpm,
		CoalesceLease:         lease,
		WorkerExtractDeadline: deadline,
		WorkerTimeout:         workerTimeout,
		DatabaseURL:           env["DATABASE_URL"],
		CoreAddr:              withDefault(env["CORE_ADDR"], "127.0.0.1:8080"),
		WorkerURL:             withDefault(env["WORKER_URL"], "http://127.0.0.1:8090"),
		CoalesceDebounce:      debounce,
		CoalesceMaxWait:       maxWait,
		RunMode:               withDefault(env["GHOST_RUN_MODE"], "dry_run"),
		AllowExternalWrites:   env["GHOST_ALLOW_EXTERNAL_WRITES"] == "true",
		APIToken:              env["GHOST_API_TOKEN"],
		RunTokenSecret:        env["GHOST_RUN_TOKEN_SECRET"],
		TransitionRulesPath:   env["GHOST_TRANSITION_RULES"],
		ReplayEventsPath:      env["GHOST_REPLAY_EVENTS"],
		ReplayHistoricalEnd:   historicalEnd,
		KnowledgeRulesPath:    env["GHOST_KNOWLEDGE_RULES"],
	}
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if c.DatabaseURL == "" {
		return errors.New("config: DATABASE_URL is required (see .env.example)")
	}
	if c.RunMode != "dry_run" && c.RunMode != "live" {
		return fmt.Errorf("config: GHOST_RUN_MODE must be dry_run or live, got %q", c.RunMode)
	}
	if c.RunTokenSecret != "" && (len(c.RunTokenSecret) < MinRunTokenSecretLen || !hasEntropy(c.RunTokenSecret)) {
		return fmt.Errorf("config: GHOST_RUN_TOKEN_SECRET must be at least %d random characters", MinRunTokenSecretLen)
	}
	if c.CoalesceMaxWait < c.CoalesceDebounce {
		return errors.New("config: COALESCE_MAX_WAIT_MS must be >= COALESCE_DEBOUNCE_MS")
	}
	return nil
}

// String redacts credentials so a Config can be logged safely.
func (c Config) String() string {
	return fmt.Sprintf("Config{DatabaseURL:%s CoreAddr:%s WorkerURL:%s RunMode:%s AllowExternalWrites:%v}",
		redactURL(c.DatabaseURL), c.CoreAddr, c.WorkerURL, c.RunMode, c.AllowExternalWrites)
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User(u.User.Username())
	return u.String()
}

// ExternalWritesEnabled is true only when both the live run mode and the explicit
// write switch are set. Everything else is structurally dry-run.
func (c Config) ExternalWritesEnabled() bool {
	return c.RunMode == "live" && c.AllowExternalWrites
}

func withDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func seconds(env map[string]string, key string, def time.Duration) (time.Duration, error) {
	raw := env[key]
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("config: %s must be a positive number of seconds, got %q", key, raw)
	}
	return time.Duration(n * float64(time.Second)), nil
}

// Defaults of the provider circuit breaker (HAR-135).
const (
	DefaultProviderBreakerThreshold = 5
	DefaultProviderBreakerCooldown  = 5 * time.Minute
)

func positiveInt(env map[string]string, key string, def int) (int, error) {
	raw := env[key]
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("config: %s must be a positive integer, got %q", key, raw)
	}
	return n, nil
}

// optionalInt parses a non-negative integer env var that defaults to unset (nil).
func optionalInt(env map[string]string, key string) (*int, error) {
	raw := env[key]
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("config: %s must be a non-negative integer, got %q", key, raw)
	}
	return &n, nil
}

// nonNegativeInt parses a count that may legitimately be zero (0 means "no limit").
func nonNegativeInt(env map[string]string, key string, def int) (int, error) {
	raw := env[key]
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("config: %s must be a non-negative integer, got %q", key, raw)
	}
	return n, nil
}

func millis(env map[string]string, key string, def int) (time.Duration, error) {
	raw := env[key]
	if raw == "" {
		return time.Duration(def) * time.Millisecond, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("config: %s must be a non-negative integer, got %q", key, raw)
	}
	return time.Duration(n) * time.Millisecond, nil
}

// mergedEnv returns .env values overridden by real environment variables.
func mergedEnv() map[string]string {
	out := map[string]string{}
	if path, ok := findDotEnv(); ok {
		for k, v := range parseDotEnv(path) {
			out[k] = v
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && v != "" {
			out[k] = v
		}
	}
	return out
}

func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		p := filepath.Join(dir, ".env")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
		// Never search above the repository root.
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func parseDotEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}
