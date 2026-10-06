// Package reactions is HAR-120, the supervision leg of the HAR-97 §12 loop. A deterministic detector
// scans the activities attributed to an account AFTER a sent decision and writes CustomerReaction
// rows (contracts/schemas/customer_reaction.v1.json) — a reply on the thread, a meeting accepted, a
// stakeholder added, a document requested, a silence tick past the configured window — and
// BusinessOutcome rows (business_outcome.v1.json) for the run's opportunity: stage moves, closed
// deals, ACV changes.
//
// A reaction links to an episode only through the watched channel: the run's correlation_id, the
// email thread of the trigger/sent conversation, the sent recipients, the caused-by chain or the
// episode's opportunity — and only while the episode is the latest send on the account, so one
// activity feeds one episode. `ignored` is emitted only when a ghost.clock customer_silence tick
// lands SilenceWindow or later after the send; silence is never inferred from absent evidence.
//
// Every new row feeds knowledgestore.RecordEvidence on the knowledge the decision used
// (candidate knowledge_refs + run knowledge_refs_used): positive reactions and advanced outcomes
// strengthen it, and enough negative reactions dispute it — correlation, never causal truth.
// Narrowing stays a human act: a counterexample is recorded by a person, not inferred from a
// reaction (ADR-0013). Detection is idempotent: (activity, type) / (activity, outcome) dedupe
// constraints make a re-scan a no-op.
package reactions

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// DefaultSilenceWindow is how long after a send a silence tick must arrive before it counts as
// "ignored" (matches signals.SilenceAfter, the customer_silence rule's own threshold).
const DefaultSilenceWindow = 7 * 24 * time.Hour

// ErrNotFound: no decision episode with that id (404 not_found).
var ErrNotFound = errors.New("reactions: not found")

// Options configures a Service.
type Options struct {
	// SilenceWindow is the post-send silence that makes a CustomerWentSilent tick an `ignored`
	// reaction (default DefaultSilenceWindow). Negative is an error.
	SilenceWindow time.Duration
	// Clock supplies the scan time (reaction created_at, evidence arrival). Defaults to the wall clock.
	Clock clock.Clock
}

// Service scans follow-on activity into supervision records and reads them back.
type Service struct {
	db            *sql.DB
	rules         knowledge.Rules
	silenceWindow time.Duration
	clk           clock.Clock
}

// New validates the options and returns a Service on db. rules is the knowledge lifecycle
// (contracts/knowledge/lifecycle.v1.json) evidence is evaluated against.
func New(db *sql.DB, rules knowledge.Rules, opts Options) (*Service, error) {
	if db == nil {
		return nil, errors.New("reactions: database is required")
	}
	if opts.SilenceWindow < 0 {
		return nil, errors.New("reactions: silence window must not be negative")
	}
	s := &Service{db: db, rules: rules, silenceWindow: opts.SilenceWindow, clk: opts.Clock}
	if s.silenceWindow == 0 {
		s.silenceWindow = DefaultSilenceWindow
	}
	if s.clk == nil {
		s.clk = clock.Real{}
	}
	return s, nil
}

// CustomerReaction is contracts/schemas/customer_reaction.v1.json.
type CustomerReaction struct {
	ID           string          `json:"id"`
	AccountID    string          `json:"account_id"`
	AgentRunID   *string         `json:"agent_run_id"`
	ActivityID   string          `json:"activity_id"`
	ReactionType string          `json:"reaction_type"`
	Polarity     string          `json:"polarity"`
	EvidenceRefs json.RawMessage `json:"evidence_refs"`
	CreatedAt    time.Time       `json:"created_at"`
}

// BusinessOutcome is contracts/schemas/business_outcome.v1.json.
type BusinessOutcome struct {
	ID            string          `json:"id"`
	AccountID     string          `json:"account_id"`
	AgentRunID    *string         `json:"agent_run_id"`
	OpportunityID *string         `json:"opportunity_id"`
	ActivityID    *string         `json:"activity_id"`
	OutcomeType   string          `json:"outcome_type"`
	Value         json.RawMessage `json:"value"`
	EvidenceRefs  json.RawMessage `json:"evidence_refs"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

// Supervision is core.yaml components.schemas.EpisodeSupervision: GET /episodes/{id}/reactions.
type Supervision struct {
	CustomerReactions []CustomerReaction `json:"customer_reactions"`
	BusinessOutcomes  []BusinessOutcome  `json:"business_outcomes"`
}

// ScanResult counts what one scan pass wrote.
type ScanResult struct {
	Episodes  int // supervision-open episodes scanned
	Reactions int // new customer_reactions rows
	Outcomes  int // new business_outcomes rows
	Evidence  int // knowledge_evidence rows recorded
}

func (r ScanResult) String() string {
	return fmt.Sprintf("episodes=%d reactions=%d outcomes=%d evidence=%d", r.Episodes, r.Reactions, r.Outcomes, r.Evidence)
}
