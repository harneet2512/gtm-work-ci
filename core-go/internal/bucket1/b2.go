package bucket1

import (
	"fmt"
	"strings"
)

func (ep Episode) person(id string) (Person, bool) {
	for _, p := range ep.People {
		if p.ID == id {
			return p, true
		}
	}
	return Person{}, false
}

func (ep Episode) opportunity(id string) (Opportunity, bool) {
	for _, o := range ep.Opportunities {
		if o.ID == id {
			return o, true
		}
	}
	return Opportunity{}, false
}

// internalEmail reports whether the address belongs to one of the workspace's own domains.
func (ep Episode) internalEmail(email string) bool {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	return contains(ep.InternalDomains, strings.ToLower(email[at+1:]))
}

// GradeB2 judges identity and linkage, deterministically: the right person, account and opportunity,
// internal versus external, abstention when ambiguous, no cross-account contamination, provenance kept.
func GradeB2(ep Episode) Result {
	if len(ep.Resolutions) == 0 {
		return Unmeasured(ep, "B2", "the episode recorded no resolution of its activities to people, accounts or opportunities")
	}
	as := []Assertion{b2Person(ep), b2Opportunity(ep), b2Internal(ep), b2Ambiguity(ep), b2CrossAccount(ep), b2Provenance(ep)}
	return rollup(ep, "B2", JudgedObject{Type: "Episode", ID: ep.ID}, as)
}

func b2Person(ep Episode) Assertion {
	const name = "person_resolved"
	var refs, bad []Ref
	for _, r := range ep.Resolutions {
		if r.PersonID == "" {
			continue
		}
		ref := Ref{ActivityID: r.ActivityID, Note: "person " + r.PersonID}
		p, ok := ep.person(r.PersonID)
		if ok && (p.Internal || p.AccountID == r.AccountID) {
			refs = append(refs, ref)
		} else {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d resolutions name a person who is unknown or belongs to another account", len(bad)), "wrong person or account", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "every resolution abstained")
	}
	return pass(name, fmt.Sprintf("%d people belong to the account they were resolved into", len(refs)), refs...)
}

func b2Opportunity(ep Episode) Assertion {
	const name = "account_opportunity_correct"
	var refs, bad []Ref
	for _, r := range ep.Resolutions {
		ref := Ref{ActivityID: r.ActivityID, Note: "account " + r.AccountID + " opportunity " + r.OpportunityID}
		act, ok := ep.activity(r.ActivityID)
		okAcc := ok && act.AccountID == r.AccountID
		okOpp := true
		if r.OpportunityID != "" {
			o, found := ep.opportunity(r.OpportunityID)
			okOpp = found && o.AccountID == r.AccountID
		}
		if okAcc && okOpp {
			refs = append(refs, ref)
		} else {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d resolutions disagree with the activity's account or the opportunity's account", len(bad)), "account or opportunity linkage is wrong", bad...)
	}
	return pass(name, fmt.Sprintf("%d activities linked to their own account and opportunity", len(refs)), refs...)
}

func b2Internal(ep Episode) Assertion {
	const name = "internal_external_correct"
	var refs, bad []Ref
	for _, r := range ep.Resolutions {
		p, ok := ep.person(r.PersonID)
		if r.PersonID == "" || !ok {
			continue
		}
		ref := Ref{ActivityID: r.ActivityID, Note: "person " + p.ID}
		if r.Internal == p.Internal && p.Internal == ep.internalEmail(p.Email) {
			refs = append(refs, ref)
		} else {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d people are classified against their email domain", len(bad)), "internal or external is wrong", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no resolved person to classify")
	}
	return pass(name, fmt.Sprintf("%d people classified consistently with the directory and the email domain", len(refs)), refs...)
}

func b2Ambiguity(ep Episode) Assertion {
	const name = "ambiguous_identity_abstains"
	var refs, bad []Ref
	for _, r := range ep.Resolutions {
		if len(r.Candidates) < 2 {
			continue
		}
		ref := Ref{ActivityID: r.ActivityID, Note: fmt.Sprintf("%d candidates", len(r.Candidates))}
		if r.PersonID == "" {
			refs = append(refs, ref)
		} else {
			bad = append(bad, ref)
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d ambiguous identities were guessed", len(bad)), "must abstain when more than one person matches", bad...)
	}
	if len(refs) == 0 {
		return na(name, "no activity matched more than one person")
	}
	return pass(name, fmt.Sprintf("%d ambiguous identities abstained", len(refs)), refs...)
}

func b2CrossAccount(ep Episode) Assertion {
	const name = "no_cross_account_contamination"
	var bad []Ref
	for _, c := range ep.Claims {
		if a, ok := ep.activity(c.ActivityID); c.AccountID != ep.AccountID || (ok && a.AccountID != c.AccountID) {
			bad = append(bad, Ref{ActivityID: c.ActivityID, ClaimID: c.ID})
		}
	}
	for _, c := range ep.PriorClaims {
		if c.AccountID != ep.AccountID {
			bad = append(bad, Ref{ClaimID: c.ID, Note: "prior claim of account " + c.AccountID})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d claims belong to another account", len(bad)), "context from another account entered this account's graph", bad...)
	}
	var refs []Ref
	for _, c := range ep.Claims {
		refs = append(refs, Ref{ActivityID: c.ActivityID, ClaimID: c.ID})
	}
	if len(refs) == 0 {
		return na(name, "the episode has no claims to attribute to an account")
	}
	return pass(name, "every claim, new and prior, belongs to the episode's account", refs...)
}

func b2Provenance(ep Episode) Assertion {
	const name = "source_mapping_retained"
	var refs, bad []Ref
	for _, r := range ep.Resolutions {
		if r.PersonID == "" {
			continue
		}
		if r.Mapping == "" {
			bad = append(bad, Ref{ActivityID: r.ActivityID})
		} else {
			refs = append(refs, Ref{ActivityID: r.ActivityID, Note: r.Mapping})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d resolutions keep no source mapping", len(bad)), "provenance of the identity match is lost", bad...)
	}
	if len(refs) == 0 {
		return unknown(name, "no resolved person")
	}
	return pass(name, fmt.Sprintf("%d resolutions keep their source mapping", len(refs)), refs...)
}
