package config

import (
	"strings"
	"testing"
	"time"
)

func TestProviderBreakerDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil || cfg.ProviderBreakerThreshold != 5 || cfg.ProviderBreakerCooldown != 5*time.Minute {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("PROVIDER_BREAKER_THRESHOLD", "9")
	t.Setenv("PROVIDER_BREAKER_COOLDOWN_S", "42.5")
	cfg, err = Load()
	if err != nil || cfg.ProviderBreakerThreshold != 9 || cfg.ProviderBreakerCooldown != 42500*time.Millisecond {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
}

func TestBadProviderBreakerValuesAreRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	for _, kv := range [][2]string{{"PROVIDER_BREAKER_THRESHOLD", "0"}, {"PROVIDER_BREAKER_THRESHOLD", "many"}, {"PROVIDER_BREAKER_COOLDOWN_S", "-1"}} {
		t.Setenv(kv[0], kv[1])
		if _, err := Load(); err == nil {
			t.Errorf("%s=%s must be rejected", kv[0], kv[1])
		}
		t.Setenv(kv[0], "")
	}
}

func TestProviderRPMDefaultsToUnlimitedAndIsReadFromTheEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	cfg, err := Load()
	if err != nil || cfg.ProviderRPM != 0 {
		t.Fatalf("default: rpm=%d err=%v, want 0 (unlimited)", cfg.ProviderRPM, err)
	}
	t.Setenv("GHOST_PROVIDER_RPM", "5")
	cfg, err = Load()
	if err != nil || cfg.ProviderRPM != 5 {
		t.Fatalf("override: rpm=%d err=%v, want 5", cfg.ProviderRPM, err)
	}
	for _, bad := range []string{"-1", "five"} {
		t.Setenv("GHOST_PROVIDER_RPM", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "GHOST_PROVIDER_RPM") {
			t.Errorf("GHOST_PROVIDER_RPM=%s: err=%v, want a refusal naming the variable", bad, err)
		}
	}
}
