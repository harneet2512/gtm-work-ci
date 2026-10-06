package config

import (
	"fmt"
	"strings"
	"time"
)

// Defaults of the run orchestrator driver (HAR-117 wiring).
const (
	DefaultOrchestratorConcurrency = 1
	DefaultOrchestratorPoll        = 2 * time.Second
	DefaultOrchestratorRetry       = 30 * time.Second
	// DefaultOrchestratorMaxAttempts is how many times one run may be handed to the orchestrator before the
	// driver fails it permanently (GHOST_ORCHESTRATOR_MAX_ATTEMPTS).
	DefaultOrchestratorMaxAttempts = 5
	DefaultWorkspaceID             = "ghost-demo"
	// ScopePlay and ScopeAll are the values of GHOST_ORCHESTRATOR_SCOPE.
	ScopePlay = "play"
	ScopeAll  = "all"
	// Paths are relative to the working directory, like GHOST_TRANSITION_RULES: core runs from the repository root.
	DefaultKnowledgeRulesPath = "contracts/knowledge/lifecycle.v1.json"
	DefaultRoutingPath        = "contracts/transitions/routing.v1.json"
)

// Orchestrator is the settings of the background driver that takes an opened AgentRun to a published
// StrategySet. It is off unless GHOST_ORCHESTRATOR=on: tests and plain `go run` never call a model by accident,
// and the demo configuration (.env.example) turns it on.
type Orchestrator struct {
	// Enabled is GHOST_ORCHESTRATOR=on|off (default off).
	Enabled bool
	// Scope is GHOST_ORCHESTRATOR_SCOPE: "play" (default) drives only the Play's run and runs it already attempted
	// (crash resume); "all" drives every open dry-run run, including the placeholder run of each imported account.
	Scope string
	// Concurrency is GHOST_ORCHESTRATOR_CONCURRENCY: runs driven at once, always for different accounts (default 1).
	Concurrency int
	// Poll is GHOST_ORCHESTRATOR_POLL_MS: how often the driver looks for open runs (default 2000).
	Poll time.Duration
	// Retry is GHOST_ORCHESTRATOR_RETRY_S: how long a run that failed transiently waits before it is resumed (default 30).
	Retry time.Duration
	// MaxAttempts is GHOST_ORCHESTRATOR_MAX_ATTEMPTS: how many times one run may be handed to the orchestrator
	// before the driver fails it permanently (default 5). It caps a provider that keeps refusing.
	MaxAttempts int
	// WorkspaceID is GHOST_WORKSPACE_ID, the workspace the deterministic evals' policy applies to.
	WorkspaceID string
	// KnowledgeRulesPath is GHOST_KNOWLEDGE_RULES (the knowledge lifecycle, contracts/knowledge/lifecycle.v1.json).
	KnowledgeRulesPath string
	// RoutingPath is GHOST_TRANSITION_ROUTING (the eval-suite router, contracts/transitions/routing.v1.json).
	RoutingPath string
}

func loadOrchestrator(env map[string]string) (Orchestrator, error) {
	o := Orchestrator{
		WorkspaceID:        withDefault(env["GHOST_WORKSPACE_ID"], DefaultWorkspaceID),
		KnowledgeRulesPath: withDefault(env["GHOST_KNOWLEDGE_RULES"], DefaultKnowledgeRulesPath),
		RoutingPath:        withDefault(env["GHOST_TRANSITION_ROUTING"], DefaultRoutingPath),
	}
	switch v := strings.ToLower(strings.TrimSpace(env["GHOST_ORCHESTRATOR"])); v {
	case "", "off":
	case "on":
		o.Enabled = true
	default:
		return Orchestrator{}, fmt.Errorf("config: GHOST_ORCHESTRATOR must be on or off, got %q", env["GHOST_ORCHESTRATOR"])
	}
	switch v := strings.ToLower(strings.TrimSpace(env["GHOST_ORCHESTRATOR_SCOPE"])); v {
	case "", ScopePlay:
		o.Scope = ScopePlay
	case ScopeAll:
		o.Scope = ScopeAll
	default:
		return Orchestrator{}, fmt.Errorf("config: GHOST_ORCHESTRATOR_SCOPE must be play or all, got %q", env["GHOST_ORCHESTRATOR_SCOPE"])
	}
	var err error
	if o.Concurrency, err = positiveInt(env, "GHOST_ORCHESTRATOR_CONCURRENCY", DefaultOrchestratorConcurrency); err != nil {
		return Orchestrator{}, err
	}
	poll, err := millis(env, "GHOST_ORCHESTRATOR_POLL_MS", int(DefaultOrchestratorPoll/time.Millisecond))
	if err != nil {
		return Orchestrator{}, err
	}
	if poll == 0 {
		return Orchestrator{}, fmt.Errorf("config: GHOST_ORCHESTRATOR_POLL_MS must be positive")
	}
	o.Poll = poll
	if o.Retry, err = seconds(env, "GHOST_ORCHESTRATOR_RETRY_S", DefaultOrchestratorRetry); err != nil {
		return Orchestrator{}, err
	}
	if o.MaxAttempts, err = positiveInt(env, "GHOST_ORCHESTRATOR_MAX_ATTEMPTS", DefaultOrchestratorMaxAttempts); err != nil {
		return Orchestrator{}, err
	}
	return o, nil
}

// ValidateForOrchestrator checks what only an enabled driver needs: a worker to call. The rule files are loaded
// (and so verified) when the service is built.
func (c Config) ValidateForOrchestrator() error {
	if !c.Orchestrator.Enabled {
		return nil
	}
	if c.WorkerURL == "" {
		return fmt.Errorf("config: GHOST_ORCHESTRATOR=on needs WORKER_URL (the orchestrator generates through the model worker)")
	}
	if c.Orchestrator.Concurrency < 1 {
		return fmt.Errorf("config: GHOST_ORCHESTRATOR_CONCURRENCY must be at least 1")
	}
	return nil
}
