// Package knowledge implements the WP20 (HAR-118) knowledge layer: a deterministic applicability
// matcher (situation signature + applicability conditions, then exceptions) over AccountState, open
// signals (ADR-0011) and the open StateTransition (ADR-0012), and the knowledge lifecycle
// candidate -> provisional -> supported -> confirmed -> disputed / stale driven by threshold data
// (ADR-0013). Nothing here calls a model, a clock or a database: same inputs, same output.
package knowledge

import "time"

// Knowledge is the contract object (contracts/schemas/knowledge.v1.json). Values are treated as
// immutable: functions return modified copies.
type Knowledge struct {
	ID                           string           `json:"id"`
	Key                          *string          `json:"key,omitempty"`
	Title                        string           `json:"title"`
	SituationSignature           []Condition      `json:"situation_signature"`
	ApplicabilityConditions      []Condition      `json:"applicability_conditions,omitempty"`
	Guidance                     Guidance         `json:"guidance"`
	Status                       string           `json:"status"`
	Counts                       Counts           `json:"counts"`
	SupportingDecisionEpisodeIDs []string         `json:"supporting_decision_episode_ids"`
	Counterexamples              []Counterexample `json:"counterexamples"`
	Exceptions                   []Exception      `json:"exceptions"`
	EvidenceClasses              []string         `json:"evidence_classes,omitempty"`
	UsedByEvaluators             []string         `json:"used_by_evaluators,omitempty"`
	Provenance                   Provenance       `json:"provenance"`
	CreatedAt                    time.Time        `json:"created_at"`
	LastValidatedAt              *time.Time       `json:"last_validated_at"`
	StatusHistory                []HistoryEntry   `json:"status_history,omitempty"`
}

// Condition is one test over the situation (knowledge.v1.json#/$defs/condition). Value is the
// decoded JSON value: a string, number or bool, a list of those, or an item pattern object.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// Exception overrides the knowledge when all of its conditions hold (checked only after the
// signature and applicability conditions hold).
type Exception struct {
	Description string      `json:"description"`
	Conditions  []Condition `json:"conditions"`
}

// Guidance is what the knowledge says to do.
type Guidance struct {
	Summary string   `json:"summary"`
	Do      []string `json:"do"`
	Dont    []string `json:"dont"`
}

// Counts are the evidence tallies the lifecycle reads.
type Counts struct {
	Decisions         int `json:"decisions"`
	PositiveReactions int `json:"positive_reactions"`
	NegativeReactions int `json:"negative_reactions"`
	OutcomesAdvanced  int `json:"outcomes_advanced"`
	Counterexamples   int `json:"counterexamples"`
}

// Counterexample is a decision episode where following the knowledge was wrong.
type Counterexample struct {
	DecisionEpisodeID string `json:"decision_episode_id"`
	Note              string `json:"note"`
}

// Provenance records where the knowledge came from.
type Provenance struct {
	CreatedFrom             string  `json:"created_from"`
	SourceDecisionEpisodeID *string `json:"source_decision_episode_id,omitempty"`
	Note                    *string `json:"note,omitempty"`
}

// HistoryEntry is one lifecycle change (knowledge_status_history).
type HistoryEntry struct {
	FromStatus    *string   `json:"from_status"`
	ToStatus      string    `json:"to_status"`
	Reason        string    `json:"reason"`
	EvidenceKind  *string   `json:"evidence_kind,omitempty"`
	EvidenceRefID *string   `json:"evidence_ref_id,omitempty"`
	ChangedAt     time.Time `json:"changed_at"`
}

// EvidenceRef points at the activity (and claim) behind a value (common.v1.json#/$defs/evidenceRef).
type EvidenceRef struct {
	ActivityID string `json:"activity_id"`
	ClaimID    string `json:"claim_id,omitempty"`
}

// Lifecycle statuses (knowledge.v1.json#/properties/status).
const (
	StatusCandidate   = "candidate"
	StatusProvisional = "provisional"
	StatusSupported   = "supported"
	StatusConfirmed   = "confirmed"
	StatusDisputed    = "disputed"
	StatusStale       = "stale"
)

// Applicability labels (contracts/evals/eval_catalog.json, knowledge_applicability).
const (
	LabelApplies            = "APPLIES"
	LabelDoesNotApply       = "DOES_NOT_APPLY"
	LabelExceptionTriggered = "EXCEPTION_TRIGGERED"
)
