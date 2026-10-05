package evalreport

import (
	"strings"
	"testing"
)

// S5 + P3 + P4: the categorize edge-case table. Each row builds one decided episode with the stated
// action / delta / verdict rows and asserts the agreement bucket.
func TestCategorizeTable(t *testing.T) {
	const evaluator, tag = "grounding", "grounding:v1"
	attributed := func() *deltaRow { return deltaOfKind("paragraph_edited") } // grounding is in the kind map
	otherAxis := func() *deltaRow { return deltaOfKind("channel_changed") }   // channel_appropriateness only
	cases := []struct {
		name     string
		action   string
		delta    func() *deltaRow
		cited    bool // the delta's explanations cite this version
		verdicts []string
		noDecide bool // the episode has no linked human_decisions row
		want     string
	}{
		{"ignore is never scored", "IGNORE", nil, false, []string{"pass"}, false, catUnscored},
		{"unchanged pass", "APPROVE_UNCHANGED", nil, false, []string{"pass"}, false, catTruePass},
		{"unchanged fail", "APPROVE_UNCHANGED", nil, false, []string{"fail"}, false, catFalseBlock},
		{"unchanged fail beats pass", "APPROVE_UNCHANGED", nil, false, []string{"pass", "fail"}, false, catFalseBlock},
		{"unchanged abstain-only", "APPROVE_UNCHANGED", nil, false, []string{"abstain"}, false, catAbstain},
		{"unchanged warn-only", "APPROVE_UNCHANGED", nil, false, []string{"warn"}, false, catWarn},
		{"abstain wins over warn", "APPROVE_UNCHANGED", nil, false, []string{"warn", "abstain"}, false, catAbstain},
		{"unchanged with no rows", "APPROVE_UNCHANGED", nil, false, nil, false, catUnscored},
		{"reject fail", "REJECT", nil, false, []string{"fail"}, false, catTrueBlockReject},
		{"reject pass", "REJECT", nil, false, []string{"pass"}, false, catPassRejected},
		{"reject abstain", "REJECT", nil, false, []string{"abstain"}, false, catAbstain},
		{"edit cited fail", "APPROVE_WITH_EDIT", attributed, true, []string{"fail"}, false, catTrueBlockRepaired},
		{"edit uncited fail", "APPROVE_WITH_EDIT", attributed, false, []string{"fail"}, false, catFailUnresolved},
		{"edit pass on an attributed axis", "APPROVE_WITH_EDIT", attributed, false, []string{"pass"}, false, catFalsePass},
		{"edit pass on another axis", "MANUAL_REPLACEMENT", otherAxis, false, []string{"pass"}, false, catPassOtherAxis},
		{"edit warn-only", "APPROVE_WITH_EDIT", attributed, false, []string{"warn"}, false, catWarn},
		{"edit with no linked delta", "APPROVE_WITH_EDIT", nil, false, []string{"pass"}, false, catUnscored},
		{"decided episode with no human decision", "APPROVE_UNCHANGED", nil, false, []string{"pass"}, true, catUnscored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			var d *deltaRow
			if tc.delta != nil {
				d = tc.delta()
			}
			ep := f.episode(tc.action, d)
			ep.HasDecision = !tc.noDecide
			rows := f.rows(ep, evaluator, tag, tc.verdicts...)
			if tc.cited && d != nil {
				f.w.explains[d.ID] = []explRow{{EvalRunID: "x", Evaluator: evaluator, Tag: tag, Verdict: "fail"}}
			}
			ev := &episodeVerdict{episode: ep, rows: rows}
			if got := f.w.categorize(ev, evaluator, tag); got != tc.want {
				t.Fatalf("categorize = %s, want %s", got, tc.want)
			}
		})
	}
}

// P3: a never-failing judge on edits other axes caused is not "consistent" — those passes are
// unscored, and the agreement invariants hold: every episode lands in exactly one bucket.
func TestAgreementOtherAxisPassesAreUnscoredAndCountsPartition(t *testing.T) {
	f := newFixture()
	const evaluator, tag = "momentum", "momentum:v1" // an axis the kind map never reaches
	f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "pass")
	for i := 0; i < 4; i++ {
		f.rows(f.episode("APPROVE_WITH_EDIT", deltaOfKind("recipient_added")), evaluator, tag, "pass")
	}
	f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "abstain")
	f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "warn")
	f.rows(f.episode("REJECT", nil), evaluator, tag, "pass")

	m := f.w.agreement(versionKey{evaluator: evaluator, version: 1, tag: tag}, f.byEpisode(tag))
	a := m.Value
	if a == nil {
		t.Fatalf("agreement = %+v", m)
	}
	if a.Consistent != 1 || a.Contradicted != 1 || a.Unscored != 4 || a.Abstained != 1 || a.Warned != 1 {
		t.Fatalf("buckets = %+v", a)
	}
	if got := a.Consistent + a.Contradicted + a.Unscored + a.Abstained + a.Warned; got != a.EpisodesEvaluated {
		t.Fatalf("buckets sum to %d of %d episodes", got, a.EpisodesEvaluated)
	}
	if a.Rate == nil || !closeTo(*a.Rate, 0.5) {
		t.Fatalf("rate = %v, want 1/2 (no inflation from the four other-axis passes)", a.Rate)
	}
}

