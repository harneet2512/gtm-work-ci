package bucket1run

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

func TestCriteriaOfKeepsEveryAssertionAndItsOwnResult(t *testing.T) {
	as := []bucket1.Assertion{
		{Name: "no_future_leak", Verdict: bucket1.Pass, Why: "holds", Refs: []bucket1.Ref{{ActivityID: "a1"}, {ClaimID: "c1"}}},
		{Name: "relevant_precedent_found", Verdict: "abstain", Why: "not measured"},
		{Name: "unsourced_pass", Verdict: bucket1.Pass, Why: "holds"},
		{Name: "irrelevant_ranked_first", Verdict: bucket1.NotApplicable, Why: "no precedents"},
		{Name: "weird", Verdict: "great", Why: "x", Refs: []bucket1.Ref{{Quote: "only words"}}},
	}
	got := criteriaOf(as)
	if len(got) != 5 {
		t.Fatalf("criteria = %+v", got)
	}
	want := []struct{ id, label, result string }{
		{"no_future_leak", "No future leak", "pass"},
		{"relevant_precedent_found", "Relevant precedent found", "unknown"}, // abstain reads as unknown
		{"unsourced_pass", "Unsourced pass", "unknown"},                     // rule R1 per criterion: a pass with no evidence is unknown
		{"irrelevant_ranked_first", "Irrelevant ranked first", "not_applicable"},
		{"weird", "Weird", "unknown"}, // an unreadable verdict is never a pass
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Label != w.label || got[i].Result != w.result {
			t.Errorf("criterion %d = %+v, want %+v", i, got[i], w)
		}
	}
	if len(got[0].EvidenceRefs) != 2 || got[0].EvidenceRefs[0] != "activity:a1" || got[0].EvidenceRefs[1] != "claim:c1" {
		t.Errorf("evidence = %v", got[0].EvidenceRefs)
	}
	if got[4].EvidenceRefs == nil || len(got[4].EvidenceRefs) != 0 {
		t.Errorf("a quote alone is not resolvable evidence: %#v", got[3].EvidenceRefs)
	}
}

func TestCriteriaOfNoAssertions(t *testing.T) {
	if got := criteriaOf(nil); got == nil || len(got) != 0 {
		t.Fatalf("want an empty, non-nil list, got %#v", got)
	}
}
