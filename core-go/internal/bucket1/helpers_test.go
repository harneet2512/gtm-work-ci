package bucket1

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)

// goodEpisode is a small coherent episode every deterministic gate passes; each test breaks one thing.
func goodEpisode() Episode {
	return Episode{
		ID: "ep-13", Name: "MedTech Event 13", At: t0, AccountID: "acc-1", OpportunityID: "opp-1",
		InternalDomains: []string{"seller.example"},
		Activities: []Activity{{ID: "a1", Source: "email", ExternalID: "m-1", SpeakerID: "p-ext", AccountID: "acc-1", OccurredAt: t0,
			Text: "Hi team, legal review is blocked until the DPA is signed.  We can start the pilot on March 20."}},
		Claims: []Claim{
			{ID: "c1", ActivityID: "a1", AccountID: "acc-1", Kind: "fact", Field: "blockers", Value: "DPA unsigned", SpeakerID: "p-ext", OccurredAt: t0,
				Quote: "legal review is blocked until the DPA is signed", Status: "active"},
			{ID: "c2", ActivityID: "a1", AccountID: "acc-1", Kind: "fact", Field: "next_milestone", Value: "pilot 2026-03-20", SpeakerID: "p-ext", OccurredAt: t0,
				Quote: "We can start the pilot on March 20", Status: "active"},
			{ID: "c3", ActivityID: "a1", AccountID: "acc-1", Kind: "inference", Field: "health", Value: "at risk", SpeakerID: "p-ext", OccurredAt: t0, Status: "active"},
		},
		CriticalFacts: []string{"DPA is signed", "pilot on March 20"},
		People: []Person{{ID: "p-ext", AccountID: "acc-1", Email: "lee@medtech.example"},
			{ID: "p-int", Email: "rep@seller.example", Internal: true}},
		Opportunities: []Opportunity{{ID: "opp-1", AccountID: "acc-1"}},
		Resolutions: []Resolution{{ActivityID: "a1", PersonID: "p-ext", AccountID: "acc-1", OpportunityID: "opp-1",
			Candidates: []string{"p-ext"}, Mapping: "from: lee@medtech.example"}},
		PriorClaims: []Claim{{ID: "pc1", AccountID: "acc-1", Kind: "fact", Field: "blockers", Value: "none", OccurredAt: t0.Add(-72 * time.Hour), Status: "superseded"},
			{ID: "pc2", AccountID: "acc-1", Kind: "fact", Field: "owner", Value: "Sam", OccurredAt: t0.Add(-72 * time.Hour), Status: "active"}},
		StateDiff:       []FieldChange{{Field: "blockers", Before: "none", After: "DPA unsigned", Material: true, Refs: []Ref{{ActivityID: "a1", ClaimID: "c1"}}}},
		GraphDiffFields: []string{"blockers"}, GraphDiffKnown: true,
		NewClaimFields: []string{"blockers", "next_milestone", "health"},
	}
}

func byName(t *testing.T, r Result, name string) Assertion {
	t.Helper()
	for _, a := range r.Assertions {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no assertion %q in %+v", name, r.Assertions)
	return Assertion{}
}

func wantVerdict(t *testing.T, a Assertion, v string) {
	t.Helper()
	if a.Verdict != v {
		t.Fatalf("%s = %s (%s), want %s", a.Name, a.Verdict, a.Why, v)
	}
}
