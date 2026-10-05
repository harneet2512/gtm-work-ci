package recompute

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// goldCase is one judgment of fixtures/recompute/e11_gold.json: a sent decision with edits, the evals on the old
// artifact, and which of them a careful reviewer says the edits invalidate and leave alone.
type goldCase struct {
	ID, Eval, Title string
	Edits           []string
	OldEvals        []struct {
		EvalType string `json:"eval_type"`
		Kind     string `json:"kind"`
		Verdict  string `json:"verdict"`
	} `json:"old_evals"`
	Invalidated  []string
	Preserved    []string
	State        string  // preserved | moved | unknown (E11.8; empty: not asserted)
	AfterVersion *int    `json:"state_after_version"`
	AfterHash    *string `json:"state_after_hash"`
}

type goldFile struct {
	LabelKind string `json:"label_kind"`
	Cases     []goldCase
}

func loadGold(t *testing.T) goldFile {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "fixtures", "recompute", "e11_gold.json"))
		if err == nil {
			var g goldFile
			if err := json.Unmarshal(raw, &g); err != nil {
				t.Fatal(err)
			}
			return g
		}
		if filepath.Dir(dir) == dir {
			t.Fatal("fixtures/recompute/e11_gold.json not found")
		}
		dir = filepath.Dir(dir)
	}
}

// kindOfEdit gives the edit the minimal literal shape derive needs.
func (c goldCase) facts(t *testing.T) facts {
	t.Helper()
	var edits []literalEdit
	for _, k := range c.Edits {
		edits = append(edits, literalEdit{Kind: k})
	}
	f := baseFacts(edits...)
	f.Old, f.New = nil, nil
	for i, o := range c.OldEvals {
		f.Old = append(f.Old, judged{ID: fmt.Sprintf("0e1a0000-0000-4000-8000-%012d", i+1), EvalType: o.EvalType, Kind: o.Kind, Verdict: o.Verdict})
	}
	if c.AfterVersion != nil {
		f.StateAfter = c.AfterVersion
	}
	if c.AfterHash != nil {
		f.HashAfter = *c.AfterHash
		if f.HashAfter == "different" {
			f.HashAfter = hashB
		}
	}
	return f
}

func evalLabels(rs []SpanRef) []string {
	var out []string
	for _, r := range rs {
		if r.Kind == EvalResult {
			out = append(out, r.Label)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

// judge compares what derive says with the gold of one case and returns the disagreements.
func (c goldCase) judge(t *testing.T) []string {
	t.Helper()
	doc, err := derive(c.facts(t))
	if err != nil {
		t.Fatalf("%s: %v", c.ID, err)
	}
	valid(t, doc)
	var invalidated []SpanRef
	for _, e := range doc.Entries {
		invalidated = append(invalidated, e.Invalidated...)
	}
	var bad []string
	if got, want := evalLabels(invalidated), sortedCopy(c.Invalidated); !slices.Equal(got, want) {
		bad = append(bad, fmt.Sprintf("invalidated %v, gold %v", got, want))
	}
	if got, want := evalLabels(doc.PreservedOverall), sortedCopy(c.Preserved); !slices.Equal(got, want) {
		bad = append(bad, fmt.Sprintf("preserved %v, gold %v", got, want))
	}
	if c.State != "" {
		got := "unknown"
		if p := doc.AccountState.Preserved; p != nil && *p {
			got = "preserved"
		} else if p != nil {
			got = "moved"
		}
		listed := len(refsOf(doc.PreservedOverall, AccountState)) == 1
		if got != c.State || listed != (c.State == "preserved") {
			bad = append(bad, fmt.Sprintf("state %s (listed as preserved: %v), gold %s", got, listed, c.State))
		}
	}
	return bad
}

// TestE11GoldAgreement is the committed agreement report of E11.1 (dependency invalidation correctness) and E11.8
// (unrelated-state preservation) against the dependency table: every gold case must agree, and a disagreement says
// whether the table or the gold is wrong. The numbers are logged so they are reproducible from the repo.
func TestE11GoldAgreement(t *testing.T) {
	g := loadGold(t)
	if g.LabelKind != "model labels, not human labels" {
		t.Fatalf("the gold must say what kind of labels it holds, got %q", g.LabelKind)
	}
	perEval := map[string][2]int{}
	for _, c := range g.Cases {
		agree := 0
		if bad := c.judge(t); len(bad) == 0 {
			agree = 1
		} else {
			t.Errorf("%s (%s): %s", c.ID, c.Title, strings.Join(bad, "; "))
		}
		n := perEval[c.Eval]
		perEval[c.Eval] = [2]int{n[0] + agree, n[1] + 1}
	}
	for _, id := range []string{"E11.1", "E11.8"} {
		n := perEval[id]
		if n[1] < 4 {
			t.Fatalf("%s has %d gold cases, want at least 4", id, n[1])
		}
		t.Logf("%s agreement with the dependency table: %d/%d", id, n[0], n[1])
	}
}

// Every eval named in the gold with a declared dependency is in the table: the gold exercises the table, not the
// default for unknown evals (that default has its own case).
func TestGoldExercisesTheDeclaredTable(t *testing.T) {
	declared, undeclared := 0, 0
	for _, c := range loadGold(t).Cases {
		for _, o := range c.OldEvals {
			if _, ok := evalReads[o.EvalType]; ok {
				declared++
			} else {
				undeclared++
			}
		}
	}
	if declared < 20 || undeclared != 1 {
		t.Errorf("gold evals: %d declared, %d undeclared; want at least 20 declared and exactly the one conservative-default case", declared, undeclared)
	}
}
