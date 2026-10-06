package bucket1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryAuditCoversEveryGateAndOnlyRealEvalsOfThatGate(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "evals", "eval_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Evals []struct{ ID, Gate string } `json:"evals"`
		Audit []struct {
			Gate      string   `json:"gate"`
			LegacyIDs []string `json:"legacy_ids"`
			Audit     string   `json:"audit"`
		} `json:"bucket1_audit"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	gateOf := map[string]string{}
	for _, e := range reg.Evals {
		gateOf[e.ID] = e.Gate
	}
	covered := map[string]bool{}
	for _, a := range reg.Audit {
		covered[a.Gate] = true
		if a.Audit != "keep" && a.Audit != "fix" && a.Audit != "missing" {
			t.Errorf("%s audit verdict %q", a.Gate, a.Audit)
		}
		for _, id := range a.LegacyIDs {
			if gateOf[id] != a.Gate {
				t.Errorf("%s lists %s, which the registry puts under %q", a.Gate, id, gateOf[id])
			}
		}
	}
	for _, g := range GateOrder {
		if !covered[g] {
			t.Errorf("gate %s has no audit row", g)
		}
	}
}
