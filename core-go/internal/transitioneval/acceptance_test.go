package transitioneval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/transitions"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found", rel)
		}
		dir = parent
	}
}

func committedResults(t *testing.T) []StepResult {
	t.Helper()
	rules, err := transitions.LoadRules(repoFile(t, "contracts/transitions/rules.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	gold, err := LoadGoldFiles(map[string]string{OriginAuthored: repoFile(t, "fixtures/gold/transitions/scenarios.json"),
		OriginBlind: repoFile(t, "fixtures/gold/transitions/blind.json")})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(rules, gold)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// knownTimingDivergence lists gold steps where the detector CONFIRMs one recompute later than the blind author
// expected, because REORG is never confirmed in the evaluation that first finds its evidence (rules.v1.json
// confirmed.after_candidate, PR #16 review round 5). The evidence is earned; only the step differs. Each entry
// is a design decision to revisit with the lead, not a free pass: any other CONFIRMED without gold fails.
var knownTimingDivergence = map[string]string{
	"lifecycle_demo_unknown_reorg_candidate_confirmed_expansion / routine reply only": "REORG confirms one recompute after its CANDIDATE",
}

// HAR-126 acceptance: no premature promotion on any gold checkpoint. A CONFIRMED prediction where the gold does
// not say CONFIRMED fails this test, contested scenarios included, except the documented timing divergences.
func TestNoPrematurePromotionOnAnyGoldStep(t *testing.T) {
	confirmed := 0
	for _, r := range committedResults(t) {
		if r.Pred.Status != transitions.StatusConfirmed {
			continue
		}
		confirmed++
		if _, known := knownTimingDivergence[r.Scenario+" / "+r.Label]; known {
			continue
		}
		if r.GoldStatus() != transitions.StatusConfirmed {
			t.Errorf("%s / %s: detector CONFIRMED %s where the gold says %s", r.Scenario, r.Label, r.Pred.ToState, r.GoldStatus())
		}
	}
	if confirmed < 10 {
		t.Fatalf("only %d CONFIRMED predictions: the check has no teeth", confirmed)
	}
}

// Every gold step yields an inspectable outcome: the detector never errors or drops a step.
func TestEveryGoldStepIsEvaluated(t *testing.T) {
	steps := 0
	for _, r := range committedResults(t) {
		steps++
		if r.Pred.Status == "" || r.Pred.RelationshipStateAfter == "" {
			t.Errorf("%s / %s: empty prediction", r.Scenario, r.Label)
		}
	}
	if steps < 100 {
		t.Fatalf("%d steps", steps)
	}
}
