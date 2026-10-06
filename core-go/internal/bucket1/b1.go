package bucket1

import (
	"fmt"
	"strings"
)

// norm collapses whitespace and case so a quote that differs from the source only by spacing is not corruption.
func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func (ep Episode) activity(id string) (Activity, bool) {
	for _, a := range ep.Activities {
		if a.ID == id {
			return a, true
		}
	}
	return Activity{}, false
}

// GradeB1 judges evidence fidelity: source preserved, speaker and date correct, fact separated from
// inference, nothing critical omitted, contradictions preserved, no double count. All deterministic over the
// activities and claims except the inference boundary, which is one recorded model judgment.
func GradeB1(ep Episode, js map[string]Judgment) Result {
	if len(ep.Activities) == 0 {
		return Unmeasured(ep, "B1", "the episode has no activities to compare the claims with")
	}
	as := []Assertion{
		b1Source(ep), b1Speaker(ep), b1Dates(ep), b1FactInference(ep), b1Critical(ep), b1Contradictions(ep), b1DoubleCount(ep),
		modelAssertion(ep, js, "B1", "inference_boundary"),
	}
	return rollup(ep, "B1", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b1Source(ep Episode) Assertion {
	const name = "source_preserved"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		if c.Quote == "" {
			continue
		}
		a, ok := ep.activity(c.ActivityID)
		r := Ref{ActivityID: c.ActivityID, ClaimID: c.ID, Quote: c.Quote}
		if ok && strings.Contains(norm(a.Text), norm(c.Quote)) {
			refs = append(refs, r)
		} else {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d of %d quotes are not in their source", len(bad), len(bad)+len(refs)),
			"a claim quotes text its activity does not contain", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no claim carries a quote to compare with the source")
	}
	return pass(name, fmt.Sprintf("%d quotes found verbatim in their activities", len(refs)), refs...)
}

func b1Speaker(ep Episode) Assertion {
	const name = "speaker_correct"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		a, ok := ep.activity(c.ActivityID)
		if !ok || c.SpeakerID == "" {
			continue
		}
		r := Ref{ActivityID: a.ID, ClaimID: c.ID}
		if c.SpeakerID == a.SpeakerID {
			refs = append(refs, r)
		} else {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d claims name a speaker other than their activity's", len(bad)), "speaker attribution differs from the source", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no claim names a speaker")
	}
	return pass(name, fmt.Sprintf("%d claims attributed to the activity's speaker", len(refs)), refs...)
}

func b1Dates(ep Episode) Assertion {
	const name = "dates_correct"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		a, ok := ep.activity(c.ActivityID)
		if !ok || c.OccurredAt.IsZero() {
			continue
		}
		r := Ref{ActivityID: a.ID, ClaimID: c.ID}
		if c.OccurredAt.Equal(a.OccurredAt) {
			refs = append(refs, r)
		} else {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d claims are dated differently from their activity", len(bad)), "claim time differs from the source time", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no claim carries a time")
	}
	return pass(name, fmt.Sprintf("%d claims carry their activity's time", len(refs)), refs...)
}

// b1FactInference: an explicit fact needs a quote from the source; a claim without one is an inference and
// must not be labelled a fact (unsupported inference cannot become a source fact).
func b1FactInference(ep Episode) Assertion {
	const name = "fact_inference_separated"
	var refs, bad []Ref
	for _, c := range ep.Claims {
		r := Ref{ActivityID: c.ActivityID, ClaimID: c.ID, Quote: c.Quote}
		if c.Kind == "fact" && c.Quote == "" { // a "record" claim (rule or CRM) is its own source and needs no quote
			bad = append(bad, r)
		} else {
			refs = append(refs, r)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d claims are labelled fact with no source quote", len(bad)), "an inference became a source fact", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no claims")
	}
	return pass(name, fmt.Sprintf("%d claims: every fact is quoted, every unquoted claim is an inference", len(refs)), refs...)
}

func b1Critical(ep Episode) Assertion {
	const name = "critical_facts_present"
	if len(ep.CriticalFacts) == 0 {
		return unknown(name, "no gold list of critical facts for this episode")
	}
	var refs []Ref
	var missing []string
	for _, f := range ep.CriticalFacts {
		hit := false
		for _, c := range ep.Claims {
			if strings.Contains(norm(c.Quote+" "+c.Value), norm(f)) {
				refs = append(refs, Ref{ActivityID: c.ActivityID, ClaimID: c.ID, Quote: c.Quote})
				hit = true
				break
			}
		}
		if !hit {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fail(name, fmt.Sprintf("%d of %d critical facts have no claim", len(missing), len(ep.CriticalFacts)),
			"omitted: "+strings.Join(missing, "; "), Ref{ActivityID: ep.Activities[0].ID})
	}
	return pass(name, fmt.Sprintf("all %d critical facts are claimed", len(ep.CriticalFacts)), refs...)
}

// b1Contradictions: two active claims about one field with different values must be linked as conflicting,
// never left as two silent truths or resolved by dropping one.
func b1Contradictions(ep Episode) Assertion {
	const name = "contradictions_preserved"
	byField := map[string][]Claim{}
	for _, c := range ep.Claims {
		if c.Status != "rejected" && c.Status != "superseded" && c.Status != "outranked" && scalarFields[c.Field] {
			byField[c.Field] = append(byField[c.Field], c)
		}
	}
	var refs, bad []Ref
	for _, cs := range byField {
		for i := range cs {
			for j := i + 1; j < len(cs); j++ {
				if norm(cs[i].Value) == norm(cs[j].Value) {
					continue
				}
				r := []Ref{{ActivityID: cs[i].ActivityID, ClaimID: cs[i].ID}, {ActivityID: cs[j].ActivityID, ClaimID: cs[j].ID}}
				if contains(cs[i].ConflictsWith, cs[j].ID) && contains(cs[j].ConflictsWith, cs[i].ID) {
					refs = append(refs, r...)
				} else {
					bad = append(bad, r...)
				}
			}
		}
	}
	if len(bad) > 0 {
		return fail(name, "two claims disagree on a field and are not linked as a conflict", "a contradiction was silently resolved", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no two claims of the episode disagree on a field")
	}
	return pass(name, "every contradiction is kept and linked", refs...)
}

func b1DoubleCount(ep Episode) Assertion {
	const name = "no_double_count"
	seen := map[string]string{}
	var bad []Ref
	for _, a := range ep.Activities {
		key := a.Source + "|" + a.ExternalID
		if a.ExternalID == "" {
			key = a.Source + "|" + a.OccurredAt.String() + "|" + norm(a.Text)
		}
		if first, dup := seen[key]; dup {
			bad = append(bad, Ref{ActivityID: first}, Ref{ActivityID: a.ID})
		} else {
			seen[key] = a.ID
		}
	}
	if len(bad) > 0 {
		return fail(name, "the same source event appears more than once", "a replayed or duplicate delivery was counted twice", bad...)
	}
	var all []Ref
	for _, a := range ep.Activities {
		all = append(all, Ref{ActivityID: a.ID})
	}
	return pass(name, fmt.Sprintf("%d activities, each a distinct source event", len(ep.Activities)), all...)
}

// scalarFields hold one value at a time: two active claims with different values are a contradiction. List fields
// (blockers, objections, commitments) legitimately hold several.
var scalarFields = map[string]bool{
	"stage": true, "health": true, "owner": true, "motion": true, "champion": true, "champion_status": true,
	"economic_buyer": true, "next_meeting": true, "next_milestone": true, "relationship_risk": true, "decision_process": true,
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
