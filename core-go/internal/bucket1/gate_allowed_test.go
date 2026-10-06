package bucket1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every gate of the registry that holds results (B1-B9, D1-D10, S1-S5) must be allowed by the gate_results table, so
// no bucket's results are refused at the database. S6 names the efficiency metrics, which are never results.
func TestEveryRegistryGateIsAllowedByTheGateResultsTable(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	sql, err := os.ReadFile(filepath.Join(root, "core-go", "internal", "store", "migrations", "0036_bucket2_evals.sql"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`gate\s+text NOT NULL CHECK \(gate ~ '([^']+)'\)`).FindSubmatch(sql)
	if m == nil {
		t.Fatal("the gate check of gate_results was not found in the migration")
	}
	allowed := regexp.MustCompile(string(m[1]))
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "evals", "eval_registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Gates []struct{ ID string } `json:"gates"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, g := range reg.Gates {
		if g.ID == "S6" {
			continue
		}
		seen++
		if !allowed.MatchString(g.ID) {
			t.Errorf("gate %s is in the registry but the gate_results check refuses it", g.ID)
		}
	}
	if seen != 24 {
		t.Errorf("%d result gates in the registry, want 24 (B1-B9, D1-D10, S1-S5)", seen)
	}
}
