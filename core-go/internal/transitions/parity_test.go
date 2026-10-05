package transitions

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"
	"time"
)

// parityFact is a fact as the Python reference evaluator serialises it (worker-py/tests/transition_parity.py).
type parityFact struct {
	Key       string       `json:"key"`
	Required  bool         `json:"required"`
	Rejects   *bool        `json:"rejects"`
	Evidence  [][2]*string `json:"evidence"`
	SignalIDs []string     `json:"signal_ids"`
}

type parityOutcome struct {
	Status        *string      `json:"status"`
	ToState       *string      `json:"to_state"`
	RuleID        *string      `json:"rule_id"`
	Confidence    float64      `json:"confidence"`
	Closed        bool         `json:"closed"`
	ConfirmedAt   *string      `json:"confirmed_at"`
	Supporting    []parityFact `json:"supporting"`
	Missing       []parityFact `json:"missing"`
	Contradicting []parityFact `json:"contradicting"`
}

type parityCase struct {
	Name     string        `json:"name"`
	Rules    string        `json:"rules"`
	Input    Input         `json:"input"`
	Expected parityOutcome `json:"expected"`
}

type parityFile struct {
	RuleSetVersion string                     `json:"rule_set_version"`
	Rulesets       map[string]json.RawMessage `json:"rulesets"`
	Cases          []parityCase               `json:"cases"`
}

func str(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func facts(in []FactResult) []parityFact {
	out := []parityFact{}
	for _, f := range in {
		pf := parityFact{Key: f.Key, Required: f.Required, Rejects: f.Rejects, SignalIDs: f.SignalIDs, Evidence: [][2]*string{}}
		for _, r := range f.EvidenceRefs {
			pf.Evidence = append(pf.Evidence, [2]*string{str(r.ActivityID), str(r.ClaimID)})
		}
		out = append(out, pf)
	}
	return out
}

func normalizeFacts(in []parityFact) []parityFact {
	if in == nil {
		return []parityFact{}
	}
	for i := range in {
		if in[i].SignalIDs == nil {
			in[i].SignalIDs = []string{}
		}
		if in[i].Evidence == nil {
			in[i].Evidence = [][2]*string{}
		}
	}
	return in
}

func asParity(o Outcome) parityOutcome {
	p := parityOutcome{Status: str(o.Status), ToState: str(o.ToState), RuleID: str(o.RuleID), Confidence: o.Confidence, Closed: o.Closed,
		Supporting: facts(o.Supporting), Missing: facts(o.Missing), Contradicting: facts(o.Contradicting)}
	if o.ConfirmedAt != nil {
		s := o.ConfirmedAt.UTC().Format(time.RFC3339)
		p.ConfirmedAt = &s
	}
	return p
}

func TestGoDetectorAgreesWithThePythonReferenceEvaluator(t *testing.T) {
	raw, err := os.ReadFile(repoFile(t, "fixtures/transitions/parity_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file parityFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	v1 := loadRules(t)
	if file.RuleSetVersion != v1.Version {
		t.Fatalf("parity file is for %s, rule set is %s", file.RuleSetVersion, v1.Version)
	}
	sets := map[string]RuleSet{"v1": v1}
	for name, doc := range file.Rulesets {
		rs, err := ParseRules(doc)
		if err != nil {
			t.Fatalf("ruleset %s: %v", name, err)
		}
		sets[name] = rs
	}
	if len(file.Cases) < 500 {
		t.Fatalf("only %d parity cases", len(file.Cases))
	}
	statuses := map[string]int{}
	for _, c := range file.Cases {
		got := asParity(Evaluate(sets[c.Rules], c.Input))
		want := c.Expected
		want.Supporting, want.Missing, want.Contradicting = normalizeFacts(want.Supporting), normalizeFacts(want.Missing), normalizeFacts(want.Contradicting)
		if math.Abs(got.Confidence-want.Confidence) > 1e-9 {
			t.Errorf("%s: confidence %v, want %v", c.Name, got.Confidence, want.Confidence)
		}
		got.Confidence, want.Confidence = 0, 0
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: outcome differs\n got  %s\n want %s", c.Name, dump(got), dump(want))
		}
		if got.Status != nil {
			statuses[*got.Status]++
		}
	}
	for _, s := range []string{StatusConfirmed, StatusCandidate, StatusUnresolved, StatusRejected} {
		if statuses[s] == 0 {
			t.Errorf("no parity case produces %s", s)
		}
	}
	t.Logf("%d cases agree: %v", len(file.Cases), fmt.Sprint(statuses))
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
