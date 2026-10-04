package evalreport

import (
	"strings"
	"testing"
)

func infer(id, episode, agreement, verdict string, history ...string) *inferenceRow {
	return &inferenceRow{ID: id, EpisodeID: episode, Agreement: agreement, Verdict: verdict, History: history}
}

// The human-vs-inference agreement: the share of Ghost's inferences the human confirmed, read from the
// append-only judgment_verdicts history (latest answer wins), with revisions and the agent-agreement
// slice reported.
func TestInferenceAgreement(t *testing.T) {
	f := newFixture()
	f.w.inferences = map[string]*inferenceRow{
		"e1": infer("i1", "e1", "agreed", "confirmed", "confirmed"),
		"e2": infer("i2", "e2", "overrode", "corrected", "confirmed", "corrected"), // revised
		"e3": infer("i3", "e3", "overrode", "pending"),                             // unanswered
		"e4": infer("i4", "e4", "overrode", "confirmed"),                           // pre-history row: falls back to the column
	}
	for _, id := range []string{"e1", "e2", "e3", "e4"} {
		f.w.episodes[id] = &episodeRow{ID: id}
	}
	byEp := map[string][]*evalRow{"e1": nil, "e2": nil, "e3": nil} // the version judged e1..e3
	m := f.w.inferenceAgreement(byEp)
	a := m.Value
	if m.Status != "ok" || a == nil {
		t.Fatalf("inferenceAgreement = %+v", m)
	}
	if a.Inferences != 4 || a.Answered != 3 || a.Confirmed != 2 || a.Corrected != 1 || a.Pending != 1 {
		t.Fatalf("counts = %+v", a)
	}
	if a.ConfirmRate == nil || !closeTo(*a.ConfirmRate, 2.0/3) {
		t.Fatalf("confirm_rate = %v", a.ConfirmRate)
	}
	if a.VerdictRows != 3 || a.Revised != 1 {
		t.Fatalf("history = rows %d revised %d", a.VerdictRows, a.Revised)
	}
	ag, ov := a.ByAgentAgreement["agreed"], a.ByAgentAgreement["overrode"]
	if ag.N != 1 || ag.Confirmed != 1 || ov.N != 3 || ov.Answered != 2 || ov.Confirmed != 1 {
		t.Fatalf("by agreement = %+v / %+v", ag, ov)
	}
	if a.ThisVersion.N != 3 || a.ThisVersion.Answered != 2 || a.ThisVersion.Confirmed != 1 ||
		a.ThisVersion.ConfirmRate == nil || !closeTo(*a.ThisVersion.ConfirmRate, 0.5) {
		t.Fatalf("this version = %+v", a.ThisVersion)
	}
}

func TestInferenceAgreementEdgeCases(t *testing.T) {
	t.Run("no inferences is n/a", func(t *testing.T) {
		f := newFixture()
		if m := f.w.inferenceAgreement(nil); m.Status != "n/a" || !strings.Contains(m.Reason, "judgment_inferences") {
			t.Fatalf("%+v", m)
		}
	})
	t.Run("all pending is n/a and says how many", func(t *testing.T) {
		f := newFixture()
		f.w.inferences = map[string]*inferenceRow{"e1": infer("i1", "e1", "agreed", "pending")}
		m := f.w.inferenceAgreement(nil)
		if m.Status != "n/a" || !strings.Contains(m.Reason, "1") {
			t.Fatalf("%+v", m)
		}
	})
	t.Run("a version that judged no inferred episode has an empty slice", func(t *testing.T) {
		f := newFixture()
		f.w.inferences = map[string]*inferenceRow{"e1": infer("i1", "e1", "agreed", "confirmed", "confirmed")}
		m := f.w.inferenceAgreement(map[string][]*evalRow{"other": nil})
		if m.Value == nil || m.Value.ThisVersion.N != 0 || m.Value.ThisVersion.ConfirmRate != nil {
			t.Fatalf("%+v", m.Value)
		}
	})
}
