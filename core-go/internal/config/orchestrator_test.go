package config

import (
	"strings"
	"testing"
	"time"
)

func TestTheOrchestratorIsOffByDefaultWithOneRunAtATime(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GHOST_ORCHESTRATOR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	o := cfg.Orchestrator
	if o.Enabled || o.Concurrency != 1 || o.Poll != 2*time.Second || o.Retry != 30*time.Second ||
		o.MaxAttempts != DefaultOrchestratorMaxAttempts ||
		o.Scope != ScopePlay || o.WorkspaceID != DefaultWorkspaceID || o.KnowledgeRulesPath != DefaultKnowledgeRulesPath || o.RoutingPath != DefaultRoutingPath {
		t.Fatalf("defaults: %+v", o)
	}
}

func TestTheOrchestratorSettingsAreReadFromTheEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	for k, v := range map[string]string{
		"GHOST_ORCHESTRATOR": " ON ", "GHOST_ORCHESTRATOR_CONCURRENCY": "3", "GHOST_ORCHESTRATOR_POLL_MS": "250",
		"GHOST_ORCHESTRATOR_RETRY_S": "1.5", "GHOST_ORCHESTRATOR_MAX_ATTEMPTS": "7", "GHOST_ORCHESTRATOR_SCOPE": " All ", "GHOST_WORKSPACE_ID": "acme", "GHOST_KNOWLEDGE_RULES": "k.json", "GHOST_TRANSITION_ROUTING": "r.json",
	} {
		t.Setenv(k, v)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Orchestrator{Enabled: true, Scope: ScopeAll, Concurrency: 3, Poll: 250 * time.Millisecond, Retry: 1500 * time.Millisecond,
		MaxAttempts: 7, WorkspaceID: "acme", KnowledgeRulesPath: "k.json", RoutingPath: "r.json"}
	if cfg.Orchestrator != want {
		t.Fatalf("got %+v, want %+v", cfg.Orchestrator, want)
	}
}

func TestBadOrchestratorValuesAreRejectedByName(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	for _, kv := range [][2]string{
		{"GHOST_ORCHESTRATOR", "yes"}, {"GHOST_ORCHESTRATOR", "true"}, {"GHOST_ORCHESTRATOR_CONCURRENCY", "0"},
		{"GHOST_ORCHESTRATOR_CONCURRENCY", "many"}, {"GHOST_ORCHESTRATOR_POLL_MS", "0"}, {"GHOST_ORCHESTRATOR_POLL_MS", "-5"},
		{"GHOST_ORCHESTRATOR_RETRY_S", "0"}, {"GHOST_ORCHESTRATOR_MAX_ATTEMPTS", "0"}, {"GHOST_ORCHESTRATOR_MAX_ATTEMPTS", "many"},
		{"GHOST_ORCHESTRATOR_SCOPE", "everything"}, {"GHOST_ORCHESTRATOR_SCOPE", "old"}, {"GHOST_ORCHESTRATOR_RETRY_S", "soon"},
	} {
		t.Setenv(kv[0], kv[1])
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), kv[0]) {
			t.Errorf("%s=%s: err = %v, want a refusal naming the variable", kv[0], kv[1], err)
		}
		t.Setenv(kv[0], "")
	}
}

func TestAnEnabledOrchestratorNeedsAWorkerURL(t *testing.T) {
	on := Config{Orchestrator: Orchestrator{Enabled: true, Concurrency: 1}, WorkerURL: "http://w"}
	if err := on.ValidateForOrchestrator(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	noWorker := on
	noWorker.WorkerURL = ""
	if err := noWorker.ValidateForOrchestrator(); err == nil || !strings.Contains(err.Error(), "WORKER_URL") {
		t.Fatalf("no worker: %v", err)
	}
	zero := on
	zero.Orchestrator.Concurrency = 0
	if err := zero.ValidateForOrchestrator(); err == nil {
		t.Fatal("zero concurrency accepted")
	}
	if err := (Config{}).ValidateForOrchestrator(); err != nil {
		t.Fatalf("a disabled orchestrator needs nothing: %v", err)
	}
}
