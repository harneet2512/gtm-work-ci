package knowledge

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestContractRulesLoad(t *testing.T) {
	r := contractRules(t)
	if r.Version != "knowledge_lifecycle:v1" || len(r.Rungs) != 3 || r.Stale.AfterDays != 180 {
		t.Fatalf("rules = %+v", r)
	}
	for status, want := range map[string]bool{
		StatusCandidate: false, StatusProvisional: true, StatusSupported: true, StatusConfirmed: true,
		StatusDisputed: false, StatusStale: false,
	} {
		if r.Applicable(status) != want {
			t.Fatalf("applicable(%s) = %v", status, !want)
		}
	}
}

func TestInvalidRulesAreRejected(t *testing.T) {
	base := func() map[string]any {
		var doc map[string]any
		raw, _ := json.Marshal(contractRules(t))
		_ = json.Unmarshal(raw, &doc)
		return doc
	}
	rung := func(doc map[string]any, i int) map[string]any { return doc["rungs"].([]any)[i].(map[string]any) }
	cases := map[string]func(map[string]any){
		"unknown key":              func(d map[string]any) { d["promote_fast"] = true },
		"no version":               func(d map[string]any) { d["version"] = "" },
		"unknown status":           func(d map[string]any) { d["applicable_statuses"] = []any{"active"} },
		"no applicable statuses":   func(d map[string]any) { d["applicable_statuses"] = []any{} },
		"rungs out of order":       func(d map[string]any) { r := d["rungs"].([]any); r[0], r[1] = r[1], r[0] },
		"two rungs":                func(d map[string]any) { d["rungs"] = d["rungs"].([]any)[:2] },
		"zero decisions":           func(d map[string]any) { rung(d, 0)["min_decisions"] = 0 },
		"share above one":          func(d map[string]any) { rung(d, 1)["max_counterexample_share"] = 1.5 },
		"non-increasing decisions": func(d map[string]any) { rung(d, 2)["min_decisions"] = 5 },
		"dispute share zero":       func(d map[string]any) { d["dispute"].(map[string]any)["min_counterexample_share"] = 0 },
		"stale zero":               func(d map[string]any) { d["stale"].(map[string]any)["after_days"] = 0 },
	}
	for name, mutate := range cases {
		doc := base()
		mutate(doc)
		raw, _ := json.Marshal(doc)
		if _, err := ParseRules(raw); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	more := map[string]func(map[string]any){
		"negative minimum":          func(d map[string]any) { rung(d, 0)["min_positive_reactions"] = -1 },
		"fewer reactions higher up": func(d map[string]any) { rung(d, 2)["min_positive_reactions"] = 0 },
		"larger share higher up":    func(d map[string]any) { rung(d, 2)["max_counterexample_share"] = 0.9 },
	}
	for name, mutate := range more {
		doc := base()
		mutate(doc)
		raw, _ := json.Marshal(doc)
		if _, err := ParseRules(raw); !errors.Is(err, ErrInvalidRules) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	raw, _ := json.Marshal(base())
	if _, err := ParseRules(append(raw, []byte(` {"x":1}`)...)); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("trailing data: %v", err)
	}
	if _, err := LoadRules("does-not-exist.json"); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("missing file: %v", err)
	}
}
