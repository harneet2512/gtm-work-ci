package bucket2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestControlEffectFollowsWhatTheBackendDoes(t *testing.T) {
	det, mdl := Deterministic.Kind, "model"
	cases := []struct {
		name            string
		gate, sub, kind string
		v               Verdict
		want            string
	}{
		{"deterministic recipients fail refuses the send", "D8", "recipients", det, Fail, "BLOCK"},
		{"deterministic stale content fail refuses the send", "D8", "no_stale_content", det, Fail, "BLOCK"},
		{"deterministic recipients pass lets it continue", "D8", "recipients", det, Pass, "CONTINUE"},
		{"a model D8 fail is a warning, never a block", "D8", "model", mdl, Fail, "RECORD ONLY"},
		{"a model judgment under a blocking sub-gate name still does not block", "D8", "recipients", mdl, Fail, "RECORD ONLY"},
		{"other deterministic D8 sub-gates only record", "D8", "policy_gates", det, Fail, "RECORD ONLY"},
		{"D8 deterministic warn only records", "D8", "recipients", det, Warn, "RECORD ONLY"},
		{"B9 fail never blocks: lifecycle holds knowledge by its own predicate", "B9", "", mdl, Fail, "RECORD ONLY"},
		{"B9 warn only records", "B9", "", det, Warn, "RECORD ONLY"},
		{"monitoring-only gates only record", "B1", "", det, Fail, "RECORD ONLY"},
		{"D3 fail only records", "D3", "ranking", mdl, Fail, "RECORD ONLY"},
		{"unknown marks unknown", "B5", "", mdl, Unknown, "MARK UNKNOWN"},
		{"an unknown D8 blocker verdict marks unknown", "D8", "recipients", det, Unknown, "MARK UNKNOWN"},
		{"S2 fail marks capability unknown", "S2", "", det, Fail, "MARK UNKNOWN"},
		{"an unrecognised gate only records", "nope", "", det, Fail, "RECORD ONLY"},
	}
	for _, c := range cases {
		if got := ControlEffect(c.gate, c.sub, c.kind, c.v); got != c.want {
			t.Errorf("%s: ControlEffect(%s,%s,%s,%s) = %s, want %s", c.name, c.gate, c.sub, c.kind, c.v, got, c.want)
		}
	}
}

// The registry states the same rules (contracts/evals/eval_registry.json control_effect_rules); the two must not drift.
func TestControlEffectMatchesTheRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "evals", "eval_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Gates []struct{ ID string } `json:"gates"`
		Rules struct {
			Default map[string]string `json:"default"`
			Gates   map[string]struct {
				Pass, Warn, Fail, Unknown string
				OnlyFor                   *struct {
					Grader   string   `json:"grader"`
					SubGates []string `json:"sub_gates"`
				} `json:"only_for"`
			} `json:"gates"`
		} `json:"control_effect_rules"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Gates) != 25 || len(reg.Rules.Default) != 4 {
		t.Fatalf("registry has %d gates and %d default rules", len(reg.Gates), len(reg.Rules.Default))
	}
	for _, g := range reg.Gates {
		rule := reg.Rules.Gates[g.ID]
		for _, v := range []Verdict{Pass, Warn, Fail, Unknown} {
			pick := map[Verdict]string{Pass: rule.Pass, Warn: rule.Warn, Fail: rule.Fail, Unknown: rule.Unknown}[v]
			// Gate-level overrides apply to every result; an only_for override applies to its sub-gates and grader only.
			if rule.OnlyFor == nil {
				want := reg.Rules.Default[string(v)]
				if pick != "" {
					want = pick
				}
				if got := ControlEffect(g.ID, "", "deterministic", v); got != want {
					t.Errorf("%s %s: code says %s, registry says %s", g.ID, v, got, want)
				}
				continue
			}
			for _, sub := range rule.OnlyFor.SubGates {
				want := reg.Rules.Default[string(v)]
				if pick != "" {
					want = pick
				}
				if got := ControlEffect(g.ID, sub, rule.OnlyFor.Grader, v); got != want {
					t.Errorf("%s/%s %s: code says %s, registry says %s", g.ID, sub, v, got, want)
				}
			}
			// Outside only_for, the default applies.
			if got := ControlEffect(g.ID, "model", "model", v); got != reg.Rules.Default[string(v)] {
				t.Errorf("%s model %s: code says %s, want default %s", g.ID, v, got, reg.Rules.Default[string(v)])
			}
		}
	}
}

func TestEvaluatorVersionNamesTheJudge(t *testing.T) {
	if got := EvaluatorVersion("D3", ModelGrader("m", "ranking:v2")); got != "ranking:v2" {
		t.Errorf("model grader = %q", got)
	}
	if got := EvaluatorVersion("D7", Deterministic); got != "D7:deterministic:v1" {
		t.Errorf("deterministic grader = %q", got)
	}
	if got := EvaluatorVersion("D3", ModelGrader("m", "")); got != "D3:model:unversioned" {
		t.Errorf("model grader without a prompt version = %q", got)
	}
}

func TestFinalizeFillsEffectAndVersion(t *testing.T) {
	r := Result{Gate: "D8", SubGate: "recipients", JudgedType: "FinalArtifact", JudgedID: "x", SpanID: "recomputed_action:e", Verdict: Fail, Observed: "o", Why: "w",
		EvidenceRefs: []string{"candidate:c"}, Grader: Deterministic}.Finalize()
	if r.ControlEffect != "BLOCK" || r.EvaluatorVersion != "D8:deterministic:v1" {
		t.Fatalf("effect %q version %q", r.ControlEffect, r.EvaluatorVersion)
	}
	// A model D8 fail is stored as a record, not a block.
	m := Result{Gate: "D8", SubGate: "model", Verdict: Fail, EvidenceRefs: []string{"candidate:c"}, Grader: ModelGrader("m", "final_artifact:v1")}.Finalize()
	if m.ControlEffect != "RECORD ONLY" {
		t.Fatalf("a model D8 fail must not be a block: %s", m.ControlEffect)
	}
	// R1 downgrades to unknown first, and the effect follows the downgraded verdict.
	u := Result{Gate: "D8", SubGate: "recipients", Verdict: Fail, Grader: Deterministic}.Finalize()
	if u.Verdict != Unknown || u.ControlEffect != "MARK UNKNOWN" {
		t.Fatalf("a result with no evidence: %s %s", u.Verdict, u.ControlEffect)
	}
}
