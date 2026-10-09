package learning_test

import (
	"github.com/harneet2512/gtm-work/core-go/internal/changedim"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

// The HAR-129 demo pair, scored as measured and never tuned. The facts are read from the recorded judge and strategy
// prompts (the cassettes of the record runs, read-only). MedTech Advances at its Event N: motion, stage, health and champion
// status unknown, no open transition, trigger signal customer_replied (a re-recorded variant also pricing_interest), and a
// triggering diff whose material fields were commercial_issue, current_commitments, decision_criteria, objections,
// product_use_case and relationship_risk. EcoLite Innovations at its Event N: stage negotiation (motion, health and champion
// status unknown), no transition, signal customer_replied, and a diff whose material fields were blockers,
// commercial_issue, objections and stage. Topic (ADR-0013 amendment 4) is therefore, for MedTech, blockers_risk, buyer_intent
// and next_step_commitment, and for EcoLite blockers_risk and buyer_intent. What MedTech's lesson can say about its
// situation is the trigger signal and the topic: two comparable features of weight 6.
//
// OWNER DECISION 2026-10-06, made after seeing this score: the minimum comparable feature count was 3 and is now 2 (the
// weight minimum 5 and the 0.60 threshold are unchanged). With 3 the lesson scored 0.8333 and was withheld as
// insufficient; the owner ruled that two strong facts are enough. This expectation was changed to match that decision, not
// derived before observing the number. The first variant is therefore offered (0.83, matched on signals and topic); the
// re-recorded variant whose lesson also names pricing_interest scores 0.5833 and is below the threshold.
func TestTheMedTechLessonScoredAgainstEcoLiteIsOfferedAtTwoStrongFacts(t *testing.T) {
	rules := testRules(t)
	ep := "00000000-0000-4000-8000-0000000000e1"
	medtechTopics := changedim.TopicsOf([]string{"commercial_issue", "current_commitments", "decision_criteria", "objections", "product_use_case", "relationship_risk"})
	ecoliteTopics := changedim.TopicsOf([]string{"blockers", "commercial_issue", "objections", "stage"})
	if strings.Join(medtechTopics, ",") != "blockers_risk,buyer_intent,next_step_commitment" || strings.Join(ecoliteTopics, ",") != "blockers_risk,buyer_intent" {
		t.Fatalf("topics: medtech %v ecolite %v", medtechTopics, ecoliteTopics)
	}
	for name, c := range map[string]struct {
		signals  []string
		score    float64
		decision string
	}{
		"the trigger signal only":                     {[]string{"customer_replied"}, 0.8333, knowledge.DecisionApplicable},
		"a re-recorded variant with pricing_interest": {[]string{"customer_replied", "pricing_interest"}, 0.5833, knowledge.DecisionBelowThreshold},
	} {
		sig, app := learning.Situation{SignalTypes: c.signals, Topics: medtechTopics}.Signature() // everything else MedTech knew was unknown
		lesson := knowledge.Knowledge{ID: "00000000-0000-4000-8000-0000000000b1", Title: "Corrected inference: the contact is the champion",
			Status: knowledge.StatusCandidate, SituationSignature: sig, ApplicabilityConditions: app,
			Provenance: knowledge.Provenance{CreatedFrom: "manual", SourceDecisionEpisodeID: &ep}, Counts: knowledge.Counts{Decisions: 1}}
		for _, cd := range append(append([]knowledge.Condition{}, sig...), app...) {
			if cd.Op == "is_unknown" || cd.Value == "unknown" {
				t.Fatalf("%s: MedTech's unknowns must not become scope: %+v", name, cd)
			}
		}
		now := time.Date(2023, 11, 29, 10, 15, 0, 0, time.UTC)
		ecolite := knowledge.Situation{AccountID: "ecolite", Now: now, Conflicts: map[string][]string{}, Topics: ecoliteTopics,
			Fields:  map[string]knowledge.Value{"stage": {Known: true, Scalar: "negotiation"}},
			Signals: []knowledge.Signal{{ID: "s1", Type: "customer_replied", CreatedAt: now.Add(-time.Hour)}}}
		if !rules.ApplicableKnowledge(lesson) {
			t.Fatalf("%s: the lifecycle still offers the lesson to the matcher", name)
		}
		results, rv, err := knowledge.RetrieveAll([]knowledge.Knowledge{lesson}, ecolite, rules.Similarity)
		if err != nil {
			t.Fatal(err)
		}
		cand := rv.Candidates[0]
		t.Logf("%s: similarity %.4f on %d comparable feature(s) of weight %g, decision %s: %s", name, cand.Score, cand.Comparable, cand.ComparableWeight, cand.Decision, rv.Message)
		if results[0].Entry.Applies != (c.decision == knowledge.DecisionApplicable) || cand.Decision != c.decision || cand.Comparable != 2 || cand.ComparableWeight != 6 || cand.Score != c.score {
			t.Fatalf("%s: %+v", name, cand)
		}
		if c.decision == knowledge.DecisionApplicable {
			if !strings.HasPrefix(rv.Message, "Closest past lesson: Corrected inference: the contact is the champion (candidate), similarity 0.83, matched on signals, topic; differs on nothing") {
				t.Fatalf("%s: message = %q", name, rv.Message)
			}
			if sim := results[0].Entry.Similarity; sim == nil || sim.Score != 0.8333 || len(sim.Matched) != 2 {
				t.Fatalf("%s: the agent is told the status and score: %+v", name, sim)
			}
		} else if !strings.HasPrefix(rv.Message, "No similar knowledge in the knowledge base (closest: Corrected inference: the contact is the champion, similarity 0.58") {
			t.Fatalf("%s: message = %q", name, rv.Message)
		}
	}
}
