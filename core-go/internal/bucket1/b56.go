package bucket1

import (
	"fmt"
	"time"
)

// MinSharedFeatures is how many situational features (transition, stage, role, relationship state...) a
// precedent must share with the episode to count as situationally rather than textually similar.
const MinSharedFeatures = 2

func noPrecedent(ep Episode, gate string) Result {
	r := Unmeasured(ep, gate, "no precedent: retrieval returned no earlier case for this situation")
	r.Observed = "0 precedents"
	r.EvidenceRefs = appendRefs(nil, ep.resolvable([]Ref{anchor(ep)}))
	return r
}

// GradeB5 judges precedent retrieval: no future leakage (deterministic, every created_at strictly before
// the episode), situational rather than textual relevance (deterministic), and misses and relevance
// (one recorded model judgment). No precedent is UNKNOWN with the reason "no precedent".
func GradeB5(ep Episode, js map[string]Judgment) Result {
	if len(ep.Precedents) == 0 {
		return noPrecedent(ep, "B5")
	}
	as := []Assertion{b5Leakage(ep), b5Situational(ep), modelAssertion(ep, js, "B5", "relevance_and_misses")}
	return rollup(ep, "B5", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b5Leakage(ep Episode) Assertion {
	const name = "no_future_leakage"
	var bad []Ref
	for _, p := range ep.Precedents {
		if !p.CreatedAt.Before(ep.At) {
			bad = append(bad, Ref{ClaimID: p.ID, Note: "created " + p.CreatedAt.Format(time.RFC3339)})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d precedents were created at or after the event (%s)", len(bad), ep.At.Format(time.RFC3339)),
			"the retrieval used the future", bad...)
	}
	var refs []Ref
	for _, p := range ep.Precedents {
		refs = append(refs, Ref{ClaimID: p.ID, Note: "created " + p.CreatedAt.Format(time.RFC3339)})
	}
	return pass(name, fmt.Sprintf("all %d precedents predate the event", len(ep.Precedents)), refs...)
}

func b5Situational(ep Episode) Assertion {
	const name = "situational_not_textual"
	var refs, bad []Ref
	for _, p := range ep.Precedents {
		r := Ref{ClaimID: p.ID, Note: fmt.Sprintf("%d shared features", len(p.SharedFeatures))}
		if p.TextOnlyMatch || len(p.SharedFeatures) < MinSharedFeatures {
			bad = append(bad, r)
		} else {
			refs = append(refs, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d of %d precedents match by text or share fewer than %d situational features", len(bad), len(ep.Precedents), MinSharedFeatures),
			"textually similar is not situationally relevant", bad...)
	}
	return pass(name, fmt.Sprintf("all %d precedents share at least %d situational features", len(refs), MinSharedFeatures), refs...)
}

// GradeB6 judges precedent interpretation deterministically: the lesson cites what happened rather than
// what the human chose, the customer response stays separate, and the analogy states its differences. No
// precedent is UNKNOWN with the reason "no precedent".
func GradeB6(ep Episode) Result {
	if len(ep.Precedents) == 0 {
		return noPrecedent(ep, "B6")
	}
	as := []Assertion{b6Choice(ep), b6Outcome(ep), b6Analogy(ep)}
	return rollup(ep, "B6", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b6Choice(ep Episode) Assertion {
	const name = "human_choice_not_truth"
	var refs, bad []Ref
	for _, p := range ep.Precedents {
		if p.Lesson == "" {
			continue
		}
		r := Ref{ClaimID: p.ID, Note: "lesson cites " + p.LessonCites}
		if p.LessonCites == "human_choice" {
			bad = append(bad, r)
		} else {
			refs = append(refs, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d lessons rest on the human's choice alone", len(bad)), "a human choice was treated as truth", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no precedent states a lesson")
	}
	return pass(name, "no lesson rests on a human choice alone", refs...)
}

func b6Outcome(ep Episode) Assertion {
	const name = "outcome_represented_and_separate"
	var refs, bad []Ref
	unobserved := 0
	for _, p := range ep.Precedents {
		r := Ref{ClaimID: p.ID, Note: "customer response: " + p.CustomerResponse}
		citesCustomer := p.LessonCites == "customer_response" || p.LessonCites == "both"
		switch {
		case p.CustomerResponse == "":
			unobserved++
			if p.Lesson != "" {
				bad = append(bad, r)
			}
		case p.Lesson != "" && (p.LessonCites == "" || p.LessonCites == "none" || (citesCustomer && p.CustomerResponse == "")):
			bad = append(bad, r)
		default:
			refs = append(refs, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d lessons cite no observed customer or world response", len(bad)), "the lesson has no outcome behind it", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, fmt.Sprintf("no outcome was observed for any of the %d precedents, so what happened in them is unknown", unobserved))
	}
	return pass(name, "every lesson cites an observed response kept apart from the human's choice", refs...)
}

func b6Analogy(ep Episode) Assertion {
	const name = "analogy_not_overclaimed"
	var refs, bad, soft []Ref
	for _, p := range ep.Precedents {
		r := Ref{ClaimID: p.ID, Note: "claims " + p.ClaimsSimilar}
		switch {
		case p.ClaimsSimilar == "":
			continue
		case p.ClaimsSimilar == "same" && len(p.Differences) == 0 && len(p.SharedFeatures) < 4:
			bad = append(bad, r)
		case p.ClaimsSimilar != "analogous" && len(p.Differences) == 0:
			soft = append(soft, r)
		default:
			refs = append(refs, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d precedents are called the same with no differences stated", len(bad)), "the analogy is overclaimed", bad...)
	}
	if len(soft) > 0 {
		return warn(name, fmt.Sprintf("%d precedents state no differences", len(soft)), "differences should be explicit", soft...)
	}
	if len(refs) == 0 {
		return na(name, "no precedent states how similar it is")
	}
	return pass(name, "every analogy states its differences", refs...)
}
