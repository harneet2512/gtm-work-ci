package evalreport

import (
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
)

var (
	tProm = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	tEnd  = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	tBef  = tProm.Add(-24 * time.Hour)
	tAft  = tProm.Add(24 * time.Hour)
	tLate = tEnd.Add(24 * time.Hour)
)

func pentestSpec() learning.Spec {
	return learning.Spec{LiteralChanges: []learning.LiteralChange{{Kind: "attachment_added", After: "pentest.pdf"}}}
}

// P1 + P2: the repeat is the version's executable spec failing on the selected candidate AND passing the
// human's final draft; the windows are scoped to the version's account, the in-force period, and episodes
// the version actually ran on (after window).
func TestRepeatCorrectionIsScopedAndSpecExact(t *testing.T) {
	f := newFixture()
	const tag = "evidence_sufficiency:v1"
	k := versionKey{evaluator: "evidence_sufficiency", version: 1, tag: tag}
	v := &versionRow{Evaluator: k.evaluator, Version: 1, Status: "retired", AccountID: "acct",
		PromotedAt: &tProm, Spec: pentestSpec()}
	f.w.versions[tag] = v
	f.w.retired = map[string]time.Time{tag: tEnd}

	mk := func(at time.Time, account string, delta *deltaRow, cand []string, final *[]string, ran bool) *episodeRow {
		ep := f.decide(f.episode("APPROVE_WITH_EDIT", delta), at, account)
		f.drafts(ep, cand, final)
		if ran {
			f.ran(ep, tag)
		}
		return ep
	}
	// Before activation, in scope.
	mk(tBef, "acct", deltaOfKind("attachment_added"), nil, strs("pentest.pdf"), false)                    // a repeat
	mk(tBef, "acct", deltaOfKind("attachment_added"), nil, strs("other.pdf"), false)                      // same axis, different fix: NOT a repeat
	mk(tBef, "acct", deltaOfKind("subject_changed"), []string{"pentest.pdf"}, strs("pentest.pdf"), false) // hub-axis edit: NOT a repeat
	mk(tBef, "stranger", deltaOfKind("attachment_added"), nil, strs("pentest.pdf"), false)                // another account: out of scope
	// After activation, in force.
	mk(tAft, "acct", deltaOfKind("attachment_added"), nil, strs("pentest.pdf"), true)  // a repeat despite the version
	mk(tAft, "acct", deltaOfKind("attachment_added"), nil, nil, true)                  // no stored final: final = candidate, still fails -> not a repeat
	mk(tAft, "acct", deltaOfKind("attachment_added"), nil, strs("pentest.pdf"), false) // the version never ran on it: out of the after window
	// After retirement: out of both windows.
	mk(tLate, "acct", deltaOfKind("attachment_added"), nil, strs("pentest.pdf"), true)
	// A draft that cannot be reconstructed is unscorable, not silently dropped or matched.
	f.decide(f.episode("APPROVE_WITH_EDIT", deltaOfKind("attachment_added")), tBef, "acct") // no candidate rows stored

	m := f.w.repeatCorrection(k, v, f.w.allEvals)
	if m.Status != "ok" || m.Value == nil {
		t.Fatalf("repeatCorrection = %+v", m)
	}
	rc := m.Value
	if rc.Method != "spec_replay" || rc.Scope != "account acct" || rc.InForceUntil == nil || !rc.InForceUntil.Equal(tEnd) {
		t.Fatalf("header = %+v", rc)
	}
	b, a := rc.Before, rc.After
	if b.Episodes != 4 || b.Deltas != 4 || b.Matching != 1 || b.Unscorable != 1 ||
		b.Rate == nil || !closeTo(*b.Rate, 0.25) || b.EpisodeRate == nil || !closeTo(*b.EpisodeRate, 0.25) {
		t.Fatalf("before = %+v", b)
	}
	if a.Episodes != 2 || a.Deltas != 2 || a.Matching != 1 || a.Rate == nil || !closeTo(*a.Rate, 0.5) {
		t.Fatalf("after = %+v", a)
	}
}

func TestRepeatCorrectionGlobalVersionSpansAccounts(t *testing.T) {
	f := newFixture()
	const tag = "evidence_sufficiency:v2"
	k := versionKey{evaluator: "evidence_sufficiency", version: 2, tag: tag}
	v := &versionRow{Evaluator: k.evaluator, Version: 2, Status: "active", PromotedAt: &tProm, Spec: pentestSpec()}
	for _, acct := range []string{"a", "b"} {
		ep := f.decide(f.episode("APPROVE_WITH_EDIT", deltaOfKind("attachment_added")), tBef, acct)
		f.drafts(ep, nil, strs("pentest.pdf"))
	}
	m := f.w.repeatCorrection(k, v, nil)
	if m.Value == nil || m.Value.Scope != "global" || m.Value.Before.Episodes != 2 || m.Value.Before.Matching != 2 {
		t.Fatalf("repeatCorrection = %+v", m)
	}
}

