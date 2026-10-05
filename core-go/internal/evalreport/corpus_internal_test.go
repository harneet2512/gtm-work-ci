package evalreport

import "testing"

// LOW: discovery yield counts only seed sources that are unexplained deltas, so it cannot exceed 1
// (verdict-seeded and explained-delta-seeded versions are counted in n_candidates_seeded, not in yield).
func TestDiscoveryYieldIsBoundedByUnexplainedDeltas(t *testing.T) {
	f := newFixture()
	f.w.deltas["u1"] = &deltaRow{ID: "u1", Unexplained: true}
	f.w.deltas["e1"] = &deltaRow{ID: "e1", Unexplained: false}
	f.w.episodes["ep"] = &episodeRow{ID: "ep"}
	f.w.versions["a:v2"] = &versionRow{Evaluator: "a", Version: 2, CreatedFrom: "human_delta", SourceDeltaID: "u1"}
	f.w.versions["b:v2"] = &versionRow{Evaluator: "b", Version: 2, CreatedFrom: "human_delta", SourceDeltaID: "e1"}
	f.w.versions["c:v2"] = &versionRow{Evaluator: "c", Version: 2, CreatedFrom: "human_delta", SourceDeltaID: "gone"}
	m := f.w.discovery(nil)
	if m.Value == nil || m.Value.Seeded != 3 || m.Value.Unexplained != 1 || m.Value.Yield == nil || !closeTo(*m.Value.Yield, 1.0) {
		t.Fatalf("discovery = %+v", m.Value)
	}
}

// LOW: coverage.this_version.seeded_by_this_version agrees with discovery's seededVersion, including the
// verdict-seeded shape (evaluator human_delta, created_from manual, knowledge-linked, no spec).
func TestCoverageSeededByThisAgreesWithSeededVersion(t *testing.T) {
	f := newFixture()
	f.w.deltas["d"] = &deltaRow{ID: "d"}
	verdictSeeded := &versionRow{Evaluator: "human_delta", Version: 2, CreatedFrom: "manual", KnowledgeID: "k"}
	k := versionKey{evaluator: "human_delta", version: 2, tag: "human_delta:v2"}
	m := f.w.coverage(k, verdictSeeded)
	if m.Value == nil || !m.Value.ThisVersion.SeededByThis {
		t.Fatalf("coverage = %+v", m.Value)
	}
	if d := f.w.discovery(verdictSeeded); !d.Value.ThisSeeded {
		t.Fatal("discovery disagrees")
	}
	plain := &versionRow{Evaluator: "grounding", Version: 1, CreatedFrom: "seed"}
	if m := f.w.coverage(versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"}, plain); m.Value.ThisVersion.SeededByThis {
		t.Fatal("a shipped seed is not seeded by a delta")
	}
}

// Deviation 4: the coverage metric states what it can and cannot see.
func TestCoverageStatesItsBasisAndLimits(t *testing.T) {
	f := newFixture()
	f.w.deltas["d"] = &deltaRow{ID: "d", Labels: []string{"corrected_fact"}}
	m := f.w.coverage(versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"}, nil)
	if m.Value == nil || m.Value.Basis == "" || len(m.Value.Limits) < 3 {
		t.Fatalf("coverage basis/limits = %q %v", m.Value.Basis, m.Value.Limits)
	}
}

// E19.7: the report always carries the repeated-judge-variance gap explicitly.
func TestReportCarriesTheTrackedGaps(t *testing.T) {
	f := newFixture()
	rep := f.w.report(versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"})
	if len(rep.TrackedGaps) != 1 || rep.TrackedGaps[0].ID != "E19.7" || rep.TrackedGaps[0].Status != "open" ||
		rep.TrackedGaps[0].Reason == "" || rep.TrackedGaps[0].Interim == "" {
		t.Fatalf("tracked gaps = %+v", rep.TrackedGaps)
	}
}
