package evalreport

import (
	"errors"
	"testing"
)

// S1: the false-block rate is false blocks over the unchanged approvals the version judged — a
// fail-everything evaluator scores 1.0, not (false blocks / every fail verdict) ≈ 0.3.
func TestFalseBlockRateDenominatorIsUnchangedApprovals(t *testing.T) {
	f := newFixture()
	tag := "grounding:v1"
	for i := 0; i < 3; i++ { // three unchanged approvals, all failed
		f.rows(f.episode("APPROVE_UNCHANGED", nil), "grounding", tag, "fail")
	}
	for i := 0; i < 2; i++ { // two rejects, failed
		f.rows(f.episode("REJECT", nil), "grounding", tag, "fail")
	}
	for i := 0; i < 5; i++ { // five edits, failed but uncited
		f.rows(f.episode("APPROVE_WITH_EDIT", deltaOfKind("paragraph_edited")), "grounding", tag, "fail")
	}
	m := f.w.falseBlock(versionKey{evaluator: "grounding", version: 1, tag: tag}, f.byEpisode(tag))
	if m.Status != "ok" || m.Value == nil {
		t.Fatalf("falseBlock = %+v", m)
	}
	v := m.Value
	if v.UnchangedApprovals != 3 || v.FalseBlock != 3 || v.FailVerdicts != 10 {
		t.Fatalf("counts = %+v", v)
	}
	if v.Rate == nil || !closeTo(*v.Rate, 1.0) {
		t.Fatalf("rate = %v, want 1.0 (every unchanged approval was blocked)", v.Rate)
	}
	if v.FailPrecision == nil || !closeTo(*v.FailPrecision, 0.3) {
		t.Fatalf("fail_precision = %v, want the legacy 3/10", v.FailPrecision)
	}
}

func TestFalseBlockEdgeCases(t *testing.T) {
	k := versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"}
	t.Run("no verdicts at all is n/a", func(t *testing.T) {
		f := newFixture()
		if m := f.w.falseBlock(k, nil); m.Status != "n/a" || m.Reason == "" {
			t.Fatalf("falseBlock = %+v", m)
		}
	})
	t.Run("never failing an unchanged approval is a measured zero", func(t *testing.T) {
		f := newFixture()
		f.rows(f.episode("APPROVE_UNCHANGED", nil), "grounding", k.tag, "pass")
		m := f.w.falseBlock(k, f.byEpisode(k.tag))
		if m.Status != "ok" || m.Value.Rate == nil || *m.Value.Rate != 0 || m.Value.FailPrecision != nil {
			t.Fatalf("falseBlock = %+v", m.Value)
		}
	})
	t.Run("fails only on edits leaves the rate unmeasured", func(t *testing.T) {
		f := newFixture()
		f.rows(f.episode("REJECT", nil), "grounding", k.tag, "fail")
		m := f.w.falseBlock(k, f.byEpisode(k.tag))
		if m.Status != "ok" || m.Value.Rate != nil || m.Value.UnchangedApprovals != 0 {
			t.Fatalf("falseBlock = %+v", m.Value)
		}
	})
	t.Run("an abstain-only unchanged approval is not a judged approval", func(t *testing.T) {
		f := newFixture()
		f.rows(f.episode("APPROVE_UNCHANGED", nil), "grounding", k.tag, "abstain")
		if m := f.w.falseBlock(k, f.byEpisode(k.tag)); m.Status != "n/a" {
			t.Fatalf("falseBlock = %+v", m)
		}
	})
}

func closeTo(got, want float64) bool {
	d := got - want
	return d < 0.0001 && d > -0.0001
}

func TestSelectKeysRejectsUnknownEvaluatorAndVersion(t *testing.T) {
	f := newFixture()
	f.w.versions["grounding:v1"] = &versionRow{Evaluator: "grounding", Version: 1}
	f.w.versions["grounding:v2"] = &versionRow{Evaluator: "grounding", Version: 2}
	f.w.orderKeys()
	cases := []struct {
		name string
		opts Options
		want int // reports; -1 = error
	}{
		{"all", Options{}, 2},
		{"evaluator", Options{Evaluator: "grounding"}, 2},
		{"evaluator+version", Options{Evaluator: "grounding", Version: 2}, 1},
		{"unknown evaluator", Options{Evaluator: "momentum"}, -1},
		{"unknown version", Options{Evaluator: "grounding", Version: 3}, -1},
		{"version without evaluator", Options{Version: 1}, -1},
	}
	for _, tc := range cases {
		keys, err := f.w.selectKeys(tc.opts)
		if tc.want < 0 {
			if !errors.Is(err, ErrUnknownSelection) {
				t.Fatalf("%s: err = %v, want ErrUnknownSelection", tc.name, err)
			}
			continue
		}
		if err != nil || len(keys) != tc.want {
			t.Fatalf("%s: keys=%v err=%v, want %d keys", tc.name, keys, err, tc.want)
		}
	}
	// An empty world selects nothing under --all: a valid empty document, not an error.
	if keys, err := newFixture().w.selectKeys(Options{}); err != nil || len(keys) != 0 {
		t.Fatalf("empty world --all: %v %v", keys, err)
	}
}
