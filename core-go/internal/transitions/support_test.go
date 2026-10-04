package transitions

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadParity(t *testing.T) (parityFile, map[string]RuleSet) {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, "fixtures/transitions/parity_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file parityFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	sets := map[string]RuleSet{"v1": loadRules(t)}
	for name, doc := range file.Rulesets {
		rs, err := ParseRules(doc)
		if err != nil {
			t.Fatal(err)
		}
		sets[name] = rs
	}
	return file, sets
}

// recordOf is the transition the detector would write for a case (nil when none).
func recordOf(t *testing.T, rs RuleSet, c parityCase) *Record {
	t.Helper()
	out := Evaluate(rs, c.Input)
	computed := c.Input.Now
	if c.Input.ComputedAt != nil {
		computed = *c.Input.ComputedAt
	}
	current := c.Input.State.RelationshipState.Value
	if current == "" {
		current = "unknown"
	}
	var prev *Record
	if c.Input.Open != nil {
		to := c.Input.Open.ToStateCandidate
		prev = &Record{Status: c.Input.Open.Status, LastUpdatedAt: c.Input.Open.LastUpdatedAt, FirstObservedAt: c.Input.Open.LastUpdatedAt,
			SupportingFacts: []FactResult{}, MissingFacts: []FactResult{}, ContradictingFacts: []FactResult{}, FromState: current}
		if to != "" {
			prev.ToStateCandidate = &to
		}
	}
	rec, _, err := Plan(prev, out, Meta{AccountID: "a", CurrentState: current, StateVersion: 1, RuleSetVersion: rs.Version, AsOf: c.Input.Now,
		ComputedAt: computed, TriggerActivityIDs: []string{"t"}})
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	return rec
}

func TestEveryDetectorRecordIsSupportedByItsOwnEvidence(t *testing.T) {
	file, sets := loadParity(t)
	checked := 0
	for _, c := range file.Cases {
		rs := sets[c.Rules]
		rec := recordOf(t, rs, c)
		if rec == nil {
			continue
		}
		checked++
		if v := CheckSupport(rs, c.Input, *rec); !v.Pass {
			t.Errorf("%s: a record the detector wrote failed its own support check: %v", c.Name, v.Reasons)
		}
	}
	if checked < 300 {
		t.Fatalf("only %d records checked", checked)
	}
}

func firstCase(t *testing.T, status string) (RuleSet, parityCase, Record) {
	t.Helper()
	file, sets := loadParity(t)
	for _, c := range file.Cases {
		if c.Rules != "v1" || c.Expected.Status == nil || *c.Expected.Status != status {
			continue
		}
		if rec := recordOf(t, sets["v1"], c); rec != nil && rec.Status == status {
			return sets["v1"], c, *rec
		}
	}
	t.Fatalf("no parity case yields %s", status)
	return RuleSet{}, parityCase{}, Record{}
}

func TestTamperedRecordsAreNotSupported(t *testing.T) {
	rs, c, cand := firstCase(t, StatusCandidate)
	promote := cand
	promote.Status = StatusConfirmed
	retarget := cand
	other := "RECOVERY"
	retarget.ToStateCandidate = &other
	fabricated := cand
	fabricated.SupportingFacts = append([]FactResult{}, cand.SupportingFacts...)
	fabricated.SupportingFacts[0].EvidenceRefs = []Ref{{ActivityID: "0ac70000-0000-4000-8000-0000000000ff", ClaimID: "c1a10000-0000-4000-8000-0000000000ff"}}
	invented := cand
	invented.SupportingFacts = append(append([]FactResult{}, cand.SupportingFacts...), FactResult{Key: "owner_stabilized", Satisfied: true, Required: true,
		EvidenceRefs: []Ref{{ActivityID: "0ac70000-0000-4000-8000-000000000101"}}})
	cases := map[string]struct {
		rec  Record
		want string
	}{
		"promoted without the evidence":   {promote, "not what the rules derive"},
		"re-targeted":                     {retarget, "not what the rules derive"},
		"fabricated evidence":             {fabricated, "cites evidence"},
		"a fact the evidence cannot earn": {invented, "owner_stabilized"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			v := CheckSupport(rs, c.Input, tc.rec)
			if v.Pass || !strings.Contains(strings.Join(v.Reasons, " | "), tc.want) {
				t.Fatalf("verdict = %+v, want a failure mentioning %q", v, tc.want)
			}
		})
	}
	if v := CheckSupport(rs, c.Input, cand); !v.Pass {
		t.Fatalf("the untampered record must pass: %v", v.Reasons)
	}
}

func TestADroppedContradictionIsNotSupported(t *testing.T) {
	file, sets := loadParity(t)
	for _, c := range file.Cases {
		rs := sets[c.Rules]
		rec := recordOf(t, rs, c)
		if rec == nil || len(rec.ContradictingFacts) == 0 || c.Rules != "v1" {
			continue
		}
		dropped := *rec
		dropped.ContradictingFacts = []FactResult{}
		if v := CheckSupport(rs, c.Input, dropped); v.Pass {
			t.Fatalf("%s: dropping a preserved contradiction must fail the support check", c.Name)
		}
		return
	}
	t.Skip("no parity case carries a contradiction")
}

func TestAStaleClosureIsSupportedOnlyWhenTheRulesCloseIt(t *testing.T) {
	rs := loadRules(t)
	in := Input{State: State{RelationshipState: RelationshipState{Value: "unknown"}}, Now: asOf}
	closed := Record{Status: StatusUnresolved, ClosedAt: &asOf, SupportingFacts: []FactResult{}, MissingFacts: []FactResult{}, ContradictingFacts: []FactResult{}}
	for _, r := range rs.Transitions {
		if !contains(r.FromStates, "unknown") {
			continue
		}
		for _, f := range r.Facts {
			closed.MissingFacts = append(closed.MissingFacts, FactResult{Key: f.Key, Description: f.Description, Required: f.Required, EvidenceRefs: []Ref{}, SignalIDs: []string{}})
		}
	}
	if v := CheckSupport(rs, in, closed); !v.Pass {
		t.Errorf("a stale UNRESOLVED the rules close must be supported: %v", v.Reasons)
	}
	promoted := closed
	to := "EXPANSION"
	promoted.Status, promoted.ToStateCandidate = StatusConfirmed, &to
	if v := CheckSupport(rs, in, promoted); v.Pass {
		t.Error("a closed record that claims CONFIRMED must fail")
	}
}
