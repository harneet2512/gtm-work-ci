package learning_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

func scopeOf(s learning.Situation) knowledge.Knowledge {
	sig, app := s.Signature()
	return knowledge.Knowledge{SituationSignature: sig, ApplicabilityConditions: app}
}

func validCandidate(k knowledge.Knowledge) knowledge.Knowledge {
	k.ID, k.Title = "00000000-0000-4000-8000-000000000001", "t"
	k.Status = knowledge.StatusCandidate
	k.Guidance = knowledge.Guidance{Summary: "s", Do: []string{}, Dont: []string{}}
	k.SupportingDecisionEpisodeIDs, k.Counterexamples, k.Exceptions = []string{}, []knowledge.Counterexample{}, []knowledge.Exception{}
	k.Provenance = knowledge.Provenance{CreatedFrom: "manual"}
	return k
}

// HAR-97 B9: one human edit plus one reply must not make knowledge applicable everywhere. The scope is the
// episode's state, not transition.status alone.
func TestSignatureScopesToTheEpisodeStateNotTheTransitionStatusAlone(t *testing.T) {
	k := scopeOf(learning.Situation{TransitionStatus: "candidate", TransitionTo: "EXPANSION", TransitionFrom: "STABLE",
		Relationship: "STABLE", Stage: "Negotiation"})
	want := map[string]bool{"transition.status": true, "transition.to_state": true, "transition.from_state": true,
		"relationship_state": true, "stage": true}
	fields := knowledge.ScopeFields(k)
	for _, f := range fields {
		delete(want, f)
	}
	if len(want) != 0 {
		t.Fatalf("scope %v lacks %v", fields, want)
	}
	if knowledge.ScopeTooBroad(k) {
		t.Fatal("scope is too broad")
	}
	if err := knowledge.Validate(validCandidate(k)); err != nil {
		t.Fatalf("scope must validate: %v", err)
	}
}

func TestSignatureWithoutATransitionStillConstrainsRelationshipAndStage(t *testing.T) {
	k := scopeOf(learning.Situation{Relationship: "STABLE", Stage: "Discovery"})
	if knowledge.ScopeTooBroad(k) {
		t.Fatalf("relationship plus stage is two fields: %v", knowledge.ScopeFields(k))
	}
}

func TestSignatureNeverEmptyAndUnknownStaysUnknown(t *testing.T) {
	sig, _ := learning.Situation{}.Signature()
	if len(sig) != 1 || sig[0].Op != "is_unknown" {
		t.Fatalf("empty situation signature = %+v", sig)
	}
}
