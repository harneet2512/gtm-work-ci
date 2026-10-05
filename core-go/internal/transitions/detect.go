package transitions

import (
	"math"
	"time"
)

// ruleResult is one rule evaluated against the account.
type ruleResult struct {
	rule          Rule
	facts         []FactResult // in rule order
	contradicting []FactResult
	decisive      bool
	confirms      bool
	pending       bool // every required fact holds but the rule waits for its CANDIDATE to be recorded
	gated         bool
	confidence    float64
}

// evaluateRule evaluates a rule over its scopes: a fact holds when it holds in any scope, a contradiction when
// it holds in any. open is the account's open transition (for after_candidate rules).
func evaluateRule(rs RuleSet, r Rule, in Input, open *Open) ruleResult {
	evs := scopes(rs, r, in)
	res := ruleResult{rule: r}
	held := map[string]bool{}
	for _, f := range r.Facts {
		fr := evs[0].fact(f)
		for _, e := range evs[1:] {
			if fr.Satisfied {
				break
			}
			fr = e.fact(f)
		}
		res.facts = append(res.facts, fr)
		held[f.Key] = fr.Satisfied
	}
	for _, c := range r.Contradictions {
		for _, e := range evs {
			if cr := e.contradiction(c); cr.Satisfied {
				res.contradicting = append(res.contradicting, cr)
				res.decisive = res.decisive || c.Rejects
				break
			}
		}
	}
	need := rs.days(r.Candidate.MinSatisfied.Threshold)
	allowed := !r.Confirmed.AfterCandidate || (open != nil && open.Status == StatusCandidate && open.ToStateCandidate == r.ToState)
	res.confirms = allHeld(held, r.Confirmed.RequiresAll) && !res.decisive && allowed
	res.pending = allHeld(held, r.Confirmed.RequiresAll) && !res.decisive && !allowed
	res.gated = anyHeld(held, r.Candidate.RequiresAny) && countHeld(held, r.Candidate.MinSatisfied.Of) >= need && !res.decisive
	satisfied := countHeld(held, r.Confirmed.RequiresAll)
	res.confidence = math.Round(float64(satisfied)/float64(len(r.Confirmed.RequiresAll))*1000) / 1000
	return res
}

func allHeld(held map[string]bool, keys []string) bool { return countHeld(held, keys) == len(keys) }
func anyHeld(held map[string]bool, keys []string) bool { return countHeld(held, keys) > 0 }
func countHeld(held map[string]bool, keys []string) int {
	n := 0
	for _, k := range keys {
		if held[k] {
			n++
		}
	}
	return n
}

func (rr ruleResult) single(status string, confirmedAt *time.Time) Outcome {
	o := Outcome{Status: status, ToState: rr.rule.ToState, RuleID: rr.rule.ID, Confidence: rr.confidence,
		Contradicting: rr.contradicting, ConfirmedAt: confirmedAt}
	for _, f := range rr.facts {
		if f.Satisfied {
			o.Supporting = append(o.Supporting, f)
		} else {
			o.Missing = append(o.Missing, f)
		}
	}
	if status == StatusCandidate && rr.pending {
		o.Missing = append(o.Missing, pendingFact) // a CANDIDATE always has an unmet required fact: here, the recorded candidate itself
	}
	return o
}

var pendingFact = FactResult{Key: "candidate_recorded", Required: true, EvidenceRefs: []Ref{}, SignalIDs: []string{},
	Description: "The evidence is complete; the transition is confirmed on the evaluation after this candidate is recorded."}

// unresolved keeps the facts of the rule(s) it came from (the first occurrence of a key wins) and
// records their contradictions as non-decisive: with no target nothing is rejected.
func unresolved(from []ruleResult, closed bool) Outcome {
	o := Outcome{Status: StatusUnresolved, Closed: closed}
	seenFact, seenContra := map[string]bool{}, map[string]bool{}
	for _, rr := range from {
		for _, f := range rr.facts {
			if seenFact[f.Key] {
				continue
			}
			seenFact[f.Key] = true
			if f.Satisfied {
				o.Supporting = append(o.Supporting, f)
			} else {
				o.Missing = append(o.Missing, f)
			}
		}
		for _, c := range rr.contradicting {
			if seenContra[c.Key] {
				continue
			}
			seenContra[c.Key] = true
			no := false
			c.Required, c.Rejects = false, &no
			o.Contradicting = append(o.Contradicting, c)
		}
		o.Confidence = math.Max(o.Confidence, rr.confidence)
	}
	return o
}

// choose: exactly one rule with the open target (or none open) gives status; two or more, or a
// different target, give UNRESOLVED. It returns false when nothing was picked.
func choose(status string, picked []ruleResult, target string, involved []ruleResult, computedAt *time.Time) (Outcome, bool) {
	if len(picked) == 0 {
		return Outcome{}, false
	}
	if len(picked) == 1 && (target == "" || target == picked[0].rule.ToState) {
		var at *time.Time
		if status == StatusConfirmed {
			copied := *computedAt
			at = &copied
		}
		return picked[0].single(status, at), true
	}
	all := append([]ruleResult{}, picked...)
	for _, o := range involved {
		if !containsRule(picked, o) {
			all = append(all, o)
		}
	}
	return unresolved(all, false), true
}

func containsRule(rs []ruleResult, r ruleResult) bool {
	for _, x := range rs {
		if x.rule.ID == r.rule.ID {
			return true
		}
	}
	return false
}

func filter(rs []ruleResult, keep func(ruleResult) bool) []ruleResult {
	var out []ruleResult
	for _, r := range rs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// Evaluate applies the rule set's evaluation steps (rules.v1.json#/evaluation) in order and returns
// the outcome for the account at this AccountState version. At most one transition step is taken.
func Evaluate(rs RuleSet, in Input) Outcome {
	e := newEvaluator(rs, in)
	current := in.State.RelationshipState.Value
	if current == "" {
		current = "unknown"
	}
	var results []ruleResult
	for _, r := range rs.Transitions {
		if contains(r.FromStates, current) {
			results = append(results, evaluateRule(rs, r, in, in.Open))
		}
	}
	confirmedAt := in.Now // confirmed_at is the evaluated version's as_of, never the wall clock
	var prev Open
	if in.Open != nil {
		prev = *in.Open
	}
	target := prev.ToStateCandidate
	own := filter(results, func(r ruleResult) bool { return target != "" && r.rule.ToState == target })
	if prev.Status == StatusCandidate && len(own) > 0 && own[0].decisive { // step 2
		return own[0].single(StatusRejected, nil)
	}
	if o, ok := choose(StatusConfirmed, filter(results, func(r ruleResult) bool { return r.confirms }), target, own, &confirmedAt); ok {
		return o // step 3
	}
	if o, ok := choose(StatusCandidate, filter(results, func(r ruleResult) bool { return r.gated }), target, own, &confirmedAt); ok {
		return o // step 4
	}
	switch prev.Status {
	case StatusCandidate: // step 5: the gate was lost
		return unresolved(own, false)
	case StatusUnresolved: // step 6: close when stale
		return unresolved(results, in.Now.Sub(prev.LastUpdatedAt) >= e.window(staleThreshold))
	}
	return Outcome{}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
