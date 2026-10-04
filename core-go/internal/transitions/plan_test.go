package transitions

import (
	"errors"
	"testing"
	"time"
)

var (
	asOf      = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	computed  = asOf.Add(7 * time.Second)
	account   = "0a000000-0000-4000-8000-000000000001"
	activity  = "0ac70000-0000-4000-8000-000000000101"
	evidenced = []Ref{{ActivityID: activity}}
)

func meta() Meta {
	return Meta{AccountID: account, CurrentState: "REORG", StateVersion: 4, RuleSetVersion: "transition_rules:v1", AsOf: asOf,
		ComputedAt: computed, TriggerActivityIDs: []string{activity}}
}

func fact(key string, required, satisfied bool) FactResult {
	f := FactResult{Key: key, Description: key, Required: required, Satisfied: satisfied, EvidenceRefs: []Ref{}, SignalIDs: []string{}}
	if satisfied {
		f.EvidenceRefs = evidenced
	}
	return f
}

func candidateOutcome() Outcome {
	return Outcome{Status: StatusCandidate, ToState: "EXPANSION", RuleID: "reorg_to_expansion", Confidence: 0.4,
		Supporting: []FactResult{fact("expansion_need_stated", true, true)}, Missing: []FactResult{fact("owner_stabilized", true, false)}}
}

func TestPlanNoOutcomeWritesNothing(t *testing.T) {
	next, changed, err := Plan(nil, Outcome{}, meta())
	if err != nil || changed || next != nil {
		t.Fatalf("got %v %v %v, want nothing", next, changed, err)
	}
}

func TestPlanFirstObservationCreatesARecordFromTheCurrentState(t *testing.T) {
	next, changed, err := Plan(nil, candidateOutcome(), meta())
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if next.FromState != "REORG" || next.Status != StatusCandidate || *next.ToStateCandidate != "EXPANSION" || next.StateVersion != 4 ||
		!next.FirstObservedAt.Equal(asOf) || !next.LastUpdatedAt.Equal(asOf) || next.ConfirmedAt != nil || next.ID != "" {
		t.Errorf("unexpected record: %+v", next)
	}
	if len(next.ContradictingFacts) != 0 || next.ContradictingFacts == nil {
		t.Errorf("contradicting facts must be an empty list, not null: %#v", next.ContradictingFacts)
	}
}

func TestPlanAnUnchangedEvaluationWritesNoHistory(t *testing.T) {
	first, _, _ := Plan(nil, candidateOutcome(), meta())
	first.ID = "1e5a0000-0000-4000-8000-000000000001"
	m := meta()
	m.StateVersion, m.AsOf = 5, asOf.Add(time.Hour)
	next, changed, err := Plan(first, candidateOutcome(), m)
	if err != nil || changed || next != nil {
		t.Fatalf("got %v %v %v, want no change", next, changed, err)
	}
}

func TestPlanConfirmingAnOpenCandidateKeepsItsIdentity(t *testing.T) {
	first, _, _ := Plan(nil, candidateOutcome(), meta())
	first.ID = "1e5a0000-0000-4000-8000-000000000001"
	m := meta()
	m.StateVersion, m.AsOf, m.ComputedAt = 6, asOf.Add(48*time.Hour), asOf.Add(48*time.Hour+3*time.Second)
	out := Outcome{Status: StatusConfirmed, ToState: "EXPANSION", Confidence: 1, Supporting: []FactResult{fact("owner_stabilized", true, true)},
		ConfirmedAt: &m.ComputedAt}
	next, changed, err := Plan(first, out, m)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if next.ID != first.ID || !next.FirstObservedAt.Equal(asOf) || next.FromState != "REORG" || next.Status != StatusConfirmed ||
		next.ConfirmedAt == nil || !next.ConfirmedAt.Equal(m.ComputedAt) || !next.LastUpdatedAt.Equal(m.AsOf) || next.StateVersion != 6 {
		t.Errorf("unexpected record: %+v", next)
	}
}

