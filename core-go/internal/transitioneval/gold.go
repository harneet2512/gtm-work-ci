// Package transitioneval measures the transition detector against the hand-labelled transition gold
// (fixtures/gold/transitions/scenarios.json, authored by worker-py/tests/transition_gold.py). It replays each
// scenario step by step through the same Evaluate and Plan the recompute transaction uses, chaining the open
// transition and the confirmed relationship state between steps, and scores the outcomes. It reads no clock
// and no database: the report is a pure function of the gold and the rule set.
package transitioneval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// SliceContested marks scenarios the pitch can be read two ways; they are reported apart from the headline.
const SliceContested = "contested"

// Origins of a gold file: the engineer who wrote the detector authored one set, and a separate author who saw only
// the product spec (never the rules or the detector) wrote the other.
const (
	OriginAuthored = "authored"
	OriginBlind    = "blind"
)

// Label is the gold for one step.
type Label struct {
	Status                 *string  `json:"status"`
	ToState                *string  `json:"to_state"`
	SupportingRequired     []string `json:"supporting_required"`
	Contradicting          []string `json:"contradicting"`
	Closed                 bool     `json:"closed"`
	RelationshipStateAfter string   `json:"relationship_state_after"`
}

// Step is one evaluation of a scenario.
type Step struct {
	Label string            `json:"label"`
	Input transitions.Input `json:"input"`
	Gold  Label             `json:"gold"`
}

// Scenario is a sequence of steps over one account.
type Scenario struct {
	ID        string          `json:"id"`
	Slice     string          `json:"slice"`
	Source    string          `json:"source"`
	Rationale string          `json:"rationale"`
	Steps     []Step          `json:"steps"`
	Contested json.RawMessage `json:"contested"`
	Origin    string          `json:"-"` // set by the loader from the file it came from
}

// IsContested reports whether the pitch can be read two ways for this scenario.
func (s Scenario) IsContested() bool {
	return s.Slice == SliceContested || (len(s.Contested) > 0 && string(s.Contested) != "null")
}

// Gold is the gold file.
type Gold struct {
	Version   int        `json:"gold_version"`
	Scenarios []Scenario `json:"scenarios"`
}

// LoadGoldFiles merges gold files; scenario ids must be unique across them and each scenario remembers its origin.
func LoadGoldFiles(files map[string]string) (Gold, error) {
	merged := Gold{Version: 1}
	seen := map[string]bool{}
	for _, origin := range []string{OriginAuthored, OriginBlind} {
		path, ok := files[origin]
		if !ok {
			continue
		}
		g, err := LoadGold(path)
		if err != nil {
			return Gold{}, err
		}
		for _, s := range g.Scenarios {
			if seen[s.ID] {
				return Gold{}, fmt.Errorf("transitioneval: scenario %q appears in two gold files", s.ID)
			}
			seen[s.ID] = true
			s.Origin = origin
			merged.Scenarios = append(merged.Scenarios, s)
		}
	}
	if len(merged.Scenarios) == 0 {
		return Gold{}, errors.New("transitioneval: no gold files given")
	}
	return merged, nil
}

// LoadGold reads and checks one gold file.
func LoadGold(path string) (Gold, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Gold{}, fmt.Errorf("transitioneval: read gold: %w", err)
	}
	var g Gold
	if err := json.Unmarshal(raw, &g); err != nil {
		return Gold{}, fmt.Errorf("transitioneval: parse gold: %w", err)
	}
	if g.Version != 1 || len(g.Scenarios) == 0 {
		return Gold{}, fmt.Errorf("transitioneval: gold version %d with %d scenarios", g.Version, len(g.Scenarios))
	}
	seen := map[string]bool{}
	for _, s := range g.Scenarios {
		if seen[s.ID] || len(s.Steps) == 0 {
			return Gold{}, fmt.Errorf("transitioneval: scenario %q is duplicated or has no steps", s.ID)
		}
		seen[s.ID] = true
		for i, st := range s.Steps {
			if st.Gold.RelationshipStateAfter == "" {
				return Gold{}, fmt.Errorf("transitioneval: %s step %d has no relationship_state_after label", s.ID, i)
			}
		}
	}
	return g, nil
}
