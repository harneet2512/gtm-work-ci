package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternalWritesRequireLiveModeAndExplicitSwitch(t *testing.T) {
	cases := []struct {
		mode  string
		allow bool
		want  bool
	}{
		{"dry_run", false, false},
		{"dry_run", true, false},
		{"live", false, false},
		{"live", true, true},
	}
	for _, tc := range cases {
		c := Config{RunMode: tc.mode, AllowExternalWrites: tc.allow}
		if got := c.ExternalWritesEnabled(); got != tc.want {
			t.Errorf("mode=%s allow=%v: got %v want %v", tc.mode, tc.allow, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	base := Config{DatabaseURL: "postgres://x", RunMode: "dry_run", CoalesceDebounce: time.Second, CoalesceMaxWait: 2 * time.Second}
	if err := base.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	noDB := base
	noDB.DatabaseURL = ""
	if noDB.validate() == nil {
		t.Error("missing DATABASE_URL accepted")
	}
	badMode := base
	badMode.RunMode = "yolo"
	if badMode.validate() == nil {
		t.Error("invalid run mode accepted")
	}
	badWindow := base
	badWindow.CoalesceMaxWait = 0
	if badWindow.validate() == nil {
		t.Error("max wait below debounce accepted")
	}
}

func TestMillisParsing(t *testing.T) {
	d, err := millis(map[string]string{"X": "250"}, "X", 1)
	if err != nil || d != 250*time.Millisecond {
		t.Fatalf("got %v %v", d, err)
	}
	if _, err := millis(map[string]string{"X": "-1"}, "X", 1); err == nil {
		t.Fatal("negative accepted")
	}
	d, _ = millis(map[string]string{}, "X", 7)
	if d != 7*time.Millisecond {
		t.Fatalf("default not applied: %v", d)
	}
}

func TestLoadReadsDotEnvAndEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	content := "# comment\nDATABASE_URL=\"postgres://from-file\"\nCOALESCE_DEBOUNCE_MS=100\nCOALESCE_MAX_WAIT_MS=500\nnot-a-pair\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("GHOST_RUN_MODE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://from-file" || cfg.CoalesceDebounce != 100*time.Millisecond || cfg.RunMode != "dry_run" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	t.Setenv("DATABASE_URL", "postgres://from-env")
	cfg, err = Load()
	if err != nil || cfg.DatabaseURL != "postgres://from-env" {
		t.Fatalf("env override failed: %+v %v", cfg, err)
	}
}

func TestLoadReadsTheReplayDatasetPath(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_REPLAY_EVENTS", "")
	if cfg, err := Load(); err != nil || cfg.ReplayEventsPath != "" {
		t.Fatalf("unset: %+v %v; Play must stay off without a dataset", cfg, err)
	}
	t.Setenv("GHOST_REPLAY_EVENTS", "fixtures/replay")
	if cfg, err := Load(); err != nil || cfg.ReplayEventsPath != "fixtures/replay" {
		t.Fatalf("set: %+v %v", cfg, err)
	}
}

func TestLoadReadsTheEpisodeReplaySettings(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_REPLAY_HISTORICAL_END", "")
	t.Setenv("GHOST_KNOWLEDGE_RULES", "")
	cfg, err := Load()
	if err != nil || cfg.ReplayHistoricalEnd != nil || cfg.KnowledgeRulesPath != "" {
		t.Fatalf("unset: %+v %v", cfg, err)
	}
	t.Setenv("GHOST_REPLAY_HISTORICAL_END", "7")
	t.Setenv("GHOST_KNOWLEDGE_RULES", "contracts/knowledge/lifecycle.v1.json")
	cfg, err = Load()
	if err != nil || cfg.ReplayHistoricalEnd == nil || *cfg.ReplayHistoricalEnd != 7 ||
		cfg.KnowledgeRulesPath != "contracts/knowledge/lifecycle.v1.json" {
		t.Fatalf("set: %+v %v", cfg, err)
	}
	t.Setenv("GHOST_REPLAY_HISTORICAL_END", "-2")
	if _, err := Load(); err == nil {
		t.Fatal("a negative historical end was accepted")
	}
}

func TestStringRedactsPassword(t *testing.T) {
	c := Config{DatabaseURL: "postgres://ghost:hunter2@ep-x.neon.tech/ghost", RunMode: "dry_run"}
	if s := c.String(); strings.Contains(s, "hunter2") || !strings.Contains(s, "ghost@ep-x.neon.tech") {
		t.Fatalf("String() = %s", s)
	}
}

func TestLoadRejectsBadNumbers(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("COALESCE_DEBOUNCE_MS", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("non-numeric debounce accepted")
	}
}

func TestAPITokenIsOptionalForLoadButRequiredToServe(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_API_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load without token must succeed (ghostctl does not serve): %v", err)
	}
	if cfg.ValidateForServe() == nil {
		t.Fatal("serving without GHOST_API_TOKEN accepted")
	}

	strong := "s3cret-token-" + strings.Repeat("x", 24)
	t.Setenv("GHOST_API_TOKEN", strong)
	cfg, err = Load()
	if err != nil || cfg.APIToken != strong {
		t.Fatalf("token not loaded: %+v %v", cfg, err)
	}
	if err := cfg.ValidateForServe(); err != nil {
		t.Fatalf("valid serve config rejected: %v", err)
	}
}

func TestCoalesceLeaseDefaultsToZeroAndReadsMilliseconds(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil || cfg.CoalesceLease != 0 {
		t.Fatalf("default lease must be 0 (the coalesce default applies): %v %v", cfg.CoalesceLease, err)
	}
	t.Setenv("COALESCE_LEASE_MS", "1800000")
	cfg, err = Load()
	if err != nil || cfg.CoalesceLease != 30*time.Minute {
		t.Fatalf("lease not loaded: %v %v", cfg.CoalesceLease, err)
	}
}

func TestLiveModeRequiresARunTokenSecretAndTokensNeedEntropy(t *testing.T) {
	good := "s3cret-token-" + strings.Repeat("x", 24)
	live := Config{APIToken: good, RunMode: "live"}
	if live.ValidateForServe() == nil {
		t.Fatal("live mode without GHOST_RUN_TOKEN_SECRET was accepted")
	}
	live.RunTokenSecret = "run-secret-" + strings.Repeat("y", 24)
	if err := live.ValidateForServe(); err != nil {
		t.Fatalf("live mode with a secret was refused: %v", err)
	}
	dry := Config{APIToken: good, RunMode: "dry_run"}
	if err := dry.ValidateForServe(); err != nil {
		t.Fatalf("dry_run with a derived key was refused: %v", err)
	}
	for _, weak := range []string{strings.Repeat("a", 40), strings.Repeat("ab", 20), strings.Repeat("abc", 15)} {
		if (Config{APIToken: weak, RunTokenSecret: good}).ValidateForServe() == nil {
			t.Errorf("low-entropy API token %q accepted", weak)
		}
		if (Config{DatabaseURL: "postgres://x", RunMode: "dry_run", CoalesceMaxWait: 1, RunTokenSecret: weak}).validate() == nil {
			t.Errorf("low-entropy run token secret %q accepted", weak)
		}
	}
}

func TestAWeakAPITokenNeedsItsOwnRunTokenSecret(t *testing.T) {
	// The run-token key derives from the API token unless a secret is set; a guessable API token
	// would let an agent that holds a run token brute-force it offline.
	weak := Config{APIToken: "short-token"}
	if weak.ValidateForServe() == nil {
		t.Fatal("a short API token without GHOST_RUN_TOKEN_SECRET was accepted")
	}
	weak.RunTokenSecret = "run-secret-" + strings.Repeat("k", MinRunTokenSecretLen)
	if err := weak.ValidateForServe(); err != nil {
		t.Fatalf("a short API token with its own run token secret was refused: %v", err)
	}
}

func TestRunTokenSecretIsOptionalButMustBeLongEnough(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_API_TOKEN", "s3cret-token")

	t.Setenv("GHOST_RUN_TOKEN_SECRET", "")
	cfg, err := Load()
	if err != nil || cfg.RunTokenSecret != "" {
		t.Fatalf("unset secret must load (the key is then derived from the API token): %+v %v", cfg, err)
	}

	t.Setenv("GHOST_RUN_TOKEN_SECRET", "too-short")
	if _, err := Load(); err == nil {
		t.Fatal("a short GHOST_RUN_TOKEN_SECRET was accepted")
	}

	long := "run-secret-" + strings.Repeat("k", MinRunTokenSecretLen)
	t.Setenv("GHOST_RUN_TOKEN_SECRET", long)
	cfg, err = Load()
	if err != nil || cfg.RunTokenSecret != long {
		t.Fatalf("a long secret was not loaded: %v", err)
	}
	if strings.Contains(cfg.String(), long) {
		t.Fatalf("String() leaks the run token secret: %s", cfg.String())
	}
}

func TestStringNeverIncludesAPIToken(t *testing.T) {
	c := Config{DatabaseURL: "postgres://x", RunMode: "dry_run", APIToken: "s3cret-token"}
	if strings.Contains(c.String(), "s3cret-token") {
		t.Fatalf("String() leaks the API token: %s", c.String())
	}
}

// GHOST_TRANSITION_RULES=off is "detector disabled" for core itself, however the value reaches it (.env or process env),
// case-insensitively and trimmed; a real path and an unset value are left as they are.
func TestTransitionRulesOffDisablesTheDetectorWithoutAnError(t *testing.T) {
	for name, tc := range map[string]struct{ value, want string }{
		"off":        {"off", ""},
		"upper":      {"OFF", ""},
		"padded":     {"  Off ", ""},
		"unset":      {"", ""},
		"user path":  {"custom/transitions.json", "custom/transitions.json"},
		"path named": {"office/rules.json", "office/rules.json"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("DATABASE_URL", "postgres://x")
			t.Setenv("GHOST_TRANSITION_RULES", tc.value)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.TransitionRulesPath != tc.want {
				t.Fatalf("TransitionRulesPath = %q, want %q", cfg.TransitionRulesPath, tc.want)
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL=postgres://x\nGHOST_TRANSITION_RULES=off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("GHOST_TRANSITION_RULES", "")
	cfg, err := Load()
	if err != nil || cfg.TransitionRulesPath != "" {
		t.Fatalf(".env off must disable the detector: %q %v", cfg.TransitionRulesPath, err)
	}
}
