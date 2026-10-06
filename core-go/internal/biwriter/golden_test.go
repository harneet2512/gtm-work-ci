package biwriter_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/biwriter"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// m1Text is the text of Slack Message 1 as the writer produces it: the headline, each change with the activities
// it cites, why it matters and the transition, in the order Message 1 shows them.
func m1Text(bi *biwriter.BI) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SUMMARY\n%s\n\nWHAT CHANGED\n", bi.Summary)
	for i, c := range bi.Claims {
		acts := make([]string, 0, len(c.EvidenceRefs))
		for _, e := range c.EvidenceRefs {
			acts = append(acts, e.ActivityID)
		}
		fmt.Fprintf(&b, "%d. [%s] %s (cites %s)\n", i+1, c.Dimension, c.Statement, strings.Join(acts, ", "))
	}
	fmt.Fprintf(&b, "\nWHY THIS MATTERS\n%s\n\nTRANSITION\n", bi.WhyItMatters)
	if bi.Transition == nil {
		b.WriteString("none\n")
		return b.String()
	}
	t := bi.Transition
	to := "none"
	if t.ToStateCandidate != nil {
		to = *t.ToStateCandidate
	}
	label := "touched by this event"
	if !t.TouchedByEvent {
		label = "unchanged"
	}
	fmt.Fprintf(&b, "%s %s -> %s (%s)\n", t.Status, t.FromState, to, label)
	for _, m := range t.MissingFacts {
		req := "optional"
		if m.Required {
			req = "required"
		}
		fmt.Fprintf(&b, "- missing (%s) %s: %s\n", req, m.Key, m.Description)
	}
	return b.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Fatalf("%s differs from the golden file; run with -update after reviewing:\n--- got\n%s", name, got)
	}
}

// The M1 text of a representative change (the SOC2 case with an open, not yet confirmed transition) is pinned.
func TestGoldenMessageOneText(t *testing.T) {
	f := facts()
	f.Transition = &biwriter.Transition{ID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: true,
		Missing: []biwriter.Fact{
			{Key: "owner_stabilized", Description: "A champion who took over after the reorg is active and has held the role long enough.", Required: true},
			{Key: "economic_buyer_known", Description: "The economic buyer is known.", Required: false}}}
	checkGolden(t, "m1_soc2_candidate.txt", m1Text(build(t, f).BI))
}

func TestGoldenMessageOneTextWithAnUnchangedTransition(t *testing.T) {
	f := facts()
	f.Transition = &biwriter.Transition{ID: "0e5a0000-0000-4000-8000-000000000002", Status: "CANDIDATE", FromState: "REORG", ToState: ptr("EXPANSION"), Touched: false,
		Missing: []biwriter.Fact{{Key: "owner_stabilized", Description: "A champion who took over after the reorg is active and has held the role long enough.", Required: true}}}
	checkGolden(t, "m1_soc2_unchanged_transition.txt", m1Text(build(t, f).BI))
}
