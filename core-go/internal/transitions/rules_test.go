package transitions

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTheCommittedRuleSetLoads(t *testing.T) {
	rs := loadRules(t)
	if rs.Version != "transition_rules:v1" || len(rs.Transitions) != 2 {
		t.Fatalf("version %q with %d rules", rs.Version, len(rs.Transitions))
	}
	for _, name := range []string{"owner_stable_min_days", "expansion_evidence_window_days", "reorg_evidence_window_days", "candidate_min_change_facts", staleThreshold} {
		if rs.Thresholds[name].Value <= 0 {
			t.Errorf("threshold %s = %d", name, rs.Thresholds[name].Value)
		}
	}
}

// mutate decodes the committed rule set into a generic document, applies edit and re-encodes it.
func mutate(t *testing.T, edit func(doc map[string]any)) []byte {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, "contracts/transitions/rules.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func firstRule(doc map[string]any) map[string]any {
	return doc["transitions"].([]any)[0].(map[string]any)
}

func TestInvalidRuleSetsAreRefusedExplicitly(t *testing.T) {
	tests := []struct {
		name string
		edit func(doc map[string]any)
		want string
	}{
		{"an unknown top-level key", func(d map[string]any) { d["surprise"] = 1 }, "surprise"},
		{"a bad version", func(d map[string]any) { d["rule_set_version"] = "v1" }, "rule_set_version"},
		{"no stale threshold", func(d map[string]any) { delete(d["thresholds"].(map[string]any), staleThreshold) }, staleThreshold},
		{"a gate naming an unknown fact", func(d map[string]any) {
			firstRule(d)["candidate"].(map[string]any)["requires_any"] = []any{"no_such_fact"}
		}, "no_such_fact"},
		{"a confirmation naming an unknown fact", func(d map[string]any) {
			firstRule(d)["confirmed"].(map[string]any)["requires_all"] = []any{"no_such_fact"}
		}, "no_such_fact"},
		{"a condition using an unknown threshold", func(d map[string]any) {
			cond := firstRule(d)["facts"].([]any)[0].(map[string]any)["any_of"].([]any)[0].(map[string]any)["all_of"].([]any)[1].(map[string]any)
			cond["threshold"] = "no_such_threshold"
		}, "no_such_threshold"},
		{"a duplicate rule id", func(d map[string]any) {
			rules := d["transitions"].([]any)
			d["transitions"] = append(rules, rules[0])
		}, "duplicate rule id"},
		{"a duplicate fact key", func(d map[string]any) {
			r := firstRule(d)
			facts := r["facts"].([]any)
			r["facts"] = append(facts, facts[0])
		}, "duplicate fact"},
		{"no transitions", func(d map[string]any) { d["transitions"] = []any{} }, "no transitions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRules(mutate(t, tt.edit))
			if !errors.Is(err, ErrInvalidRules) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want ErrInvalidRules mentioning %q", err, tt.want)
			}
		})
	}
}

func TestLoadRulesReportsAMissingFile(t *testing.T) {
	if _, err := LoadRules("no/such/rules.json"); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

// A fact that holds must cite evidence (state_transition.v1.json: satisfied facts have evidence_refs >= 1), so
// every alternative of every fact needs a condition that carries evidence, not only an absence test.
func TestEveryFactAlternativeCanCiteEvidence(t *testing.T) {
	rs := loadRules(t)
	for _, r := range rs.Transitions {
		for _, f := range append(append([]Fact{}, r.Facts...), r.Contradictions...) {
			for i, g := range f.AnyOf {
				carries := false
				for _, c := range g.AllOf {
					carries = carries || (c.Op != opNotExist && c.Op != opUnknown)
				}
				if !carries {
					t.Errorf("%s.%s alternative %d has only absence tests, so it could hold without evidence", r.ID, f.Key, i)
				}
			}
		}
	}
}
