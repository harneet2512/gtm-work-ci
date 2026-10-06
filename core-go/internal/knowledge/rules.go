package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ErrInvalidRules is wrapped by every lifecycle-rules error.
var ErrInvalidRules = errors.New("invalid knowledge lifecycle rules")

// Rules are the lifecycle thresholds (contracts/knowledge/lifecycle.v1.json, knowledge_lifecycle.v1.json).
type Rules struct {
	Version            string      `json:"version"`
	Description        string      `json:"description,omitempty"`
	ApplicableStatuses []string    `json:"applicable_statuses"`
	Rungs              []Rung      `json:"rungs"`
	Dispute            DisputeRule `json:"dispute"`
	Stale              StaleRule   `json:"stale"`
}

// Rung is one step of the promotion ladder.
type Rung struct {
	Status                 string  `json:"status"`
	MinDecisions           int     `json:"min_decisions"`
	MinPositiveReactions   int     `json:"min_positive_reactions"`
	MinOutcomesAdvanced    int     `json:"min_outcomes_advanced"`
	MaxCounterexampleShare float64 `json:"max_counterexample_share"`
}

// DisputeRule says when evidence contradicts the knowledge.
type DisputeRule struct {
	MinCounterexamples     int     `json:"min_counterexamples"`
	MinCounterexampleShare float64 `json:"min_counterexample_share"`
	MinNegativeReactions   int     `json:"min_negative_reactions"`
}

// StaleRule says when unvalidated knowledge goes stale.
type StaleRule struct {
	AfterDays int `json:"after_days"`
}

// ladder is the promotion order; disputed and stale sit outside it.
var ladder = []string{StatusCandidate, StatusProvisional, StatusSupported, StatusConfirmed}

var allStatuses = map[string]bool{
	StatusCandidate: true, StatusProvisional: true, StatusSupported: true, StatusConfirmed: true,
	StatusDisputed: true, StatusStale: true,
}

// LoadRules reads and validates a lifecycle rules file.
func LoadRules(path string) (Rules, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Rules{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	return ParseRules(raw)
}

// ParseRules decodes strictly (unknown keys are errors) and checks the ladder.
func ParseRules(raw []byte) (Rules, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Rules
	if err := dec.Decode(&r); err != nil {
		return Rules{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	if dec.More() {
		return Rules{}, fmt.Errorf("%w: trailing data after the rules object", ErrInvalidRules)
	}
	if err := r.check(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

func (r Rules) check() error {
	if r.Version == "" || len(r.ApplicableStatuses) == 0 {
		return fmt.Errorf("%w: version and applicable_statuses are required", ErrInvalidRules)
	}
	for _, s := range r.ApplicableStatuses {
		if !allStatuses[s] {
			return fmt.Errorf("%w: unknown status %q", ErrInvalidRules, s)
		}
	}
	if len(r.Rungs) != len(ladder)-1 {
		return fmt.Errorf("%w: need %d rungs", ErrInvalidRules, len(ladder)-1)
	}
	for i, g := range r.Rungs {
		if g.Status != ladder[i+1] || g.MinDecisions < 1 || g.MinPositiveReactions < 0 || g.MinOutcomesAdvanced < 0 ||
			g.MaxCounterexampleShare < 0 || g.MaxCounterexampleShare > 1 {
			return fmt.Errorf("%w: rung %d must be %q with min_decisions >= 1, non-negative minimums and a share in [0,1]",
				ErrInvalidRules, i, ladder[i+1])
		}
		if i > 0 && !stricter(g, r.Rungs[i-1]) {
			return fmt.Errorf("%w: rung %q must be stricter than %q", ErrInvalidRules, g.Status, r.Rungs[i-1].Status)
		}
	}
	d := r.Dispute
	if d.MinCounterexamples < 1 || d.MinNegativeReactions < 1 || d.MinCounterexampleShare <= 0 || d.MinCounterexampleShare > 1 {
		return fmt.Errorf("%w: dispute thresholds out of range", ErrInvalidRules)
	}
	if r.Stale.AfterDays < 1 {
		return fmt.Errorf("%w: stale.after_days must be >= 1", ErrInvalidRules)
	}
	return nil
}

// stricter: more decisions, no fewer reactions or outcomes, and no larger counterexample share.
func stricter(g, prev Rung) bool {
	return g.MinDecisions > prev.MinDecisions && g.MinPositiveReactions >= prev.MinPositiveReactions &&
		g.MinOutcomesAdvanced >= prev.MinOutcomesAdvanced && g.MaxCounterexampleShare <= prev.MaxCounterexampleShare
}

// Applicable reports whether knowledge in this status may be offered for decision guidance.
func (r Rules) Applicable(status string) bool { return hasString(r.ApplicableStatuses, status) }

func rank(status string) int {
	for i, s := range ladder {
		if s == status {
			return i
		}
	}
	return -1
}
