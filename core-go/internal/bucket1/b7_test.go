package bucket1

import (
	"errors"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

func b7Knowledge(id, status string, stage string) knowledge.Knowledge {
	return knowledge.Knowledge{
		ID: id, Title: "k " + id, Status: status, CreatedAt: t0.Add(-24 * time.Hour),
		SituationSignature:           []knowledge.Condition{eq("transition.status", "candidate")},
		ApplicabilityConditions:      []knowledge.Condition{eq("stage", stage)},
		Guidance:                     knowledge.Guidance{Summary: "s", Do: []string{"loop in legal"}, Dont: []string{}},
		SupportingDecisionEpisodeIDs: []string{}, Counterexamples: []knowledge.Counterexample{}, Exceptions: []knowledge.Exception{},
		Provenance: knowledge.Provenance{CreatedFrom: "manual"},
	}
}

func b7Episode(applicable, blocked []string, ks ...knowledge.Knowledge) Episode {
	ep := goodEpisode()
	ep.Knowledge = ks
	var ids []string
	for _, k := range ks {
		ids = append(ids, k.ID)
	}
	ep.Situation = &knowledge.Situation{
		AccountID: "acc-1", Now: t0, RelationshipState: "STABLE",
		Fields:     map[string]knowledge.Value{"stage": {Known: true, Scalar: "Negotiation", EvidenceRefs: []knowledge.EvidenceRef{{ActivityID: "a1"}}}},
		Transition: &knowledge.Transition{ID: "tr1", Status: "candidate", FromState: "STABLE", ToState: "EXPANSION"},
	}
	ep.Attribution = &Attribution{StepID: "step-build-context", AsOf: t0, Retrieved: ids, Applicable: applicable, ExceptionBlocked: blocked, Used: nil}
	return ep
}

func TestB7ParseAttributionReadsTheRealRecord(t *testing.T) {
	detail := []byte(`{"knowledge_attribution":{"as_of":"2026-03-10T09:00:00Z","retrieved":["k1","k2"],"applicable":["k1"],"exception_blocked":["k2"],"used":["k1"]}}`)
	a, err := ParseAttribution("s1", detail)
	if err != nil || a.StepID != "s1" || len(a.Retrieved) != 2 || a.Applicable[0] != "k1" || !a.AsOf.Equal(t0) {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := ParseAttribution("s1", []byte(`{"other":1}`)); !errors.Is(err, ErrNoAttribution) {
		t.Fatalf("a step without the record = %v", err)
	}
	if _, err := ParseAttribution("s1", []byte(`{not json`)); err == nil {
		t.Fatal("malformed detail must be an explicit error")
	}
}

func TestB7AgreeingRecordPasses(t *testing.T) {
	ep := b7Episode([]string{"k1"}, nil, b7Knowledge("k1", "provisional", "Negotiation"), b7Knowledge("k2", "provisional", "Discovery"))
	r := GradeB7(ep)
	if r.Verdict != Pass || r.JudgedObject.Type != "AgentRun" || r.EvidenceRefs[0].StepID != "step-build-context" {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
}

func TestB7KnowledgeWhosePreconditionsAreUnmetMustNotBeApplicable(t *testing.T) {
	ep := b7Episode([]string{"k1", "k2"}, nil, b7Knowledge("k1", "provisional", "Negotiation"), b7Knowledge("k2", "provisional", "Discovery"))
	a := byName(t, GradeB7(ep), "scope_and_preconditions")
	wantVerdict(t, a, Fail)
}

func TestB7AnApplicableObjectTheRecordMissedFails(t *testing.T) {
	ep := b7Episode(nil, nil, b7Knowledge("k1", "provisional", "Negotiation"))
	wantVerdict(t, byName(t, GradeB7(ep), "scope_and_preconditions"), Fail)
}

func TestB7ExceptionsMustBlock(t *testing.T) {
	k := b7Knowledge("k1", "provisional", "Negotiation")
	k.Exceptions = []knowledge.Exception{{Description: "security open", Conditions: []knowledge.Condition{eq("stage", "Negotiation")}}}
	ep := b7Episode([]string{"k1"}, nil, k)
	if r := GradeB7(ep); byName(t, r, "scope_and_preconditions").Verdict != Fail {
		t.Fatal("claimed applicable although an exception fires")
	}
	ep = b7Episode(nil, []string{"k1"}, k)
	wantVerdict(t, byName(t, GradeB7(ep), "exceptions_checked"), Pass)
	ep = b7Episode(nil, nil, k)
	wantVerdict(t, byName(t, GradeB7(ep), "exceptions_checked"), Fail)
}

func TestB7StaleOrFutureKnowledgeMustNotBeApplicable(t *testing.T) {
	stale := b7Knowledge("k1", "stale", "Negotiation")
	wantVerdict(t, byName(t, GradeB7(b7Episode([]string{"k1"}, nil, stale)), "freshness_and_version"), Fail)
	future := b7Knowledge("k1", "provisional", "Negotiation")
	future.CreatedAt = t0.Add(time.Hour)
	wantVerdict(t, byName(t, GradeB7(b7Episode([]string{"k1"}, nil, future)), "freshness_and_version"), Fail)
}

func TestB7ContradictoryApplicableKnowledgeIsSurfaced(t *testing.T) {
	a, b := b7Knowledge("k1", "provisional", "Negotiation"), b7Knowledge("k2", "provisional", "Negotiation")
	b.Guidance = knowledge.Guidance{Summary: "s", Do: []string{}, Dont: []string{"Loop in legal"}}
	r := GradeB7(b7Episode([]string{"k1", "k2"}, nil, a, b))
	wantVerdict(t, byName(t, r, "contradictory_knowledge_surfaced"), Warn)
}

func TestB7NothingApplicableIsUnknownNotPass(t *testing.T) {
	ep := b7Episode(nil, nil, b7Knowledge("k1", "provisional", "Discovery"))
	r := GradeB7(ep)
	if r.Verdict != Unknown || r.Why == "" {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
}

func TestB7InconsistentRecordFails(t *testing.T) {
	ep := b7Episode([]string{"k1", "ghost"}, nil, b7Knowledge("k1", "provisional", "Negotiation"))
	wantVerdict(t, byName(t, GradeB7(ep), "record_consistent"), Fail)
}

func TestB7WithoutTheRecordIsNotMeasured(t *testing.T) {
	ep := b7Episode(nil, nil)
	ep.Attribution = nil
	if r := GradeB7(ep); r.Verdict != Unknown || r.Observed != "not measured" {
		t.Fatalf("%s %s", r.Verdict, r.Observed)
	}
}
