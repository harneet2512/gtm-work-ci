package transitions

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

// Verdict is the result of the state_transition_support check (eval_catalog.json, L1, deterministic).
type Verdict struct {
	Pass    bool
	Reasons []string
}

// CheckSupport re-derives a transition from the rule set and the input it was evaluated on, and says
// whether the record is what the evidence earns: same status and target, the same satisfied, unmet and
// contradicting facts, and the same cited evidence. It is the deterministic answer to "is the proposed
// transition supported by evidence?" (HAR-97 L1): no model decides whether a state is earned.
//
// in must be the input exactly as the detector saw it: the AccountState version the record was last evaluated
// against, with relationship_state as it was before the record (from_state and when it was confirmed), the
// claims and signals of that moment and the open transition the evaluation started from (in.Open, which the
// history snapshot before this one holds). A closed record is re-derived the same way with a stale UNRESOLVED
// as its open transition.
func CheckSupport(rs RuleSet, in Input, rec Record) Verdict {
	target := ""
	if rec.ToStateCandidate != nil {
		target = *rec.ToStateCandidate
	}
	if rec.ClosedAt != nil {
		return closedSupport(rs, in, rec)
	}
	out := Evaluate(rs, in)
	if out.Status != rec.Status || out.ToState != target || out.Closed {
		return Verdict{Reasons: []string{fmt.Sprintf("recorded %s/%s is not what the rules derive from this evidence (derived %s/%s)", rec.Status, target, out.Status, out.ToState)}}
	}
	reasons := factReasons(rec, out)
	return Verdict{Pass: len(reasons) == 0, Reasons: reasons}
}

// factReasons lists how a record's facts differ from a derived outcome's.
func factReasons(rec Record, derived Outcome) []string {
	var reasons []string
	reasons = append(reasons, factDiff("supporting", rec.SupportingFacts, derived.Supporting)...)
	reasons = append(reasons, factDiff("missing", rec.MissingFacts, derived.Missing)...)
	return append(reasons, factDiff("contradicting", rec.ContradictingFacts, derived.Contradicting)...)
}

// closedSupport re-derives a closed (stale) UNRESOLVED record: the rules must close it, with the facts it kept.
func closedSupport(rs RuleSet, in Input, rec Record) Verdict {
	if rec.Status != StatusUnresolved || rec.ToStateCandidate != nil {
		return Verdict{Reasons: []string{"only an UNRESOLVED transition without a target can be closed"}}
	}
	in.Open = &Open{Status: StatusUnresolved, LastUpdatedAt: in.Now.Add(-time.Duration(rs.days(staleThreshold)) * 24 * time.Hour)}
	out := Evaluate(rs, in)
	if out.Status != StatusUnresolved || !out.Closed {
		return Verdict{Reasons: []string{"the rules do not close this transition as stale (derived " + out.Status + ")"}}
	}
	reasons := factReasons(rec, out)
	return Verdict{Pass: len(reasons) == 0, Reasons: reasons}
}

// factDiff compares a recorded fact list with the derived one, by key, evidence and signals.
func factDiff(kind string, recorded, derived []FactResult) []string {
	var out []string
	want := map[string]FactResult{}
	for _, f := range derived {
		want[f.Key] = f
	}
	for _, f := range recorded {
		d, ok := want[f.Key]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s fact %s is not derivable", kind, f.Key))
		case !sameRefs(f.EvidenceRefs, d.EvidenceRefs):
			out = append(out, fmt.Sprintf("%s fact %s cites evidence the rules do not find", kind, f.Key))
		case f.Rejects != nil && d.Rejects != nil && *f.Rejects != *d.Rejects:
			out = append(out, fmt.Sprintf("%s fact %s has a different decisiveness", kind, f.Key))
		}
		delete(want, f.Key)
	}
	missing := make([]string, 0, len(want))
	for k := range want {
		missing = append(missing, k)
	}
	sort.Strings(missing)
	for _, k := range missing {
		out = append(out, fmt.Sprintf("%s fact %s was derived but is not recorded", kind, k))
	}
	return out
}

func sameRefs(a, b []Ref) bool {
	key := func(r Ref) string { return r.ActivityID + "|" + r.ClaimID }
	ka, kb := make([]string, 0, len(a)), make([]string, 0, len(b))
	for _, r := range a {
		ka = append(ka, key(r))
	}
	for _, r := range b {
		kb = append(kb, key(r))
	}
	slices.Sort(ka)
	slices.Sort(kb)
	return slices.Equal(ka, kb)
}
