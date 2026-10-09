package learning_test

import (
	"strings"
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

func TestSignatureNeverEmptyAndNeverScopesOnUnknown(t *testing.T) {
	sig, app := learning.Situation{}.Signature()
	if len(sig) != 1 || sig[0].Op == "is_unknown" || len(app) != 0 {
		t.Fatalf("empty situation signature = %+v %+v", sig, app)
	}
	k := scopeOf(learning.Situation{Stage: "Negotiation"})
	if len(k.SituationSignature)+len(k.ApplicabilityConditions) != 1 {
		t.Fatalf("only the known stage is a condition: %+v", k)
	}
}

// ADR-0013 amendment 2: the scope is only what the episode knew: transition, state features and the trigger signals.
func TestSignatureScopesOnTheKnownFeaturesAndTheTriggerSignals(t *testing.T) {
	k := scopeOf(learning.Situation{Motion: "expansion", Health: "at_risk", ChampionStatus: "weakening",
		SignalTypes: []string{"customer_replied", "pricing_interest"}})
	got := map[string]bool{}
	for _, c := range append(append([]knowledge.Condition{}, k.SituationSignature...), k.ApplicabilityConditions...) {
		got[c.Field+" "+c.Op] = true
		if c.Op == "is_unknown" || c.Value == "unknown" || c.Value == "" {
			t.Fatalf("an unknown or empty value is never a scope condition: %+v", c)
		}
	}
	for _, want := range []string{"motion eq", "health eq", "champion_status eq", "diff.customer_replied exists", "diff.pricing_interest exists"} {
		if !got[want] {
			t.Fatalf("scope lacks %q: %v", want, got)
		}
	}
	if err := knowledge.Validate(validCandidate(k)); err != nil {
		t.Fatalf("scope must validate: %v", err)
	}
}

// The topic of the source episode (the change dimensions of its triggering diff) is a scope condition the closest-match
// retrieval reads back as the topic feature; an unknown topic is never a condition.
func TestSignatureStatesTheTopicOfTheEpisodeAndOnlyWhenKnown(t *testing.T) {
	k := scopeOf(learning.Situation{Stage: "Negotiation", Topics: []string{"blockers_risk", "buyer_intent"}})
	var fields []string
	for _, c := range append(append([]knowledge.Condition{}, k.SituationSignature...), k.ApplicabilityConditions...) {
		fields = append(fields, c.Field+" "+c.Op)
	}
	if got := strings.Join(fields, ","); got != "stage eq,topic.blockers_risk exists,topic.buyer_intent exists" {
		t.Fatalf("conditions = %s", got)
	}
	if err := knowledge.Validate(validCandidate(k)); err != nil {
		t.Fatalf("the topic scope must validate: %v", err)
	}
	f := knowledge.LessonFeatures(k)
	if _, ok := f[knowledge.FeatTopic]; !ok {
		t.Fatalf("the lesson's topic feature is not read back: %v", f)
	}
	for _, c := range scopeOf(learning.Situation{Stage: "Negotiation"}).ApplicabilityConditions {
		t.Fatalf("a situation with no topic states none: %+v", c)
	}
}
