package bucket1

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

func eq(field, v string) knowledge.Condition {
	return knowledge.Condition{Field: field, Op: "eq", Value: v}
}

func goodRevision() Revision {
	return Revision{
		ID: "m1", Operation: "CREATE", KnowledgeID: "k1", Status: "candidate",
		Preconditions:           []knowledge.Condition{eq("transition.status", "candidate")},
		ApplicabilityConditions: []knowledge.Condition{eq("relationship_state", "STABLE"), eq("stage", "Negotiation")},
		Evidence:                []RevisionEvidence{{Kind: "decision_episode", RefID: "ep-13", ActivityID: "a1"}},
		Version:                 1, SourceEpisode: "ep-13", OccurredAt: t0,
	}
}

func b9Episode(trace RevisionTrace, revs ...Revision) Episode {
	ep := goodEpisode()
	ep.Trace = &trace
	ep.Revisions = revs
	return ep
}

func TestClassifyRevisionFollowsTheEvidence(t *testing.T) {
	cases := []struct {
		name  string
		trace RevisionTrace
		want  string
	}{
		{"edit with no knowledge creates a candidate", RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, "CREATE"},
		{"a weak edit with no knowledge changes nothing", RevisionTrace{HumanEdit: true, SignalStrength: "weak"}, "NO_CHANGE"},
		{"positive reply in scope strengthens", RevisionTrace{KnowledgeInScope: true, Customer: []string{"positive"}}, "STRENGTHEN"},
		{"human edit alone never strengthens", RevisionTrace{KnowledgeInScope: true, HumanEdit: true, SignalStrength: "strong"}, "NO_CHANGE"},
		{"negative reply weakens", RevisionTrace{KnowledgeInScope: true, Customer: []string{"negative"}}, "WEAKEN"},
		{"negative reply under a new condition adds an exception", RevisionTrace{KnowledgeInScope: true, Customer: []string{"negative"}, DistinguishingCondition: true}, "ADD_EXCEPTION"},
		{"counterexamples dispute", RevisionTrace{KnowledgeInScope: true, Disputed: true}, "DISPUTE"},
		{"past the stale age", RevisionTrace{KnowledgeInScope: true, Stale: true}, "MARK_STALE"},
		{"two independent positive replies outside scope expand", RevisionTrace{KnowledgeOutOfScope: true, Customer: []string{"positive", "positive"}, IndependentPositive: 2}, "EXPAND"},
		{"one positive reply outside scope does not expand", RevisionTrace{KnowledgeOutOfScope: true, Customer: []string{"positive"}, IndependentPositive: 1}, "NO_CHANGE"},
		{"a strong edit outside scope creates a scoped candidate", RevisionTrace{KnowledgeOutOfScope: true, HumanEdit: true, SignalStrength: "strong", Customer: []string{"positive"}, IndependentPositive: 1}, "CREATE"},
		{"a strong signal with an advanced outcome expands", RevisionTrace{KnowledgeOutOfScope: true, HumanEdit: true, SignalStrength: "strong", OutcomeAdvanced: true}, "EXPAND"},
		{"negative reply outside scope leaves it alone", RevisionTrace{KnowledgeOutOfScope: true, Customer: []string{"negative"}}, "NO_CHANGE"},
		{"human narrowing", RevisionTrace{KnowledgeInScope: true, HumanNarrows: true, HumanEdit: true, SignalStrength: "strong"}, "NARROW"},
		{"human rewording", RevisionTrace{KnowledgeInScope: true, HumanRewords: true, HumanEdit: true, SignalStrength: "strong"}, "REFINE"},
		{"nothing happened", RevisionTrace{}, "NO_CHANGE"},
	}
	for _, c := range cases {
		if got := ClassifyRevision(c.trace); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestB9ACorrectScopedCreateFromOneEditPasses(t *testing.T) {
	ep := b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, goodRevision())
	r := GradeB9(ep)
	if r.Verdict != Pass {
		t.Fatalf("verdict %s: %s", r.Verdict, r.Why)
	}
	if r.Gate != "B9" || r.Calibrated || len(r.EvidenceRefs) == 0 || r.JudgedObject.Type != "KnowledgeMutation" {
		t.Fatalf("shape: %+v", r)
	}
}

func TestB9TheOperationMustFollowTheTrace(t *testing.T) {
	rev := goodRevision()
	rev.Operation = "STRENGTHEN"
	r := GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, rev))
	wantVerdict(t, byName(t, r, "justified_by_trace"), Fail)
	if r.Verdict != Fail {
		t.Fatal(r.Verdict)
	}
}

