package contracts

import (
	"path/filepath"
	"testing"
)

// TestEvalAreasInstanceValidates checks contracts/evals/eval_areas.json (HAR-145: eval type -> family -> area) against
// its schema; the example is covered by TestEveryExampleValidates and the Go table by internal/evalarea.
func TestEvalAreasInstanceValidates(t *testing.T) {
	root := contractsDir(t)
	if err := schemaFor(t, compiler(t, root), "eval_areas").Validate(readJSON(t, filepath.Join(root, "evals", "eval_areas.json"))); err != nil {
		t.Fatalf("eval_areas.json invalid: %v", err)
	}
}

func TestEvalAreasKnownBadAreRejected(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "eval_areas")
	cases := []struct {
		name string
		fn   func(m map[string]any)
	}{
		{"exactly four areas", func(m map[string]any) { m["areas"] = m["areas"].([]any)[:3] }},
		{"an unknown area id", func(m map[string]any) { m["areas"].([]any)[0].(map[string]any)["id"] = "growth" }},
		{"an area holds only E families", func(m map[string]any) {
			m["areas"].([]any)[0].(map[string]any)["families"] = []any{"M1"}
		}},
		{"an unknown eval type", func(m map[string]any) { m["eval_types"].(map[string]any)["vibes"] = "E1" }},
		{"a type maps to an E family, never an M metric", func(m map[string]any) {
			m["eval_types"].(map[string]any)["provenance_coverage"] = "M1"
		}},
		{"metric families are M ids", func(m map[string]any) { m["metric_families"] = []any{"E1"} }},
		{"no extra fields", func(m map[string]any) { m["score"] = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.Validate(mutate(t, root, "eval_areas", tc.fn)); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			}
		})
	}
}
