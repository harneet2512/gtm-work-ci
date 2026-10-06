package contracts

import (
	"path/filepath"
	"strings"
	"testing"
)

// The eval cards read each gate's definition (HAR-97 v2) from the registry. Bucket 1 and 2 gates carry the full
// definition, Bucket 2 also says when it runs, and Bucket 3 gates say honestly what is built. The existing
// name/question/improves fields stay as they are (stored results and the Go gate metadata pin them). Web twin:
// web/tests/gate-definitions.test.ts.

type gateDef struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	DisplayName     string            `json:"display_name"`
	DisplayQuestion string            `json:"display_question"`
	Invariant       string            `json:"invariant"`
	Judges          string            `json:"judges"`
	Grader          string            `json:"grader"`
	Criteria        map[string]string `json:"criteria"`
	CriteriaNote    string            `json:"criteria_note"`
	Protects        string            `json:"protects"`
	When            string            `json:"when"`
	StatusNote      string            `json:"status_note"`
	Mode            string            `json:"mode"`
	Trigger         string            `json:"trigger"`
	NotTriggered    string            `json:"not_triggered"`
}

func TestEveryGateCarriesItsDefinition(t *testing.T) {
	var reg struct {
		Gates []gateDef `json:"gates"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "evals", "eval_registry.json"), &reg)
	if len(reg.Gates) != 25 {
		t.Fatalf("want 25 gates (B1-B9, D1-D10, S1-S6), got %d", len(reg.Gates))
	}
	for _, g := range reg.Gates {
		switch g.Mode {
		case "live_required", "live_conditional", "offline_benchmark", "continuous_aggregate":
		default:
			t.Errorf("%s: mode %q is not one of the HAR-97 execution modes", g.ID, g.Mode)
		}
		if g.Trigger == "" {
			t.Errorf("%s: trigger is required", g.ID)
		}
		if (g.Mode == "live_conditional") != (g.NotTriggered != "") {
			t.Errorf("%s: not_triggered belongs to live_conditional gates only, and every one has it", g.ID)
		}
		if g.Mode == "offline_benchmark" && g.ID[0] != 'S' || g.ID[0] == 'S' && g.ID != "S1" && g.ID != "S6" && g.Mode != "offline_benchmark" {
			t.Errorf("%s: only S2-S5 are offline benchmarks", g.ID)
		}
		if g.DisplayName == "" || g.DisplayQuestion == "" {
			t.Errorf("%s: display_name and display_question are required", g.ID)
		}
		switch g.ID[0] {
		case 'S':
			if g.StatusNote == "" {
				t.Errorf("%s: status_note is required", g.ID)
			}
		default:
			if g.Invariant == "" || g.Judges == "" || g.Grader == "" || g.Protects == "" {
				t.Errorf("%s: invariant, judges, grader and protects are required", g.ID)
			}
			if len(g.Criteria) == 0 && g.CriteriaNote == "" {
				t.Errorf("%s: criteria (or a criteria_note) is required", g.ID)
			}
			for k, v := range g.Criteria {
				if strings.TrimSpace(v) == "" {
					t.Errorf("%s: criteria.%s is empty", g.ID, k)
				}
			}
			if g.ID[0] == 'D' && g.When == "" {
				t.Errorf("%s: when is required for a Bucket 2 gate", g.ID)
			}
		}
	}
}
