package knowledge

import "testing"

func TestLintFlagsStructureWrittenAsText(t *testing.T) {
	kn := k([]Condition{cond("blockers", OpContains, "security review")},
		exc("budget approved", cond("blockers", OpContains, "budget approval blocker with status resolved")),
		exc("rep owes a deliverable", cond("current_commitments", OpContains, "open item owned by our rep")),
		exc("structured", cond("blockers", OpContains, map[string]any{"text": "budget", "status": "resolved"})),
		exc("scalar prose", cond("next_milestone", OpContains, "customer-stated later date")),
	)
	got := Lint(kn)
	want := []LintFinding{
		{Where: "budget approved", Rule: "status_in_text"},
		{Where: "rep owes a deliverable", Rule: "status_in_text"},
		{Where: "rep owes a deliverable", Rule: "owner_in_text"},
	}
	if len(got) != len(want) {
		t.Fatalf("findings = %+v", got)
	}
	for i, w := range want {
		if got[i].Where != w.Where || got[i].Rule != w.Rule || got[i].Message == "" || got[i].Condition == "" {
			t.Fatalf("finding %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestLintLeavesPlainTextAlone(t *testing.T) {
	kn := k([]Condition{cond("motion", OpEq, "expansion")},
		exc("reviewer asked", cond("current_commitments", OpContains, "invite for the security review")))
	kn.ApplicabilityConditions = []Condition{cond("blockers", OpContains, "residency")}
	if got := Lint(kn); len(got) != 0 {
		t.Fatalf("findings = %+v", got)
	}
}
