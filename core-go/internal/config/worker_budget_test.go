package config

import (
	"strings"
	"testing"
	"time"
)

func loadWith(t *testing.T, kv map[string]string) Config {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	for k, v := range kv {
		t.Setenv(k, v)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestWorkerTimeoutDefaultsToTheWorkerDeadlinePlusMargin(t *testing.T) {
	cfg := loadWith(t, nil)
	if cfg.WorkerExtractDeadline != 180*time.Second || cfg.WorkerTimeout != 210*time.Second {
		t.Fatalf("deadline %v timeout %v, want 180s and 210s", cfg.WorkerExtractDeadline, cfg.WorkerTimeout)
	}
	cfg = loadWith(t, map[string]string{"EXTRACT_DEADLINE_S": "100"})
	if cfg.WorkerTimeout != 130*time.Second {
		t.Fatalf("default timeout must follow the deadline: %v", cfg.WorkerTimeout)
	}
}

func TestWorkerTimeoutAndDeadlineAreReadFromTheEnvironment(t *testing.T) {
	cfg := loadWith(t, map[string]string{"EXTRACT_DEADLINE_S": "60", "WORKER_TIMEOUT_MS": "75000"})
	if cfg.WorkerExtractDeadline != time.Minute || cfg.WorkerTimeout != 75*time.Second {
		t.Fatalf("got %v %v", cfg.WorkerExtractDeadline, cfg.WorkerTimeout)
	}
}

func TestBadWorkerBudgetNumbersAreRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("EXTRACT_DEADLINE_S", "never")
	if _, err := Load(); err == nil {
		t.Fatal("non-numeric deadline accepted")
	}
	t.Setenv("EXTRACT_DEADLINE_S", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero deadline accepted")
	}
	t.Setenv("EXTRACT_DEADLINE_S", "180")
	t.Setenv("WORKER_TIMEOUT_MS", "-5")
	if _, err := Load(); err == nil {
		t.Fatal("negative timeout accepted")
	}
}

func TestValidateWorkerBudget(t *testing.T) {
	base := Config{WorkerURL: "http://w", WorkerExtractDeadline: 180 * time.Second, WorkerTimeout: 210 * time.Second}
	cases := []struct {
		name    string
		mutate  func(*Config)
		lease   time.Duration
		wantErr string
	}{
		{"defaults are fine", func(*Config) {}, 5 * time.Minute, ""},
		{"timeout equal to the deadline", func(c *Config) { c.WorkerTimeout = 180 * time.Second }, 5 * time.Minute, "must exceed"},
		{"timeout below the deadline", func(c *Config) { c.WorkerTimeout = 120 * time.Second }, 5 * time.Minute, "must exceed"},
		{"lease shorter than one call", func(*Config) {}, 200 * time.Second, "COALESCE_LEASE_MS"},
		{"lease equal to one call", func(*Config) {}, 210 * time.Second, ""},
		{"zero values mean the defaults", func(c *Config) { c.WorkerExtractDeadline = 0; c.WorkerTimeout = 0 }, 5 * time.Minute, ""},
		{"no worker, nothing to check", func(c *Config) { c.WorkerURL = ""; c.WorkerTimeout = 0 }, time.Second, ""},
	}
	for _, tc := range cases {
		cfg := base
		tc.mutate(&cfg)
		err := cfg.ValidateWorkerBudget(tc.lease)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: error %v, want containing %q", tc.name, err, tc.wantErr)
		}
	}
}