// P2 fallback: a version whose spec cannot execute (paragraph edits are semantic) matches on an exact
// semantic label of its source delta only, never on the broad axis.
func TestRepeatCorrectionLabelFallbackWhenSpecCannotExecute(t *testing.T) {
	f := newFixture()
	const tag = "next_action_quality:v2"
	k := versionKey{evaluator: "next_action_quality", version: 2, tag: tag}
	f.w.deltas["src"] = &deltaRow{ID: "src", Labels: []string{"reduced_pressure"}}
	v := &versionRow{Evaluator: k.evaluator, Version: 2, Status: "active", PromotedAt: &tProm, SourceDeltaID: "src",
		Spec: learning.Spec{LiteralChanges: []learning.LiteralChange{{Kind: "paragraph_edited"}}}}
	mk := func(labels ...string) {
		d := deltaOfKind("paragraph_edited") // same broad axis as the criterion
		d.Labels = labels
		f.decide(f.episode("APPROVE_WITH_EDIT", d), tBef, "acct")
	}
	mk("reduced_pressure", "style_only") // exact label: a repeat
	mk("style_only")                     // same axis, different semantics: not
	mk()                                 // unlabeled: not
	m := f.w.repeatCorrection(k, v, nil)
	rc := m.Value
	if rc == nil || rc.Method != "label_match" || rc.Before.Deltas != 3 || rc.Before.Matching != 1 {
		t.Fatalf("repeatCorrection = %+v", m)
	}
	// No labels on the source delta and no executable spec: nothing to match on.
	f.w.deltas["src"] = &deltaRow{ID: "src"}
	if m := f.w.repeatCorrection(k, v, nil); m.Status != "n/a" || !strings.Contains(m.Reason, "label") {
		t.Fatalf("labelless fallback = %+v", m)
	}
}

func TestRepeatCorrectionEdgeCases(t *testing.T) {
	k := versionKey{evaluator: "evidence_sufficiency", version: 1, tag: "evidence_sufficiency:v1"}
	t.Run("no promotion is n/a", func(t *testing.T) {
		f := newFixture()
		if m := f.w.repeatCorrection(k, &versionRow{Status: "candidate"}, nil); m.Status != "n/a" {
			t.Fatalf("%+v", m)
		}
	})
	t.Run("nil version is n/a", func(t *testing.T) {
		f := newFixture()
		if m := f.w.repeatCorrection(k, nil, nil); m.Status != "n/a" {
			t.Fatalf("%+v", m)
		}
	})
	t.Run("the seeding delta is not a repeat of itself", func(t *testing.T) {
		f := newFixture()
		seed := deltaOfKind("attachment_added")
		seed.ID = "seed"
		ep := f.decide(f.episode("APPROVE_WITH_EDIT", seed), tBef, "acct")
		f.drafts(ep, nil, strs("pentest.pdf"))
		v := &versionRow{Evaluator: k.evaluator, Version: 1, Status: "active", PromotedAt: &tProm,
			SourceDeltaID: "seed", Spec: pentestSpec()}
		m := f.w.repeatCorrection(k, v, nil)
		if m.Value == nil || m.Value.Before.Matching != 0 || !m.Value.SeedExcluded || m.Value.Before.Episodes != 0 {
			t.Fatalf("%+v", m.Value)
		}
	})
	t.Run("an episode with no human decision cannot be placed in a window", func(t *testing.T) {
		f := newFixture()
		ep := f.episode("APPROVE_UNCHANGED", nil)
		ep.HasDecision = false
		v := &versionRow{Evaluator: k.evaluator, Version: 1, Status: "active", PromotedAt: &tProm, Spec: pentestSpec()}
		m := f.w.repeatCorrection(k, v, nil)
		if m.Value == nil || m.Value.Before.Episodes != 0 || m.Value.After.Episodes != 0 {
			t.Fatalf("%+v", m.Value)
		}
	})
}

func TestInForceUntil(t *testing.T) {
	f := newFixture()
	mk := func(ev string, n int, acct string, promoted *time.Time) *versionRow {
		v := &versionRow{Evaluator: ev, Version: n, AccountID: acct, PromotedAt: promoted}
		f.w.versions[keyOf(ev, n).tag] = v
		return v
	}
	later := tEnd
	v1 := mk("grounding", 1, "", &tProm)
	mk("grounding", 2, "", &later) // a global successor retires v1
	if got := f.w.inForceUntil(v1); got == nil || !got.Equal(later) {
		t.Fatalf("superseded = %v", got)
	}
	// A successor on another account does not retire an account-scoped version.
	a1 := mk("momentum", 1, "a", &tProm)
	mk("momentum", 2, "b", &later)
	if got := f.w.inForceUntil(a1); got != nil {
		t.Fatalf("a stranger-account successor ended the window: %v", got)
	}
	// An explicit retirement row wins when earlier.
	early := tProm.Add(48 * time.Hour)
	f.w.retired = map[string]time.Time{"grounding:v1": early}
	if got := f.w.inForceUntil(v1); got == nil || !got.Equal(early) {
		t.Fatalf("retired = %v", got)
	}
}

// S5: criterionMatch is an exact label intersection — the broad change-kind axis never matches.
func TestCriterionMatchTable(t *testing.T) {
	labels := map[string]bool{"reduced_pressure": true}
	cases := []struct {
		name string
		d    *deltaRow
		want bool
	}{
		{"nil delta", nil, false},
		{"no labels", &deltaRow{}, false},
		{"exact label", &deltaRow{Labels: []string{"reduced_pressure"}}, true},
		{"one of several", &deltaRow{Labels: []string{"style_only", "reduced_pressure"}}, true},
		{"other label", &deltaRow{Labels: []string{"increased_pressure"}}, false},
		{"axis only", func() *deltaRow { d := deltaOfKind("paragraph_edited"); d.Suggested = "next_action_quality"; return d }(), false},
	}
	for _, tc := range cases {
		if got := criterionMatch(tc.d, labels); got != tc.want {
			t.Errorf("%s: criterionMatch = %v, want %v", tc.name, got, tc.want)
		}
	}
	if criterionMatch(&deltaRow{Labels: []string{"reduced_pressure"}}, map[string]bool{}) {
		t.Error("an empty criterion label set must match nothing")
	}
}
