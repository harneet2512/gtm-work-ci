// Package transitions is the deterministic transition detector (HAR-126, ADR-0012). Given an
// AccountState, the account's signals and claims, and its open transition, Evaluate applies the
// rule set held as data (contracts/transitions/rules.v1.json) and says which StateTransition
// status the evidence earns. There is no clock, database or model in the decision: the same
// input always gives the same outcome, and the Python reference evaluator
// (worker-py/tests/transition_reference.py) agrees with it on the shared parity cases.
package transitions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Threshold is one named, configurable number of the rule set.
type Threshold struct {
	Value       int    `json:"value"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
}

// Condition is one test over the account (rules.v1.json grammar; transition_rules.v1.json).
type Condition struct {
	Path      string            `json:"path"`
	Op        string            `json:"op"`
	Value     json.RawMessage   `json:"value,omitempty"`
	Threshold string            `json:"threshold,omitempty"`
	Since     string            `json:"since,omitempty"`
	About     string            `json:"about,omitempty"`
	Where     map[string]string `json:"where,omitempty"`
	// Stance selects stated (default) or withdrawn claims for a claim.* condition.
	Stance string `json:"stance,omitempty"`
}

// Group is a conjunction of conditions.
type Group struct {
	AllOf []Condition `json:"all_of"`
}

// Fact is a named requirement (or, in Rule.Contradictions, a named contradiction): a disjunction of groups.
type Fact struct {
	Key         string  `json:"key"`
	Required    bool    `json:"required"`
	Rejects     bool    `json:"rejects,omitempty"`
	Description string  `json:"description"`
	Har97       string  `json:"har97,omitempty"`
	AnyOf       []Group `json:"any_of"`
}

// MinSatisfied is the candidate gate's breadth: at least Threshold of Of must hold.
type MinSatisfied struct {
	Threshold string   `json:"threshold"`
	Of        []string `json:"of"`
}

// Gate is the CANDIDATE gate: one RequiresAny fact holds and MinSatisfied is met.
type Gate struct {
	RequiresAny  []string     `json:"requires_any"`
	MinSatisfied MinSatisfied `json:"min_satisfied"`
}

// Confirmed lists the facts that must all hold for CONFIRMED.
type Confirmed struct {
	RequiresAll []string `json:"requires_all"`
	// AfterCandidate: confirm only when this rule's own CANDIDATE was recorded by an earlier evaluation.
	AfterCandidate bool `json:"after_candidate,omitempty"`
}

// Rule is one transition the detector may propose.
type Rule struct {
	ID          string   `json:"id"`
	FromStates  []string `json:"from_states"`
	ToState     string   `json:"to_state"`
	Description string   `json:"description"`
	// ClaimScope is "deal" (read each open deal's own claims and signals) or "account" (read all of them).
	ClaimScope     string    `json:"claim_scope"`
	Har97Sections  []string  `json:"har97_sections"`
	Facts          []Fact    `json:"facts"`
	Candidate      Gate      `json:"candidate"`
	Confirmed      Confirmed `json:"confirmed"`
	Contradictions []Fact    `json:"contradictions"`
}

// RuleSet is contracts/transitions/rules.v1.json.
type RuleSet struct {
	Version                   string               `json:"rule_set_version"`
	Description               string               `json:"description"`
	Evaluation                []string             `json:"evaluation"`
	Confidence                string               `json:"confidence"`
	Anchors                   map[string]string    `json:"anchors"`
	ClaimStandings            []string             `json:"claim_standings"`
	ClaimStatuses             []string             `json:"claim_statuses"`
	BuyingGroupMemberStatuses []string             `json:"buying_group_member_statuses"`
	Vocabularies              map[string][]string  `json:"vocabularies"`
	Thresholds                map[string]Threshold `json:"thresholds"`
	Transitions               []Rule               `json:"transitions"`
}

// staleThreshold names the threshold after which an idle UNRESOLVED transition is closed.
const staleThreshold = "unresolved_stale_days"

var versionPattern = regexp.MustCompile(`^transition_rules:v[0-9]+$`)

// ErrInvalidRules wraps every rule-set validation failure.
var ErrInvalidRules = errors.New("transitions: invalid rule set")

// LoadRules reads and validates a rule set file.
func LoadRules(path string) (RuleSet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RuleSet{}, fmt.Errorf("transitions: read rules: %w", err)
	}
	return ParseRules(raw)
}

// ParseRules decodes strictly (an unknown key is an error, so nothing in a rule is silently ignored)
// and checks that every reference inside the set resolves.
func ParseRules(raw []byte) (RuleSet, error) {
	var rs RuleSet
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rs); err != nil {
		return RuleSet{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	if err := rs.validate(); err != nil {
		return RuleSet{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	return rs, nil
}

func (rs RuleSet) validate() error {
	if !versionPattern.MatchString(rs.Version) {
		return fmt.Errorf("rule_set_version %q", rs.Version)
	}
	if len(rs.Transitions) == 0 {
		return errors.New("no transitions")
	}
	if len(rs.ClaimStatuses) == 0 || len(rs.ClaimStandings) == 0 {
		return errors.New("claim_statuses and claim_standings must not be empty")
	}
	if _, ok := rs.Thresholds[staleThreshold]; !ok {
		return fmt.Errorf("missing threshold %q", staleThreshold)
	}
	seen := map[string]bool{}
	for _, r := range rs.Transitions {
		if seen[r.ID] {
			return fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
		if err := rs.validateRule(r); err != nil {
			return fmt.Errorf("rule %s: %w", r.ID, err)
		}
	}
	return nil
}

func (rs RuleSet) validateRule(r Rule) error {
	if r.ClaimScope != "deal" && r.ClaimScope != "account" {
		return fmt.Errorf("claim_scope %q", r.ClaimScope)
	}
	keys := map[string]bool{}
	for _, f := range r.Facts {
		if keys[f.Key] {
			return fmt.Errorf("duplicate fact %q", f.Key)
		}
		keys[f.Key] = true
	}
	refs := append(append(append([]string{}, r.Candidate.RequiresAny...), r.Candidate.MinSatisfied.Of...), r.Confirmed.RequiresAll...)
	for _, k := range refs {
		if !keys[k] {
			return fmt.Errorf("gate or confirmation names unknown fact %q", k)
		}
	}
	if len(r.Confirmed.RequiresAll) == 0 {
		return errors.New("a rule needs at least one required fact")
	}
	if _, ok := rs.Thresholds[r.Candidate.MinSatisfied.Threshold]; !ok {
		return fmt.Errorf("unknown threshold %q", r.Candidate.MinSatisfied.Threshold)
	}
	for _, f := range append(append([]Fact{}, r.Facts...), r.Contradictions...) {
		for _, g := range f.AnyOf {
			for _, c := range g.AllOf {
				if c.Threshold != "" {
					if _, ok := rs.Thresholds[c.Threshold]; !ok {
						return fmt.Errorf("fact %s: unknown threshold %q", f.Key, c.Threshold)
					}
				}
			}
		}
	}
	return nil
}

// Threshold returns a named threshold value; Validate guarantees the name exists for rule-set references.
func (rs RuleSet) days(name string) int { return rs.Thresholds[name].Value }

// ClaimPaths lists the claim field paths the rules read ("claim.<path>" conditions), sorted and unique,
// so the detector loads only those claims.
func (rs RuleSet) ClaimPaths() []string {
	set := map[string]bool{}
	for _, r := range rs.Transitions {
		for _, f := range append(append([]Fact{}, r.Facts...), r.Contradictions...) {
			for _, g := range f.AnyOf {
				for _, c := range g.AllOf {
					if path, ok := strings.CutPrefix(c.Path, "claim."); ok {
						set[path] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
