package evalreport

import (
	"strings"
	"testing"
)

func withConf(rows []*evalRow, c float64) []*evalRow {
	for _, r := range rows {
		cc := c
		r.Confidence = &cc
	}
	return rows
}

// S5 + P3: calibCorrect edge table. A pass on an edited episode is never usable — whether or not the
// edit is attributed to the axis, the stored rows cannot tell which artifact the pass judged.
func TestCalibCorrectTable(t *testing.T) {
	const evaluator, tag = "grounding", "grounding:v1"
	cases := []struct {
		name        string
		action      string
		delta       *deltaRow
		cited       bool
		verdict     string
		wantCorrect bool
		wantUsable  bool
		reason      string
	}{
		{name: "pass on unchanged approval", action: "APPROVE_UNCHANGED", verdict: "pass", wantCorrect: true, wantUsable: true},
		{name: "fail on unchanged approval", action: "APPROVE_UNCHANGED", verdict: "fail", wantCorrect: false, wantUsable: true},
		{name: "pass on a reject whose reason names the axis", action: "REJECT", reason: "Grounding: invented a number", verdict: "pass", wantCorrect: false, wantUsable: true},
		{name: "fail on a reject whose reason names the axis", action: "REJECT", reason: "bad grounding", verdict: "fail", wantCorrect: true, wantUsable: true},
		{name: "pass on a reject for another reason", action: "REJECT", reason: "wrong channel for this buyer", verdict: "pass"},
		{name: "fail on a reject with no reason", action: "REJECT", verdict: "fail"},
		{name: "pass on attributed edit", action: "APPROVE_WITH_EDIT", delta: deltaOfKind("paragraph_edited"), verdict: "pass"},
		{name: "pass on other-axis edit", action: "APPROVE_WITH_EDIT", delta: deltaOfKind("channel_changed"), verdict: "pass"},
		{name: "cited fail on edit", action: "APPROVE_WITH_EDIT", delta: deltaOfKind("paragraph_edited"), cited: true, verdict: "fail", wantCorrect: true, wantUsable: true},
		{name: "uncited fail on edit", action: "APPROVE_WITH_EDIT", delta: deltaOfKind("paragraph_edited"), verdict: "fail"},
		{name: "edit with no linked delta", action: "MANUAL_REPLACEMENT", verdict: "pass"},
		{name: "ignore", action: "IGNORE", verdict: "pass"},
		{name: "warn is not a verdict to confirm", action: "APPROVE_UNCHANGED", verdict: "warn"},
		{name: "abstain is not a verdict to confirm", action: "REJECT", reason: "grounding", verdict: "abstain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			ep := f.episode(tc.action, tc.delta)
			ep.Reason = tc.reason
			r := f.rows(ep, evaluator, tag, tc.verdict)[0]
			if tc.cited {
				f.w.explains[tc.delta.ID] = []explRow{{EvalRunID: r.ID, Evaluator: evaluator, Tag: tag}}
			}
			c, u := f.w.calibCorrect(r, ep)
			if c != tc.wantCorrect || u != tc.wantUsable {
				t.Fatalf("calibCorrect = (%v,%v), want (%v,%v)", c, u, tc.wantCorrect, tc.wantUsable)
			}
		})
	}
}

// S5: ECE edge cases — no rows, score-only rows, one class, boundary confidences.
func TestCalibrationECEEdgeCases(t *testing.T) {
	const evaluator, tag = "grounding", "grounding:v1"
	k := versionKey{evaluator: evaluator, version: 1, tag: tag}

	t.Run("n=0 is n/a", func(t *testing.T) {
		f := newFixture()
		if m := f.w.calibration(k, nil); m.Status != "n/a" || m.Reason == "" {
			t.Fatalf("calibration = %+v", m)
		}
	})
	t.Run("score without confidence is n/a naming score", func(t *testing.T) {
		f := newFixture()
		rows := f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "pass")
		s := 0.9
		rows[0].Score = &s
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Status != "n/a" || !strings.Contains(m.Reason, "score") {
			t.Fatalf("calibration = %+v", m)
		}
	})
	t.Run("a single correct class at confidence 1.0", func(t *testing.T) {
		f := newFixture()
		withConf(f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "pass"), 1.0)
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Status != "ok" || m.Value.ECE == nil || !closeTo(*m.Value.ECE, 0) || len(m.Value.Buckets) != 1 ||
			m.Value.Buckets[0].Range != "[0.9,1]" {
			t.Fatalf("calibration = %+v", m.Value)
		}
	})
	t.Run("confidently wrong", func(t *testing.T) {
		f := newFixture()
		withConf(f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "fail"), 0.95)
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Value == nil || m.Value.ECE == nil || !closeTo(*m.Value.ECE, 0.95) {
			t.Fatalf("calibration = %+v", m)
		}
	})
	t.Run("bucket boundaries are lower-inclusive", func(t *testing.T) {
		f := newFixture()
		withConf(f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "pass"), 0.5)
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Value == nil || len(m.Value.Buckets) != 1 || m.Value.Buckets[0].Range != "[0.5,0.7]" {
			t.Fatalf("calibration = %+v", m)
		}
	})
	t.Run("rejections are scored only when their reason points at the axis", func(t *testing.T) {
		f := newFixture()
		scoped := f.episode("REJECT", nil)
		scoped.Reason = "the grounding is off"
		withConf(f.rows(scoped, evaluator, tag, "fail"), 0.9)
		for i := 0; i < 2; i++ {
			unscoped := f.episode("REJECT", nil) // no stated reason: the fail may be right for the wrong reason
			withConf(f.rows(unscoped, evaluator, tag, "fail"), 0.9)
		}
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Status != "ok" || m.Value.Rows != 1 || m.Value.Rejections.AxisScoped != 1 || m.Value.Rejections.Unscoped != 2 {
			t.Fatalf("calibration = %+v", m.Value)
		}
	})
	t.Run("only unscoped rejections is n/a and says why", func(t *testing.T) {
		f := newFixture()
		withConf(f.rows(f.episode("REJECT", nil), evaluator, tag, "fail"), 0.9)
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Status != "n/a" || !strings.Contains(m.Reason, "rejection") {
			t.Fatalf("calibration = %+v", m)
		}
	})
	t.Run("a rejection whose reason names the axis with underscores or spaces", func(t *testing.T) {
		for _, reason := range []string{"next_step_quality is poor", "poor next step quality"} {
			f := newFixture()
			ep := f.episode("REJECT", nil)
			ep.Reason = reason
			withConf(f.rows(ep, "next_step_quality", "next_step_quality:v1", "fail"), 0.8)
			kk := versionKey{evaluator: "next_step_quality", version: 1, tag: "next_step_quality:v1"}
			if m := f.w.calibration(kk, f.byEpisode(kk.tag)); m.Status != "ok" {
				t.Fatalf("reason %q: %+v", reason, m)
			}
		}
	})
	t.Run("edited-episode passes contribute no rows", func(t *testing.T) {
		f := newFixture()
		withConf(f.rows(f.episode("APPROVE_WITH_EDIT", deltaOfKind("channel_changed")), evaluator, tag, "pass"), 0.8)
		m := f.w.calibration(k, f.byEpisode(tag))
		if m.Status != "n/a" {
			t.Fatalf("calibration = %+v", m)
		}
	})
}
