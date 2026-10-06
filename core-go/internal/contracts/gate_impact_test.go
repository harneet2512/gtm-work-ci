package contracts

import (
	"path/filepath"
	"strings"
	"testing"
)

// HAR-145: every registry gate states what its failure changes (`impact`, with a code citation in `impact_basis`) and the
// Cliff message it belongs to (`message`). Both are data the web reads, never derived from a gate name. Web twin:
// web/tests/gate-impact.test.ts.
func TestEveryGateStatesItsImpactAndMessage(t *testing.T) {
	var reg struct {
		Gates []struct {
			ID          string `json:"id"`
			Impact      string `json:"impact"`
			ImpactBasis string `json:"impact_basis"`
			Message     string `json:"message"`
		} `json:"gates"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "evals", "eval_registry.json"), &reg)
	impacts := map[string]bool{
		"blocks current action": true, "requires human review": true, "triggers recomputation": true,
		"prevents knowledge promotion": true, "marks capability unreliable": true, "monitoring only": true,
	}
	messages := map[string]bool{"M1": true, "M2": true, "M3": true, "ecolite": true}
	for _, g := range reg.Gates {
		if !impacts[g.Impact] {
			t.Errorf("%s: impact %q is not one of HAR-145's six values", g.ID, g.Impact)
		}
		if len(strings.TrimSpace(g.ImpactBasis)) < 10 {
			t.Errorf("%s: impact_basis must cite the code behind the impact", g.ID)
		}
		if strings.HasPrefix(g.ID, "S") {
			if g.Message != "system" {
				t.Errorf("%s: a System gate has message system, got %q", g.ID, g.Message)
			}
		} else if !messages[g.Message] {
			t.Errorf("%s: message %q is not M1, M2, M3 or ecolite", g.ID, g.Message)
		}
	}
}
