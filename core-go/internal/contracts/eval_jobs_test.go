package contracts

import (
	"path/filepath"
	"testing"
)

// HAR-129 demo-loop clarification (2026-10-04): three distinct eval jobs. Intelligence-building evals (E1-E6:
// evidence fidelity, entity resolution, graph/state mutation, state interpretation, precedent and knowledge
// applicability, and the record of what knowledge was retrieved: E1-E7) judge context -> organizational
// intelligence (E5-E7 are shown under Decision & Learning > Retrieve, but their job is intelligence); decision and
// feedback-loop evals (E8-E17) judge
// intelligence -> decision -> action -> human feedback -> updated knowledge; system metrics (validation, safety and
// operational classes: E18-E22, M1-M5) stay secondary. Every family carries exactly one job, and the split follows
// this rule so the surfaces (web /evals, the run eval page, Slack) can never show a system metric as the loop.
func TestEveryRegistryFamilyHasTheJobItServes(t *testing.T) {
	var reg struct {
		Families []struct {
			ID    string `json:"id"`
			Class string `json:"class"`
			Job   string `json:"job"`
		} `json:"families"`
	}
	decodeFile(t, filepath.Join(contractsDir(t), "evals", "eval_registry.json"), &reg)
	if len(reg.Families) != 27 {
		t.Fatalf("got %d families, want 27 (E1-E22, M1-M5)", len(reg.Families))
	}
	intelligence := map[string]bool{"E1": true, "E2": true, "E3": true, "E4": true, "E5": true, "E6": true, "E7": true}
	system := map[string]bool{"validation": true, "safety": true, "operational": true}
	counts := map[string]int{}
	for _, f := range reg.Families {
		want := "decision_loop"
		switch {
		case system[f.Class]:
			want = "system"
		case intelligence[f.ID]:
			want = "intelligence"
		}
		if f.Job != want {
			t.Errorf("family %s (class %s): job %q, want %q", f.ID, f.Class, f.Job, want)
		}
		counts[f.Job]++
	}
	if counts["intelligence"] != 7 || counts["decision_loop"] != 10 || counts["system"] != 10 {
		t.Fatalf("job split %v, want intelligence 7, decision_loop 10, system 10", counts)
	}
}