func TestB9AnOperationOutsideTheVocabularyFails(t *testing.T) {
	rev := goodRevision()
	rev.Operation = "PROMOTE"
	wantVerdict(t, byName(t, GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, rev)), "operation_valid"), Fail)
}

func TestB9HumanChoiceAloneCannotRaiseStatusAboveCandidate(t *testing.T) {
	rev := goodRevision()
	rev.Status = "provisional"
	wantVerdict(t, byName(t, GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, rev)), "human_choice_not_truth"), Fail)
	rev.Evidence = append(rev.Evidence, RevisionEvidence{Kind: "customer_reaction", RefID: "r1", Polarity: "positive", ActivityID: "a1"})
	wantVerdict(t, byName(t, GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong", Customer: []string{"positive"}}, rev)), "human_choice_not_truth"), Pass)
}

func TestB9CustomerEvidenceMustBeRecordedSeparately(t *testing.T) {
	rev := goodRevision() // the trace has a customer reply but the revision recorded only the human edit
	r := GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong", Customer: []string{"positive"}}, rev))
	wantVerdict(t, byName(t, r, "customer_evidence_separate"), Fail)
}

func TestB9AStatusOnlyScopeFails(t *testing.T) {
	rev := goodRevision()
	rev.ApplicabilityConditions = nil
	wantVerdict(t, byName(t, GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, rev)), "scope_explicit"), Fail)
}

func TestB9AnExceptionRevisionNeedsAnException(t *testing.T) {
	rev := goodRevision()
	rev.Operation, rev.VersionBefore, rev.Version, rev.PriorKept = "ADD_EXCEPTION", 1, 2, true
	rev.Evidence = []RevisionEvidence{{Kind: "customer_reaction", RefID: "r1", Polarity: "negative", ActivityID: "a1"}}
	tr := RevisionTrace{KnowledgeInScope: true, Customer: []string{"negative"}, DistinguishingCondition: true}
	wantVerdict(t, byName(t, GradeB9(b9Episode(tr, rev)), "scope_explicit"), Fail)
	rev.Exceptions = []knowledge.Exception{{Description: "security review open", Conditions: []knowledge.Condition{eq("stage", "Security")}}}
	if r := GradeB9(b9Episode(tr, rev)); r.Verdict != Pass {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
}

func TestB9PriorKnowledgeMustBePreservedOnRevision(t *testing.T) {
	rev := goodRevision()
	rev.Operation, rev.VersionBefore, rev.Version, rev.PriorKept = "NARROW", 1, 2, false
	tr := RevisionTrace{KnowledgeInScope: true, HumanNarrows: true, HumanEdit: true, SignalStrength: "strong"}
	wantVerdict(t, byName(t, GradeB9(b9Episode(tr, rev)), "history_preserved"), Fail)
}

func TestB9AWallClockTimestampFails(t *testing.T) {
	rev := goodRevision()
	rev.OccurredAt = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	wantVerdict(t, byName(t, GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "strong"}, rev)), "replay_time"), Fail)
}

func TestB9NoRevisionWhenTheTraceSaysNoChangeIsAPass(t *testing.T) {
	r := GradeB9(b9Episode(RevisionTrace{HumanEdit: true, SignalStrength: "weak"}))
	if r.Verdict != Pass || r.Observed == "" {
		t.Fatalf("%s %s", r.Verdict, r.Why)
	}
}

func TestB9MissingRevisionWhenTheTraceDemandsOneFails(t *testing.T) {
	r := GradeB9(b9Episode(RevisionTrace{KnowledgeInScope: true, Customer: []string{"positive"}}))
	if r.Verdict != Fail {
		t.Fatalf("%s %s", r.Verdict, r.Why)
	}
}

func TestB9WithoutATraceIsNotMeasured(t *testing.T) {
	ep := goodEpisode()
	if r := GradeB9(ep); r.Verdict != Unknown {
		t.Fatalf("verdict %s", r.Verdict)
	}
}
