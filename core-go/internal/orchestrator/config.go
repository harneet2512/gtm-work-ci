package orchestrator

import (
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/runtoken"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Defaults of Config.
const (
	DefaultJudgeConcurrency = 3
	DefaultMaxContextPulls  = 6
	// leaseMargin is how long past the token's life a draft-step claim is honoured before another caller may take
	// the run over (a crashed worker's claim expires; a live one finishes well inside the token).
	leaseMargin = 5 * time.Minute
	// maxGenerationAttempts: invalid worker output is retried once, then the run fails.
	maxGenerationAttempts = 2
)

// Config is the orchestrator's policy and the workspace facts the deterministic evals read (ADR-0014).
type Config struct {
	WorkspaceID string
	Policy      deterministic.Policy // WorkspaceID is overwritten with Config.WorkspaceID
	CRM         deterministic.CRMRules
	Knowledge   knowledge.Rules
	Routing     *Routing // the eval-suite router (contracts/transitions/routing.v1.json)

	// The run token's life is the signer's (runtoken.Signer.TTL). It must outlive generation plus judging plus one
	// revision with their per-call budgets (workerclient.*Timeout): an expired token is a transient failure, never
	// a lost run, but a token that cannot last one run would never let one finish.
	JudgeConcurrency int // candidates evaluated in parallel
	MaxContextPulls  int // the worker's pull budget per generation

	// KnowledgeCounterfactual turns on E7's `changed` attribution: after generating with knowledge the run generates
	// the same state once more with the DecisionGuidance withheld (arm A), stores it on the draft step and compares
	// each candidate, the ranking and the preferred action against it. It adds one more /v1/strategies call per run
	// and a longer token, so it is off by default and recorded on the run either way (counterfactual.status).
	KnowledgeCounterfactual bool

	// NewID, when set, supplies the ids the run itself mints (guidance, strategy set, draft-step episode, eval bundle).
	// Experiments that replay recorded model calls give it a deterministic sequence, because those ids are part of
	// the prompts the recordings are keyed by; nil (the default, and production) draws random v4 uuids.
	NewID func() string
}

// DefaultPolicy is the dry-run policy: every tool allowed, customer-facing autonomy (nothing is ever sent).
func DefaultPolicy() deterministic.Policy {
	return deterministic.Policy{AutonomyLevel: "customer_facing", AllowedTools: []string{
		deterministic.ToolEmailSend, deterministic.ToolCalendarInvite, deterministic.ToolDocumentShare,
		deterministic.ToolSlackPost, deterministic.ToolCRMNote, deterministic.ToolCRMUpdate}}
}

// DefaultCRM is a generic ordered stage list for the CRM legality checks.
func DefaultCRM() deterministic.CRMRules {
	return deterministic.CRMRules{Stages: []string{"Discovery", "Technical evaluation", "Commercial review", "Negotiation"},
		TerminalStages: []string{"Closed won", "Closed lost"}, MaxForwardSteps: 1}
}

// withDefaults fills unset fields.
func (c Config) withDefaults() Config {
	if c.JudgeConcurrency == 0 {
		c.JudgeConcurrency = DefaultJudgeConcurrency
	}
	if c.MaxContextPulls == 0 {
		c.MaxContextPulls = DefaultMaxContextPulls
	}
	if len(c.Policy.AllowedTools) == 0 {
		c.Policy = DefaultPolicy()
	}
	if len(c.CRM.Stages) == 0 {
		c.CRM = DefaultCRM()
	}
	c.Policy.WorkspaceID = c.WorkspaceID
	return c
}

// RequiredTokenTTL is the longest a run's token must work: a generation, the one regeneration the transition policy
// may ask for, with the counterfactual one more, then two judging rounds (the original and the one revision; a round
// takes one judge budget because candidates are judged in parallel, and a malformed judge answer is retried once)
// and the revision itself.
func RequiredTokenTTL(counterfactual bool) time.Duration {
	generations := 2
	if counterfactual {
		generations++
	}
	return time.Duration(generations)*workerclient.StrategiesTimeout +
		2*maxGenerationAttempts*workerclient.JudgeTimeout + workerclient.ReviseTimeout
}

func (c Config) validate(tokenTTL time.Duration) error {
	if c.WorkspaceID == "" {
		return errors.New("orchestrator: a workspace id is required")
	}
	if c.Routing == nil {
		return errors.New("orchestrator: an eval-suite router is required (LoadRouting)")
	}
	if len(c.Knowledge.ApplicableStatuses) == 0 {
		return errors.New("orchestrator: knowledge lifecycle rules are required")
	}
	if need := RequiredTokenTTL(c.KnowledgeCounterfactual); tokenTTL < need || tokenTTL > runtoken.MaxTTL {
		return fmt.Errorf("orchestrator: the run token lives %s but generation, judging and one revision need %s (at most %s)",
			tokenTTL, need, runtoken.MaxTTL)
	}
	if c.JudgeConcurrency < 1 || c.MaxContextPulls < 1 || c.MaxContextPulls > 12 {
		return errors.New("orchestrator: judge concurrency must be >= 1 and max tool calls in [1, 12]")
	}
	return nil
}

// lease is how long a draft-step claim is honoured: the token's life plus a margin.
func lease(tokenTTL time.Duration) time.Duration { return tokenTTL + leaseMargin }