// P4: abstain is its own bucket; abstain_rate is reported and the worst-case agreement counts abstains
// as wrong.
func TestAgreementAbstainRateAndWorstCase(t *testing.T) {
	f := newFixture()
	const evaluator, tag = "grounding", "grounding:v1"
	for i := 0; i < 3; i++ {
		f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "pass")
	}
	f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "fail")
	f.rows(f.episode("APPROVE_UNCHANGED", nil), evaluator, tag, "abstain")
	m := f.w.agreement(versionKey{evaluator: evaluator, version: 1, tag: tag}, f.byEpisode(tag))
	a := m.Value
	if a.Rate == nil || !closeTo(*a.Rate, 0.75) { // 3 of 4 decisive
		t.Fatalf("rate = %v", a.Rate)
	}
	if a.AbstainRate == nil || !closeTo(*a.AbstainRate, 0.2) { // 1 of 5 episodes
		t.Fatalf("abstain_rate = %v", a.AbstainRate)
	}
	if a.WorstCaseRate == nil || !closeTo(*a.WorstCaseRate, 0.6) { // 3 / (3+1+1)
		t.Fatalf("worst_case_rate = %v", a.WorstCaseRate)
	}
}

func TestAgreementNoEpisodesIsNA(t *testing.T) {
	f := newFixture()
	if m := f.w.agreement(versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"}, nil); m.Status != "n/a" || m.Reason == "" {
		t.Fatalf("agreement = %+v", m)
	}
}

// P3: per-axis attribution coverage. An axis the kind map never reaches and no delta suggests is
// unattributable; false_pass says so instead of "no edits".
func TestAttributionCoverageAndUnattributableFalsePass(t *testing.T) {
	f := newFixture()
	const tag = "momentum:v1"
	f.rows(f.episode("APPROVE_WITH_EDIT", deltaOfKind("recipient_added")), "momentum", tag, "pass")
	f.rows(f.episode("APPROVE_WITH_EDIT", deltaOfKind("paragraph_edited")), "momentum", tag, "pass")
	k := versionKey{evaluator: "momentum", version: 1, tag: tag}
	by := f.byEpisode(tag)

	ac := f.w.attribution(k, by)
	if ac.Attributable || ac.EditedEpisodes != 2 || ac.AttributedEdits != 0 || ac.Share == nil || *ac.Share != 0 {
		t.Fatalf("attribution = %+v", ac)
	}
	fp := f.w.falsePass(k, by)
	if fp.Status != "n/a" || !strings.Contains(fp.Reason, "unattributable") {
		t.Fatalf("false_pass = %+v, want n/a 'unattributable'", fp)
	}

	// A reachable axis with no matching edit says "no edits", not "unattributable".
	g := newFixture()
	g.rows(g.episode("APPROVE_UNCHANGED", nil), "grounding", "grounding:v1", "pass")
	gk := versionKey{evaluator: "grounding", version: 1, tag: "grounding:v1"}
	gfp := g.w.falsePass(gk, g.byEpisode("grounding:v1"))
	if gfp.Status != "n/a" || strings.Contains(gfp.Reason, "unattributable") {
		t.Fatalf("grounding false_pass = %+v", gfp)
	}
	if gac := g.w.attribution(gk, g.byEpisode("grounding:v1")); !gac.Attributable || gac.EditedEpisodes != 0 || gac.Share != nil {
		t.Fatalf("grounding attribution = %+v", gac)
	}

	// An axis becomes attributable when a delta in the corpus suggests it.
	f.w.deltas["suggest"] = &deltaRow{ID: "suggest", Suggested: "momentum"}
	if !f.w.attribution(k, by).Attributable {
		t.Fatal("a suggested_eval_type must make the axis attributable")
	}
}
