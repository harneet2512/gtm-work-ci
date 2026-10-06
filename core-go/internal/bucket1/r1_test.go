package bucket1

import "testing"

// Rule R1 probes from the Bucket 1 review: a vacuous or empty input must never pass, and a ref that names
// nothing in the episode is not evidence.

func TestR1AnEmptyB9TraceIsUnknownNotNoChange(t *testing.T) {
	ep := Episode{ID: "ep-empty", At: t0, Trace: &RevisionTrace{}}
	r := GradeB9(ep)
	if r.Verdict != Unknown || len(r.EvidenceRefs) != 0 {
		t.Fatalf("empty trace = %s with %v", r.Verdict, r.EvidenceRefs)
	}
	// the same with a real activity present: nothing happened, so there is still nothing to pass on
	ep = goodEpisode()
	ep.Trace = &RevisionTrace{}
	if r := GradeB9(ep); r.Verdict != Unknown {
		t.Fatalf("empty trace on a real episode = %s", r.Verdict)
	}
}

func TestR1AnEpisodeWithOnlyAGraphDiffFlagIsUnknownForB4(t *testing.T) {
	ep := Episode{ID: "ep-empty", At: t0, GraphDiffKnown: true}
	r := GradeB4(ep)
	if r.Verdict != Unknown || len(r.EvidenceRefs) != 0 {
		t.Fatalf("%s with %v", r.Verdict, r.EvidenceRefs)
	}
}

func TestR1APrecedentWithNothingObservedIsUnknownForB6(t *testing.T) {
	ep := goodEpisode()
	ep.Precedents = []Precedent{{ID: "pr-empty", CreatedAt: t0.Add(-time24)}}
	r := GradeB6(ep)
	if r.Verdict != Unknown {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
	wantVerdict(t, byName(t, r, "outcome_represented_and_separate"), Unknown)
}

const time24 = 24 * 3600e9

func TestR1ARefThatNamesNothingInTheEpisodeIsNotEvidence(t *testing.T) {
	ep := goodEpisode()
	for _, ref := range []Ref{{ActivityID: ep.ID}, {ActivityID: "ghost"}, {Note: "just words"}, {}} {
		r := rollup(ep, "B2", JudgedObject{Type: "Episode", ID: ep.ID}, []Assertion{{Name: "a", Verdict: Pass, Refs: []Ref{ref}}})
		if r.Verdict != Unknown || len(r.EvidenceRefs) != 0 {
			t.Fatalf("ref %+v: %s with %v", ref, r.Verdict, r.EvidenceRefs)
		}
	}
	r := rollup(ep, "B2", JudgedObject{Type: "Episode", ID: ep.ID}, []Assertion{{Name: "a", Verdict: Pass, Refs: []Ref{{ActivityID: "a1"}}}})
	if r.Verdict != Pass || len(r.EvidenceRefs) != 1 {
		t.Fatalf("a real ref must prove the pass: %s %v", r.Verdict, r.EvidenceRefs)
	}
}

func TestR1NothingToCheckNeverLiftsAGateToPass(t *testing.T) {
	ep := goodEpisode()
	r := rollup(ep, "B2", JudgedObject{Type: "Episode", ID: ep.ID}, []Assertion{na("x", "nothing"), na("y", "nothing")})
	if r.Verdict != Unknown {
		t.Fatalf("%s", r.Verdict)
	}
	// ...and it does not hide a real pass either
	r = rollup(ep, "B2", JudgedObject{Type: "Episode", ID: ep.ID}, []Assertion{na("x", "nothing"), pass("y", "ok", Ref{ActivityID: "a1"})})
	if r.Verdict != Pass {
		t.Fatalf("%s", r.Verdict)
	}
}

func TestR1B8CounterEvidenceMustBeCitedNotMentioned(t *testing.T) {
	ep := goodEpisode()
	ep.Claims[0].Status = "conflicted"
	ep.Beliefs = []Belief{{Kind: "blocker", Statement: "blockers are in conflict", Refs: []Ref{{ActivityID: "a1"}}}}
	wantVerdict(t, byName(t, GradeB8(ep, nil), "counter_evidence_not_omitted"), Warn)
	ep.Beliefs[0].Refs = []Ref{{ActivityID: "a1", ClaimID: "c1"}}
	wantVerdict(t, byName(t, GradeB8(ep, nil), "counter_evidence_not_omitted"), Pass)
}
