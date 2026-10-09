package knowledge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	// NarrowCandidate, when set, offers a still-candidate knowledge object minted from a human's explicit
	// correction to the matcher (HAR-97 §11; ADR-0013 amendment). Nil keeps ApplicableStatuses the only gate.
	NarrowCandidate *NarrowRule `json:"narrow_candidate,omitempty"`
	// Similarity is the closest-match retrieval rules (similarity.v1.json next to the lifecycle file, loaded by
	// LoadRules; ADR-0013 amendment 2). Nil: every knowledge object is matched by its exact conditions.
	Similarity *Similarity `json:"-"`
}

// SimilarityFile is the similarity rules file LoadRules reads from the lifecycle file's directory.
const SimilarityFile = "similarity.v1.json"

// NarrowRule is the narrow-candidate exception to the applicable statuses.
type NarrowRule struct {
	CreatedFrom  []string `json:"created_from"`
	MinDecisions int      `json:"min_decisions"`
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
func LoadRules(path string) (Rules, error) { return loadRules(path, os.Stat) }

// loadRules is LoadRules with the stat of the similarity file injected, so a stat failure other than "not there" can be tested.
func loadRules(path string, stat func(string) (fs.FileInfo, error)) (Rules, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Rules{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	r, err := ParseRules(raw)
	if err != nil {
		return Rules{}, err
	}
	simPath := filepath.Join(filepath.Dir(path), SimilarityFile)
	if _, statErr := stat(simPath); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return r, nil // no similarity rules beside the lifecycle: exact matching only
		}
		return Rules{}, fmt.Errorf("%w: similarity rules %s: %v", ErrInvalidRules, simPath, statErr)
	}
	sim, err := LoadSimilarity(simPath)
	if err != nil {
		return Rules{}, err
	}
	r.Similarity = &sim
	return r, nil
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
	if n := r.NarrowCandidate; n != nil {
		if len(n.CreatedFrom) == 0 || n.MinDecisions < 1 {
			return fmt.Errorf("%w: narrow_candidate needs created_from and min_decisions >= 1", ErrInvalidRules)
		}
		for _, f := range n.CreatedFrom {
			if f != "manual" && f != "human_delta" {
				return fmt.Errorf("%w: narrow_candidate.created_from %q", ErrInvalidRules, f)
			}
		}
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

// ApplicableKnowledge reports whether this knowledge object may be offered for decision guidance: its status is
// applicable, or it is a narrow candidate (NarrowCandidate): still a candidate, minted from a human's explicit
// correction, named by the episode it was learned from, seeded from its own corrected, sent episode (no independent
// support yet), never contradicted (no counterexample, no negative reaction) and not too broad in scope.
func (r Rules) ApplicableKnowledge(k Knowledge) bool {
	if r.Applicable(k.Status) {
		return true
	}
	n := r.NarrowCandidate
	// Knowledge matched by similarity is bounded by the similarity threshold and its minimum comparable
	// features at match time, not by the exact-scope breadth check.
	broad := !r.Similarity.Matches(k) && ScopeTooBroad(k)
	return n != nil && k.Status == StatusCandidate && hasString(n.CreatedFrom, k.Provenance.CreatedFrom) &&
		k.Provenance.SourceDecisionEpisodeID != nil && k.Counts.Decisions >= n.MinDecisions &&
		k.Counts.Counterexamples == 0 && k.Counts.NegativeReactions == 0 && !broad
}

func rank(status string) int {
	for i, s := range ladder {
		if s == status {
			return i
		}
	}
	return -1
}