func TestPlanALostGateClearsTheTarget(t *testing.T) {
	first, _, _ := Plan(nil, candidateOutcome(), meta())
	out := Outcome{Status: StatusUnresolved, Supporting: []FactResult{fact("expansion_need_stated", true, true)}}
	next, changed, err := Plan(first, out, meta())
	if err != nil || !changed || next.ToStateCandidate != nil || next.Status != StatusUnresolved {
		t.Fatalf("got %+v %v %v, want UNRESOLVED with no target", next, changed, err)
	}
}

func TestPlanRejectionAndStaleClosureStampTheirTime(t *testing.T) {
	first, _, _ := Plan(nil, candidateOutcome(), meta())
	no := true
	contra := fact("support_risk_high", false, true)
	contra.Rejects = &no
	rejected, _, err := Plan(first, Outcome{Status: StatusRejected, ToState: "EXPANSION", Contradicting: []FactResult{contra}}, meta())
	if err != nil || rejected.RejectedAt == nil || !rejected.RejectedAt.Equal(asOf) || rejected.ConfirmedAt != nil {
		t.Fatalf("rejected = %+v, err %v", rejected, err)
	}
	open := &Record{ID: "x", AccountID: account, FromState: "REORG", Status: StatusUnresolved, FirstObservedAt: asOf.Add(-40 * 24 * time.Hour),
		LastUpdatedAt: asOf.Add(-31 * 24 * time.Hour), SupportingFacts: []FactResult{}, MissingFacts: []FactResult{}, ContradictingFacts: []FactResult{}}
	closed, changed, err := Plan(open, Outcome{Status: StatusUnresolved, Closed: true}, meta())
	if err != nil || !changed || closed.ClosedAt == nil || closed.CloseReason == nil || *closed.CloseReason != CloseReasonStale || closed.Open() {
		t.Fatalf("closed = %+v changed=%v err=%v", closed, changed, err)
	}
}

func TestPlanTimesNeverRunBackwards(t *testing.T) {
	first, _, _ := Plan(nil, candidateOutcome(), meta())
	m := meta()
	m.AsOf, m.StateVersion = asOf.Add(-time.Hour), 5 // an out-of-order backfill evaluates at an earlier as_of
	out := candidateOutcome()
	out.Confidence = 0.6
	next, _, _ := Plan(first, out, m)
	if next.LastUpdatedAt.Before(first.LastUpdatedAt) {
		t.Errorf("last_updated_at went backwards: %v < %v", next.LastUpdatedAt, first.LastUpdatedAt)
	}
	early := meta()
	early.ComputedAt = asOf.Add(-time.Minute)
	confirmed, _, _ := Plan(nil, Outcome{Status: StatusConfirmed, ToState: "EXPANSION", Supporting: []FactResult{fact("a", true, true)}}, early)
	if confirmed.ConfirmedAt.Before(confirmed.FirstObservedAt) {
		t.Errorf("confirmed_at %v precedes first_observed_at %v", confirmed.ConfirmedAt, confirmed.FirstObservedAt)
	}
}

func TestPlanRefusesASatisfiedFactWithoutEvidence(t *testing.T) {
	bad := candidateOutcome()
	bad.Supporting = []FactResult{{Key: "expansion_need_stated", Required: true, Satisfied: true, EvidenceRefs: []Ref{}}}
	if _, _, err := Plan(nil, bad, meta()); !errors.Is(err, ErrUnevidenced) {
		t.Fatalf("err = %v, want ErrUnevidenced", err)
	}
}

func TestOpenRecordsExposeOnlyWhatTheStepsNeed(t *testing.T) {
	to := "EXPANSION"
	r := Record{Status: StatusCandidate, ToStateCandidate: &to, LastUpdatedAt: asOf}
	if o := r.AsOpen(); o.Status != StatusCandidate || o.ToStateCandidate != "EXPANSION" || !o.LastUpdatedAt.Equal(asOf) || !r.Open() {
		t.Errorf("AsOpen = %+v", o)
	}
	r.Status = StatusConfirmed
	if r.Open() {
		t.Error("a CONFIRMED record is not open")
	}
}
