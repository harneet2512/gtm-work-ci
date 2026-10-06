package bucket2

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestD8BlockersAreOnlyCatalogMappedDeterministicFails(t *testing.T) {
	det := Deterministic
	model := ModelGrader("m", "v")
	results := []Result{
		{Gate: "D8", SubGate: "recipients", Verdict: Fail, Grader: det},
		{Gate: "D8", SubGate: "no_stale_content", Verdict: Fail, Grader: det},
		{Gate: "D8", SubGate: "policy_gates", Verdict: Fail, Grader: det},        // blocks through the send-time evals, not here
		{Gate: "D8", SubGate: "model", Verdict: Fail, Grader: model},             // advisory
		{Gate: "D8", SubGate: "recipients", Verdict: Warn, Grader: det},          // a warn never blocks
		{Gate: "D8", SubGate: "no_stale_content", Verdict: Unknown, Grader: det}, // unknown never blocks
		{Gate: "D2", SubGate: "recipients", Verdict: Fail, Grader: det},          // another gate
		{Gate: "D8", SubGate: "recipients", Verdict: Pass, Grader: det},
	}
	got := D8Blockers(results)
	if len(got) != 2 || got[0].SubGate != "recipients" || got[1].SubGate != "no_stale_content" {
		t.Fatalf("D8Blockers = %+v, want the recipients and no_stale_content fails only", got)
	}
	if len(D8Blockers(nil)) != 0 {
		t.Fatal("no results must block nothing")
	}
}

// Every sub-gate allowed to block must name a catalog eval that can block and states a blocking rule that does
// not say "never blocks": the catalog, not this package, owns who may refuse a send.
func TestD8BlockingMapMatchesTheCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/evals/eval_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		EvalTypes map[string]struct {
			CanBlock     bool   `json:"can_block"`
			BlockingRule string `json:"blocking_rule"`
		} `json:"eval_types"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	for sub, evalType := range D8BlockingEvals() {
		e, ok := cat.EvalTypes[evalType]
		if !ok || !e.CanBlock {
			t.Errorf("sub-gate %s maps to %s, which the catalog does not allow to block", sub, evalType)
		}
		if strings.HasPrefix(e.BlockingRule, "Never blocks") {
			t.Errorf("sub-gate %s maps to %s whose blocking_rule says it never blocks", sub, evalType)
		}
	}
}
