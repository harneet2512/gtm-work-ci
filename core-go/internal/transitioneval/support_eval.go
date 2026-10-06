package transitioneval

import (
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

// SupportReport measures the deterministic state_transition_support check (HAR-97 L1: "is the proposed transition
// supported by evidence?"). Positives are the detector's own records on steps where it agrees with the gold; the
// negatives are those records made wrong in one way each, so each is unsupported by construction. A good check
// passes every positive and fails every negative. It is a re-derivation check: it catches a record that deviates
// from what the rule set derives and says nothing about whether the rule set itself is right.
type SupportReport struct {
	Positives        Ratio            `json:"supported_records_passed"`
	Negatives        Ratio            `json:"unsupported_records_caught"`
	CaughtByMutation map[string]Ratio `json:"caught_by_mutation"`
	FalseAlarms      []string         `json:"false_alarms"`
	Missed           []string         `json:"missed"`
}

type mutation struct {
	name   string
	mutate func(transitions.Record) (transitions.Record, bool)
}

const (
	fabricatedActivity = "0ac70000-0000-4000-8000-0000000000ff"
	fabricatedClaim    = "c1a10000-0000-4000-8000-0000000000ff"
)

var mutations = []mutation{
	{"promoted_to_confirmed", func(r transitions.Record) (transitions.Record, bool) {
		if r.Status == transitions.StatusConfirmed {
			return r, false
		}
		r.Status = transitions.StatusConfirmed
		return withTarget(r), true
	}},
	{"demoted_to_candidate", func(r transitions.Record) (transitions.Record, bool) {
		if r.Status != transitions.StatusConfirmed {
			return r, false
		}
		r.Status = transitions.StatusCandidate
		return r, true
	}},
	{"retargeted", func(r transitions.Record) (transitions.Record, bool) {
		if r.ToStateCandidate == nil {
			return r, false
		}
		other := "REORG" // a state a rule can propose, so the wrong target is a plausible one
		if *r.ToStateCandidate == other {
			other = "EXPANSION"
		}
		r.ToStateCandidate = &other
		return r, true
	}},
	{"contradiction_dropped", func(r transitions.Record) (transitions.Record, bool) {
		if len(r.ContradictingFacts) == 0 {
			return r, false
		}
		r.ContradictingFacts = []transitions.FactResult{}
		return r, true
	}},
	{"fact_not_earned_added", func(r transitions.Record) (transitions.Record, bool) {
		if len(r.MissingFacts) == 0 {
			return r, false
		}
		f := r.MissingFacts[0]
		f.Satisfied, f.EvidenceRefs = true, []transitions.Ref{{ActivityID: fabricatedActivity}}
		r.SupportingFacts = append(append([]transitions.FactResult{}, r.SupportingFacts...), f)
		r.MissingFacts = append([]transitions.FactResult{}, r.MissingFacts[1:]...)
		return r, true
	}},
	{"evidence_fabricated", func(r transitions.Record) (transitions.Record, bool) {
		if len(r.SupportingFacts) == 0 {
			return r, false
		}
		facts := append([]transitions.FactResult{}, r.SupportingFacts...)
		facts[0].EvidenceRefs = []transitions.Ref{{ActivityID: fabricatedActivity, ClaimID: fabricatedClaim}}
		r.SupportingFacts = facts
		return r, true
	}},
}

// withTarget gives an UNRESOLVED record a target so a promotion is a well-formed (but unearned) CONFIRMED record.
func withTarget(r transitions.Record) transitions.Record {
	if r.ToStateCandidate == nil {
		to := "EXPANSION"
		r.ToStateCandidate = &to
	}
	return r
}

// EvaluateSupport runs the check over the detector's records.
func EvaluateSupport(rules transitions.RuleSet, results []StepResult) SupportReport {
	rep := SupportReport{CaughtByMutation: map[string]Ratio{}, FalseAlarms: []string{}, Missed: []string{}}
	passed, total := 0, 0
	caught, tried := map[string]int{}, map[string]int{}
	for _, r := range results {
		if r.Contested || r.record == nil || r.GoldStatus() != r.Pred.Status || goldTo(r.Gold) != r.Pred.ToState {
			continue // only records the gold agrees are right are positives
		}
		total++
		if v := transitions.CheckSupport(rules, r.input, *r.record); v.Pass {
			passed++
		} else {
			rep.FalseAlarms = append(rep.FalseAlarms, r.Scenario+": "+r.Label)
		}
		for _, m := range mutations {
			bad, ok := m.mutate(*r.record)
			if !ok {
				continue
			}
			tried[m.name]++
			if v := transitions.CheckSupport(rules, r.input, bad); !v.Pass {
				caught[m.name]++
			} else {
				rep.Missed = append(rep.Missed, m.name+" on "+r.Scenario+": "+r.Label)
			}
		}
	}
	rep.Positives = ratio(passed, total)
	sumCaught, sumTried := 0, 0
	for name, n := range tried {
		rep.CaughtByMutation[name] = ratio(caught[name], n)
		sumCaught += caught[name]
		sumTried += n
	}
	rep.Negatives = ratio(sumCaught, sumTried)
	sort.Strings(rep.FalseAlarms)
	sort.Strings(rep.Missed)
	return rep
}
