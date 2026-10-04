package biwriter_test

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
)

// The business reading of a change (why_it_matters), the label of a claim that rests on the event itself, and the
// unchanged transition: the text side of Build.

// why_it_matters is a business reading, not pipeline diagnostics: no signal types, no trigger reason codes, no
// "eligible for an agent run".
func TestWhyItMattersCarriesNoPipelineDiagnostics(t *testing.T) {
	f := facts() // its signals and trigger evaluation are set
	why := build(t, f).BI.WhyItMatters
	for _, banned := range []string{"security_blocker_appeared", "new_stakeholder_entered", "eligible_blocker_change", "eligible_stakeholder_change",
		"agent run", "Trigger", "Signals raised", "reason"} {
		if strings.Contains(why, banned) {
			t.Errorf("why_it_matters leaks pipeline diagnostics (%q): %s", banned, why)
		}
	}
}

// Every dimension has its own commercial reading.
func TestEveryDimensionHasItsOwnReading(t *testing.T) {
	cases := map[string]struct {
		entry biwriter.DiffEntry
		want  string
	}{
		"stakeholder_structure":  {entry("economic_buyer", "set", nil, "Marco Ruiz"), "The buying group moved"},
		"relationship_ownership": {entry("owner", "changed", "Dana", "Lee"), "Ownership of the relationship moved"},
		"buyer_intent":           {entry("stage", "changed", "Discovery", "Negotiation"), "The buyer's position moved"},
		"blockers_risk":          {entry("health", "changed", "green", "red"), "The risk picture moved"},
		"next_step_commitment":   {entry("next_milestone", "set", nil, "security review"), "The next step moved"},
	}
	readings := map[string]string{}
	for dim, tc := range cases {
		f := facts()
		f.Diff.Entries = []biwriter.DiffEntry{tc.entry}
		bi := build(t, f).BI
		if bi.Claims[0].Dimension != dim {
			t.Fatalf("%s: dimension = %s", tc.entry.Field, bi.Claims[0].Dimension)
		}
		if !strings.Contains(bi.WhyItMatters, tc.want) || !strings.Contains(bi.WhyItMatters, biwriterLabel(tc.entry.Field)) {
			t.Errorf("%s: why_it_matters %q lacks %q or the cited fact", dim, bi.WhyItMatters, tc.want)
		}
		readings[dim] = bi.WhyItMatters
	}
	seen := map[string]string{}
	for dim, why := range readings {
		if other, dup := seen[why]; dup {
			t.Errorf("%s and %s share a reading: %s", dim, other, why)
		}
		seen[why] = dim
	}
}

func biwriterLabel(field string) string {
	return map[string]string{"economic_buyer": "Economic buyer", "owner": "Relationship owner", "stage": "Stage", "health": "Account health", "next_milestone": "Next milestone"}[field]
}

func TestAnEntryWithoutEvidenceIsCitedThroughTheTriggerActivity(t *testing.T) {
	f := facts()
	f.Diff.Entries = []biwriter.DiffEntry{entry("stage", "changed", "Discovery", "Negotiation")}
	r := build(t, f)
	refs := r.BI.Claims[0].EvidenceRefs
	if len(refs) != 1 || refs[0].ActivityID != actN || refs[0].Quote != "" {
		t.Fatalf("refs = %+v, want the trigger activity without an invented quote", refs)
	}
	if st := r.BI.Claims[0].Statement; !strings.Contains(st, "rests on the triggering event itself") {
		t.Fatalf("a claim with no evidence of its own must say so: %q", st)
	}
}

func TestAClaimWithItsOwnEvidenceIsNotLabelledSelfCited(t *testing.T) {
	for _, c := range build(t, facts()).BI.Claims {
		if strings.Contains(c.Statement, "triggering event itself") {
			t.Errorf("claim %q has evidence of its own and must not carry the label", c.Statement)
		}
	}
}

func TestTheSelfCitedLabelNeverPushesAStatementOverTheLimit(t *testing.T) {
	f := facts()
	f.Diff.Entries = []biwriter.DiffEntry{entry("blockers", "changed", items(strings.Repeat("a", 400)), items(strings.Repeat("b", 400)))}
	st := build(t, f).BI.Claims[0].Statement
	if n := len([]rune(st)); n > 500 || !strings.HasSuffix(st, "triggering event itself.)") {
		t.Fatalf("statement of %d runes: %q", n, st)
	}
}

// An open transition the event did not touch is still shown, labelled unchanged; the reading does not claim the
// event moved it.
func TestAnOpenTransitionTheEventDidNotTouchIsShownAsUnchanged(t *testing.T) {
	f := facts()
	f.Transition = &biwriter.Transition{ID: "t3", Status: "CANDIDATE", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: false,
		Missing: []biwriter.Fact{{Key: "owner_stabilized", Description: "A champion is stable.", Required: true}}}
	bi := build(t, f).BI
	if bi.Transition == nil || bi.Transition.TouchedByEvent || bi.Transition.Status != "CANDIDATE" || len(bi.Transition.MissingFacts) != 1 {
		t.Fatalf("transition = %+v, want the open transition with touched_by_event=false", bi.Transition)
	}
	if strings.Contains(bi.WhyItMatters, "The relationship") {
		t.Errorf("an untouched transition is not part of the reading: %s", bi.WhyItMatters)
	}
	f.Transition.Touched = true
	if got := build(t, f).BI.Transition; !got.TouchedByEvent {
		t.Fatalf("a touched transition says so: %+v", got)
	}
}
