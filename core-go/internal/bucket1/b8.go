package bucket1

import (
	"fmt"
	"strings"
)

// beliefKind maps a state field to the belief kind that must reflect it.
var beliefKind = map[string]string{
	"blockers": "blocker", "current_commitments": "commitment", "champion": "stakeholder",
	"economic_buyer": "stakeholder", "next_milestone": "next_decision",
}

// GradeB8 judges the current-intelligence synthesis: every belief traces to evidence in the episode,
// conflicted fields are mentioned, blockers, commitments, stakeholders and the next decision are present
// when claims exist; confidence and omitted counter-evidence are one recorded model judgment.
func GradeB8(ep Episode, js map[string]Judgment) Result {
	if len(ep.Beliefs) == 0 {
		return Unmeasured(ep, "B8", "the episode recorded no synthesized beliefs")
	}
	as := []Assertion{b8Supported(ep), b8CounterEvidence(ep), b8Coverage(ep), modelAssertion(ep, js, "B8", "confidence_and_omissions")}
	return rollup(ep, "B8", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b8Supported(ep Episode) Assertion {
	const name = "beliefs_traceable"
	var refs, bad []Ref
	for _, b := range ep.Beliefs {
		if len(b.Refs) == 0 || len(ep.unknownRefs(b.Refs)) > 0 {
			bad = append(bad, Ref{Note: b.Kind + ": " + b.Statement})
			continue
		}
		refs = append(refs, b.Refs...)
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d of %d beliefs cite no evidence that exists in the episode", len(bad), len(ep.Beliefs)),
			"a belief is not traceable backward to evidence", append(bad, anchor(ep))...)
	}
	return pass(name, fmt.Sprintf("all %d beliefs cite evidence in the episode", len(ep.Beliefs)), refs...)
}

// cites reports whether some belief cites the claim in its evidence refs: surfacing counter-evidence means
// pointing at it, not mentioning a field name.
func cites(beliefs []Belief, claimID string) bool {
	for _, b := range beliefs {
		for _, r := range b.Refs {
			if r.ClaimID == claimID {
				return true
			}
		}
	}
	return false
}

func mentions(beliefs []Belief, words ...string) bool {
	for _, b := range beliefs {
		s := norm(b.Statement)
		for _, w := range words {
			if strings.Contains(s, norm(w)) {
				return true
			}
		}
	}
	return false
}

func b8CounterEvidence(ep Episode) Assertion {
	const name = "counter_evidence_not_omitted"
	var bad, refs []Ref
	for _, c := range ep.Claims {
		if c.Status != "conflicted" && len(c.ConflictsWith) == 0 {
			continue
		}
		r := Ref{ActivityID: c.ActivityID, ClaimID: c.ID}
		if cites(ep.Beliefs, c.ID) {
			refs = append(refs, r)
		} else {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return warn(name, fmt.Sprintf("%d conflicted claims are not mentioned by any belief", len(bad)), "counter-evidence was omitted from the summary", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no claim of the episode is in conflict")
	}
	return pass(name, "every conflicted field is mentioned", refs...)
}

func b8Coverage(ep Episode) Assertion {
	const name = "blockers_commitments_stakeholders_next_decision"
	have := map[string]bool{}
	for _, b := range ep.Beliefs {
		have[b.Kind] = true
	}
	var bad, refs []Ref
	for _, c := range ep.Claims {
		kind, ok := beliefKind[c.Field]
		if !ok || c.Status == "rejected" || c.Status == "superseded" {
			continue
		}
		r := Ref{ActivityID: c.ActivityID, ClaimID: c.ID, Note: "needs a " + kind + " belief"}
		if have[kind] {
			refs = append(refs, r)
		} else {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return warn(name, fmt.Sprintf("%d current claims have no belief of their kind", len(bad)), "the synthesis does not reflect current evidence", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no blocker, commitment, stakeholder or next-decision claim needs a belief")
	}
	return pass(name, "every such claim is reflected by a belief", refs...)
}
