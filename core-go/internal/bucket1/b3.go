package bucket1

import "fmt"

func (ep Episode) anyClaim(id string) (Claim, bool) {
	for _, c := range append(append([]Claim(nil), ep.Claims...), ep.PriorClaims...) {
		if c.ID == id {
			return c, true
		}
	}
	return Claim{}, false
}

// GradeB3 judges prior-context integration over the claim graph: supersession justified, chronology
// respected, no stale or contradicting prior fact silently carried forward, unrelated state preserved; the
// supporting/conflicting links are one recorded model judgment.
func GradeB3(ep Episode, js map[string]Judgment) Result {
	if len(ep.Claims) == 0 && len(ep.PriorClaims) == 0 {
		return Unmeasured(ep, "B3", "the episode has no claim graph to integrate")
	}
	as := []Assertion{b3Supersession(ep), b3Chronology(ep), b3Conflicts(ep), b3Unrelated(ep),
		modelAssertion(ep, js, "B3", "supporting_and_conflicting_links")}
	return rollup(ep, "B3", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b3Supersession(ep Episode) Assertion {
	const name = "supersession_justified"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		if c.Supersedes == "" {
			continue
		}
		ref := Ref{ActivityID: c.ActivityID, ClaimID: c.ID}
		prior, ok := ep.anyClaim(c.Supersedes)
		if ok && prior.Field == c.Field && c.SupersessionReason != "" {
			refs = append(refs, ref)
		} else {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d supersessions are unjustified", len(bad)),
			"a claim supersedes one that is missing, about another field, or gives no reason", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no claim supersedes another")
	}
	return pass(name, fmt.Sprintf("%d supersessions name the same field and give a reason", len(refs)), refs...)
}

func b3Chronology(ep Episode) Assertion {
	const name = "chronology_respected"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		if c.Supersedes == "" {
			continue
		}
		ref := Ref{ActivityID: c.ActivityID, ClaimID: c.ID}
		if prior, ok := ep.anyClaim(c.Supersedes); ok && c.OccurredAt.Before(prior.OccurredAt) {
			bad = append(bad, ref)
		} else {
			refs = append(refs, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d older claims supersede newer ones", len(bad)), "chronology was not respected", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no supersession to order")
	}
	return pass(name, fmt.Sprintf("%d supersessions go from older to newer", len(refs)), refs...)
}

// b3Conflicts: a new active claim that disagrees with a still-active prior claim on a field must supersede
// it or be linked as a conflict; otherwise the stale fact is silently carried forward.
func b3Conflicts(ep Episode) Assertion {
	const name = "conflicts_surfaced"
	var refs, bad []Ref
	for _, n := range ep.Claims {
		if n.Status == "rejected" {
			continue
		}
		for _, p := range ep.PriorClaims {
			if p.Field != n.Field || p.Status != "active" || norm(p.Value) == norm(n.Value) || !scalarFields[n.Field] {
				continue
			}
			if ep.StateUnread {
				return unknown(name, "a new claim disagrees with an older active claim, but the account state after the event was not read, so whether the old claim still wins its field is unknown")
			}
			if ep.WinningClaims != nil && ep.WinningClaims[p.Field] != p.ID {
				continue // the old claim no longer wins its field: it is not carried forward
			}
			r := []Ref{{ActivityID: n.ActivityID, ClaimID: n.ID}, {ClaimID: p.ID}}
			if n.Supersedes == p.ID || contains(n.ConflictsWith, p.ID) {
				refs = append(refs, r...)
			} else {
				bad = append(bad, r...)
			}
		}
	}
	if len(bad) > 0 {
		return fail(name, "a new claim disagrees with an active prior claim and neither supersedes nor conflicts with it", "a stale or contradicted fact was silently carried forward", bad...)
	}
	if len(refs) == 0 {
		if len(ep.Claims) == 0 || len(ep.PriorClaims) == 0 {
			return na(name, "no new claim or no prior claim to compare")
		}
		var seen []Ref
		for _, n := range ep.Claims {
			seen = append(seen, Ref{ActivityID: n.ActivityID, ClaimID: n.ID})
		}
		return pass(name, "no new claim disagrees with an active prior claim", seen...)
	}
	return pass(name, fmt.Sprintf("%d disagreements are linked", len(refs)/2), refs...)
}

// b3Unrelated: prior claims about fields no new claim speaks about keep their status.
func b3Unrelated(ep Episode) Assertion {
	const name = "unrelated_state_preserved"
	if ep.PriorStatusAfter == nil {
		return unknown(name, "the prior claims' statuses after the event were not read")
	}
	var bad, kept []Ref
	untouched := 0
	for _, p := range ep.PriorClaims {
		if contains(ep.NewClaimFields, p.Field) {
			continue
		}
		untouched++
		kept = append(kept, Ref{ClaimID: p.ID})
		if after, ok := ep.PriorStatusAfter[p.ID]; ok && after != p.Status {
			bad = append(bad, Ref{ClaimID: p.ID, Note: p.Status + " became " + after})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d prior claims about untouched fields changed status", len(bad)), "unrelated state was altered", bad...)
	}
	if untouched == 0 {
		return na(name, "every prior claim concerns a field this event touches")
	}
	return pass(name, fmt.Sprintf("%d prior claims about untouched fields keep their status", untouched), kept...)
}
