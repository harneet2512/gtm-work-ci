package transitions

import "time"

// Status values of a StateTransition (common.v1.json#/$defs/transitionStatus).
const (
	StatusConfirmed  = "CONFIRMED"
	StatusCandidate  = "CANDIDATE"
	StatusUnresolved = "UNRESOLVED"
	StatusRejected   = "REJECTED"
)

// FactResult is one fact (or contradiction) evaluated against the account; its JSON is the
// state_transition.v1.json fact shape.
type FactResult struct {
	Key          string   `json:"key"`
	Description  string   `json:"description"`
	Required     bool     `json:"required"`
	Satisfied    bool     `json:"satisfied"`
	EvidenceRefs []Ref    `json:"evidence_refs"`
	SignalIDs    []string `json:"signal_ids"`
	Rejects      *bool    `json:"rejects,omitempty"`
}

// Outcome is what the rule set says about one account at one AccountState version. Status "" means
// no transition. Closed is set on a stale UNRESOLVED that must be closed.
type Outcome struct {
	Status        string
	ToState       string // "" when UNRESOLVED or no transition
	RuleID        string
	Supporting    []FactResult
	Missing       []FactResult
	Contradicting []FactResult
	Confidence    float64
	Closed        bool
	ConfirmedAt   *time.Time
}

// None reports whether the evaluation produced no transition.
func (o Outcome) None() bool { return o.Status == "" }
