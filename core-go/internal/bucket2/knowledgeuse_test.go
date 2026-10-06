package bucket2

import (
	"strings"
	"testing"
)

func TestKnowledgeUseReportsThreeSeparateValues(t *testing.T) {
	rep, r := MeasureKnowledgeUse(KnowledgeUseInput{EpisodeID: "e", Retrieved: []string{"k1", "k2", "k3"}, Applicable: []string{"k1", "k2"},
		Offered: []string{"k1", "k2"}, Cited: []string{"k1", "k9"}})
	if len(rep.Retrieved) != 3 || len(rep.Applicable) != 2 || len(rep.Used) != 1 || rep.Used[0] != "k1" {
		t.Fatalf("report = %+v", rep)
	}
	if r.Verdict != Pass || r.EvidenceRefs[0] != "knowledge:k1" {
		t.Fatalf("result = %+v", r)
	}
	if strings.Contains(strings.ToLower(r.Observed+r.Why), "influence") {
		t.Fatal("the result must never call use influence")
	}
}

func TestKnowledgeUsedRequiresOfferedAndCitedAndApplicable(t *testing.T) {
	cases := []struct {
		name string
		in   KnowledgeUseInput
		used int
		v    Verdict
	}{
		{"cited but never offered", KnowledgeUseInput{Applicable: []string{"k"}, Offered: nil, Cited: []string{"k"}}, 0, Warn},
		{"offered but never cited", KnowledgeUseInput{Applicable: []string{"k"}, Offered: []string{"k"}, Cited: nil}, 0, Warn},
		{"cited and offered but not applicable", KnowledgeUseInput{Retrieved: []string{"k"}, Offered: []string{"k"}, Cited: []string{"k"}}, 0, Unknown},
		{"nothing at all", KnowledgeUseInput{}, 0, Unknown},
	}
	for _, c := range cases {
		c.in.EpisodeID = "e"
		rep, r := MeasureKnowledgeUse(c.in)
		if len(rep.Used) != c.used || r.Verdict != c.v {
			t.Errorf("%s: used=%d verdict=%s, want %d/%s", c.name, len(rep.Used), r.Verdict, c.used, c.v)
		}
		if r.Verdict != Unknown && len(r.EvidenceRefs) == 0 {
			t.Errorf("%s: verdict without evidence", c.name)
		}
	}
}
