package evalreport_test

// The per-version assertions of TestReportWorld — one helper per report under test, each walking the
// hand-computed expectations the episode comments in report_test.go set up.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

func catOf(m evalreport.Metric[evalreport.Agreement], cat string) int {
	if m.Value == nil {
		return -1
	}
	return m.Value.Categories[cat]
}

// assertRecipientV1 covers the observed-only shipped v1 tag: agreement across unchanged, edited and
// false-blocked episodes, calibration ECE, and the same-input repeat disagreement.
func assertRecipientV1(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	r := reportOf(t, set, "recipient_correctness:v1")
	if r.Registered || r.Status != "observed" {
		t.Fatalf("rc:v1 registration = %v/%s, want observed", r.Registered, r.Status)
	}

	// E1 true_pass, E3 true_block_repaired (the refused rc fail), E4 false_block; E2/E5a/E5c are
	// passes on edits other axes caused (pass_other_axis) and are UNSCORED (P3, not "consistent"):
	// consistent 2, contradicted 1, unscored 3, rate 2/3; accepts 1 of 6.
	ag := r.Metrics.HumanAgreement
	if ag.Status != "ok" || ag.Value == nil {
		t.Fatalf("rc agreement = %q %q", ag.Status, ag.Reason)
	}
	a := ag.Value
	if a.EpisodesEvaluated != 6 || catOf(ag, "true_pass") != 1 || catOf(ag, "pass_other_axis") != 3 ||
		catOf(ag, "true_block_repaired") != 1 || catOf(ag, "false_block") != 1 {
		t.Fatalf("rc categories = %v (n=%d)", a.Categories, a.EpisodesEvaluated)
	}
	if a.Consistent != 2 || a.Contradicted != 1 || a.Unscored != 3 || a.Rate == nil || !closeTo(*a.Rate, 2.0/3) ||
		a.AbstainRate == nil || *a.AbstainRate != 0 || a.WorstCaseRate == nil || !closeTo(*a.WorstCaseRate, 2.0/3) {
		t.Fatalf("rc agreement rate = %+v", a)
	}
	if a.AcceptUnchanged != 1 || a.AcceptRate == nil || !closeTo(*a.AcceptRate, 1.0/6) {
		t.Fatalf("rc accept = %+v", a)
	}

	// E3's delta is attributed to rc (a recipient edit added Priya to cc) and the delta's
	// explanations cite rc:v1's refused fail -> a true detection, not a false pass.
	fp := r.Metrics.FalsePass
	if fp.Status != "ok" || fp.Value == nil || fp.Value.AttributedEdits != 1 ||
		fp.Value.TrueDetection != 1 || fp.Value.FalsePass != 0 ||
		fp.Value.Rate == nil || *fp.Value.Rate != 0 {
		t.Fatalf("rc false_pass = %+v", fp)
	}

	// Two episodes carry a fail verdict: E3 (refused, cited -> repaired) and E4 (unchanged ->
	// false block). rate = 1/2.
	fb := r.Metrics.FalseBlock
	if fb.Status != "ok" || fb.Value == nil || fb.Value.FailVerdicts != 2 ||
		fb.Value.FalseBlock != 1 || fb.Value.FailUnresolved != 0 ||
		fb.Value.Rate == nil || !closeTo(*fb.Value.Rate, 0.5) {
		t.Fatalf("rc false_block = %+v", fb)
	}

	// Two confidence rows: .9 pass correct (E1), .7 fail wrong (E4, unchanged approval).
	// Buckets [0.7,0.9): n1 mean .7 obs 0; [0.9,1]: n1 mean .9 obs 1 -> ECE = .35+.05 = .4.
	cal := r.Metrics.Calibration
	if cal.Status != "ok" || cal.Value == nil || cal.Value.Rows != 2 || cal.Value.ECE == nil ||
		!closeTo(*cal.Value.ECE, 0.4) || len(cal.Value.Buckets) != 2 {
		t.Fatalf("rc calibration = %+v", cal)
	}

	// Same-input repeats: E1's group agrees (gen pass == send pass), E4's disagrees (gen fail vs
	// send pass); E3's group is skipped (the artifact was edited). rate = 1/2, no spec replay.
	con := r.Metrics.Consistency
	if con.Status != "ok" || con.Value == nil || con.Value.RepeatedGroups != 2 ||
		con.Value.Agreeing != 1 || con.Value.Rate == nil || !closeTo(*con.Value.Rate, 0.5) ||
		con.Value.SkippedInputChanged != 1 || con.Value.Replay != nil {
		t.Fatalf("rc consistency = %+v", con)
	}

	// Corpus metrics carry the same corpus numbers on every version's report.
	ex := r.Metrics.Explained
	if ex.Status != "ok" || ex.Value == nil || ex.Value.Deltas != 4 || ex.Value.ExplainedDeltas != 1 ||
		ex.Value.Rate == nil || !closeTo(*ex.Value.Rate, 0.25) ||
		ex.Value.ByThisVersion != 1 || ex.Value.ByThisAxis != 1 || ex.Value.AxisAttributed != 1 {
		t.Fatalf("rc explained = %+v", ex)
	}
	di := r.Metrics.Discovery
	if di.Status != "ok" || di.Value == nil || di.Value.Episodes != 7 || di.Value.Unexplained != 3 ||
		di.Value.Seeded != 3 || di.Value.Yield == nil || !closeTo(*di.Value.Yield, 1.0) ||
		di.Value.PerEpisodes == nil || !closeTo(*di.Value.PerEpisodes, 3.0/7) || di.Value.ThisSeeded {
		t.Fatalf("rc discovery = %+v", di)
	}
	cov := r.Metrics.Coverage
	if cov.Status != "ok" || cov.Value == nil || cov.Value.ThisVersion.AxisDeltas != 1 ||
		cov.Value.ThisVersion.SeededByThis {
		t.Fatalf("rc coverage = %+v", cov)
	}

	// rc:v1 was never promoted -> no activation split.
	rc := r.Metrics.RepeatCorrection
	if rc.Status != "n/a" || rc.Reason == "" {
		t.Fatalf("rc repeat_correction = %+v", rc)
	}
}

// assertGroundingV1 covers the semantic observed tag: a true pass, a false pass on an attributed
// paragraph edit, and a true block on the discard; plus the unusable-confidence drop.
func assertGroundingV1(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	r := reportOf(t, set, "grounding:v1")
	ag := r.Metrics.HumanAgreement
	if ag.Status != "ok" || ag.Value == nil || ag.Value.EpisodesEvaluated != 3 ||
		catOf(ag, "true_pass") != 1 || catOf(ag, "false_pass") != 1 || catOf(ag, "true_block_reject") != 1 ||
		ag.Value.Rate == nil || !closeTo(*ag.Value.Rate, 2.0/3) {
		t.Fatalf("grounding agreement = %+v", ag)
	}

	// One attributed edit (E2's paragraph rewrite) and the version passed -> false pass, rate 1.
	fp := r.Metrics.FalsePass
	if fp.Status != "ok" || fp.Value == nil || fp.Value.AttributedEdits != 1 ||
		fp.Value.FalsePass != 1 || fp.Value.TrueDetection != 0 ||
		fp.Value.Rate == nil || !closeTo(*fp.Value.Rate, 1.0) {
		t.Fatalf("grounding false_pass = %+v", fp)
	}

	// One fail verdict (E6, rejected) -> no false block; the rate is 0, not n/a.
	fb := r.Metrics.FalseBlock
	if fb.Status != "ok" || fb.Value == nil || fb.Value.FailVerdicts != 1 || fb.Value.FalseBlock != 0 ||
		fb.Value.Rate == nil || *fb.Value.Rate != 0 {
		t.Fatalf("grounding false_block = %+v", fb)
	}

	// Calibration: E1's .8 pass is confirmed; E6's .4 fail is confirmed by the reject; E2's .6 pass
	// is DROPPED — on an attributed edit the judged artifact (candidate vs final) is unknowable.
	cal := r.Metrics.Calibration
	if cal.Status != "ok" || cal.Value == nil || cal.Value.Rows != 2 || cal.Value.ECE == nil ||
		!closeTo(*cal.Value.ECE, 0.4) || len(cal.Value.Buckets) != 2 ||
		cal.Value.Rejections.AxisScoped != 1 || cal.Value.Rejections.Unscoped != 0 {
		t.Fatalf("grounding calibration = %+v", cal.Value)
	}

	// Semantic evals are never re-run at send -> no same-artifact repeat exists.
	con := r.Metrics.Consistency
	if con.Status != "n/a" || con.Reason == "" {
		t.Fatalf("grounding consistency = %+v", con)
	}
	if r.Metrics.Coverage.Value == nil || r.Metrics.Coverage.Value.ThisVersion.AxisDeltas != 1 {
		t.Fatalf("grounding coverage = %+v", r.Metrics.Coverage)
	}
	ex := r.Metrics.Explained
	if ex.Value == nil || ex.Value.ByThisVersion != 0 || ex.Value.AxisAttributed != 1 {
		t.Fatalf("grounding explained = %+v", ex)
	}
	if r.Metrics.RepeatCorrection.Status != "n/a" {
		t.Fatalf("grounding repeat_correction = %+v", r.Metrics.RepeatCorrection)
	}
}

// assertEvidenceV1 is the headline version: a promoted learned axis with a true block repaired, a
// false block on the unedited no-attachment send, false passes when the correction slips by anyway,
// spec replay, and the before/after repeat-correction split.
func assertEvidenceV1(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	r := reportOf(t, set, "evidence_sufficiency:v1")
	if !r.Registered || r.Status != "active" || r.PromotedAt == nil || !r.PromotedAt.Equal(tMid) {
		t.Fatalf("es:v1 header = %+v", r)
	}

	// E1 true_pass, E2 pass_other_axis (unscored), E4 false_block, E5a/E5c false_pass, and E3
	// fail_unresolved_edit: the axis fail on the refused draft is uncited — the repaired send's
	// pass reuses its content-free id and is dropped (axis result ids hash run+draft+axis+clock,
	// never the judged artifact — a WP21 write-path property this report honestly shows).
	// -> consistent 1, contradicted 3, unscored 2 (E2's other-axis pass, E3) -> rate 1/4; one unchanged accept.
	ag := r.Metrics.HumanAgreement
	if ag.Status != "ok" || ag.Value == nil || ag.Value.EpisodesEvaluated != 6 ||
		catOf(ag, "true_pass") != 1 || catOf(ag, "pass_other_axis") != 1 ||
		catOf(ag, "fail_unresolved_edit") != 1 || catOf(ag, "false_block") != 1 ||
		catOf(ag, "false_pass") != 2 ||
		ag.Value.Consistent != 1 || ag.Value.Contradicted != 3 || ag.Value.Unscored != 2 ||
		ag.Value.Rate == nil || !closeTo(*ag.Value.Rate, 0.25) {
		t.Fatalf("es agreement = %+v", *ag.Value)
	}

	// Three attachment-attributed edits: E5a and E5c slipped (false pass); E3's stored fail was
	// not the cited explanation -> fail_not_cited.
	fp := r.Metrics.FalsePass
	if fp.Status != "ok" || fp.Value == nil || fp.Value.AttributedEdits != 3 ||
		fp.Value.FalsePass != 2 || fp.Value.TrueDetection != 0 || fp.Value.FailNotCited != 1 ||
		fp.Value.Rate == nil || !closeTo(*fp.Value.Rate, 2.0/3) {
		t.Fatalf("es false_pass = %+v", *fp.Value)
	}

	// Two episodes with a fail: E4 unchanged (false block), E3 edited and uncited (unresolved).
	fb := r.Metrics.FalseBlock
	if fb.Status != "ok" || fb.Value == nil || fb.Value.FailVerdicts != 2 || fb.Value.FalseBlock != 1 ||
		fb.Value.FailUnresolved != 1 || fb.Value.Rate == nil || !closeTo(*fb.Value.Rate, 0.5) {
		t.Fatalf("es false_block = %+v", *fb.Value)
	}

	// human_delta rows carry no confidence.
	if r.Metrics.Calibration.Status != "n/a" || r.Metrics.Calibration.Reason == "" {
		t.Fatalf("es calibration = %+v", r.Metrics.Calibration)
	}

	// E4's group repeats on the identical artifact and agrees (fail==fail) -> rate 1.0; E3's
	// refused fail is a singleton (its repaired pass was dropped on the id collision) so nothing
	// is skipped. Spec replay: 7 stored human_delta rows; E3's refused fail judged the
	// attachment-less artifact the stored final no longer holds, so it is EXCLUDED with its reason
	// (not counted as a mismatch) -> 6 replayable, 6 matching.
	con := r.Metrics.Consistency
	if con.Status != "ok" || con.Value == nil || con.Value.RepeatedGroups != 1 ||
		con.Value.Agreeing != 1 || con.Value.Rate == nil || *con.Value.Rate != 1 ||
		con.Value.SkippedInputChanged != 0 || con.Value.Replay == nil ||
		con.Value.Replay.Replayable != 6 || con.Value.Replay.Matching != 6 ||
		con.Value.Replay.Rate == nil || !closeTo(*con.Value.Replay.Rate, 1.0) ||
		con.Value.Replay.Excluded != 1 || len(con.Value.Replay.ExcludedRows) != 1 ||
		con.Value.Replay.ExcludedRows[0].Reason == "" {
		t.Fatalf("es consistency = %+v", *con.Value)
	}

	// Coverage: this axis owns three deltas; the corpus shows every observed label.
	cov := r.Metrics.Coverage
	if cov.Status != "ok" || cov.Value == nil || cov.Value.ThisVersion.AxisDeltas != 3 ||
		cov.Value.ThisVersion.SeededByThis {
		t.Fatalf("es coverage = %+v", cov)
	}
	labels := map[string]evalreport.LabelCoverage{}
	for _, l := range cov.Value.Labels {
		labels[l.Label] = l
	}
	if cc := labels["corrected_fact"]; cc.Deltas != 3 || cc.Unexplained != 2 || cc.Seeded != 2 || !cc.Covered {
		t.Fatalf("corrected_fact coverage = %+v", cc)
	}
	if rp := labels["reduced_pressure"]; rp.Deltas != 1 || rp.Unexplained != 1 || rp.Seeded != 1 || !rp.Covered {
		t.Fatalf("reduced_pressure coverage = %+v", rp)
	}
	if ams := labels["added_missing_stakeholder"]; ams.Deltas != 1 || ams.Unexplained != 0 || !ams.Covered {
		t.Fatalf("added_missing_stakeholder coverage = %+v", ams)
	}
	if len(cov.Value.Unobserved) != 9 {
		t.Fatalf("unobserved labels = %v", cov.Value.Unobserved)
	}
	if len(cov.Value.SuggestedAxes) != 2 {
		t.Fatalf("suggested axes = %+v", cov.Value.SuggestedAxes)
	}

	// No explanation cites this axis (its repaired row is un-citable by id collision) — but three
	// deltas attribute to the axis.
	ex := r.Metrics.Explained
	if ex.Value == nil || ex.Value.ByThisVersion != 0 || ex.Value.ByThisAxis != 0 || ex.Value.AxisAttributed != 3 {
		t.Fatalf("es explained = %+v", *ex.Value)
	}

	// Repeat semantic correction (P1/P2): the repeat is the version's executable spec failing the
	// selected candidate AND passing the human's final draft. Before activation (account-scoped, all
	// six decided episodes) 3 deltas exist but only E5a repeats the correction (E3's candidate already
	// carried the pen-test attachment — the old axis match counted it); after, E5c — the version ran
	// on it — repeats it despite being in force.
	rc := r.Metrics.RepeatCorrection
	if rc.Status != "ok" || rc.Value == nil || !rc.Value.PromotedAt.Equal(tMid) ||
		rc.Value.Method != "spec_replay" || !strings.HasPrefix(rc.Value.Scope, "account ") ||
		rc.Value.InForceUntil != nil || rc.Value.SeedExcluded {
		t.Fatalf("es repeat_correction = %+v", rc)
	}
	b, a := rc.Value.Before, rc.Value.After
	if b.Episodes != 6 || b.Deltas != 3 || b.Matching != 1 || b.Unscorable != 0 || b.Rate == nil || !closeTo(*b.Rate, 1.0/3) ||
		b.MatchingEpisode != 1 || b.EpisodeRate == nil || !closeTo(*b.EpisodeRate, 1.0/6) {
		t.Fatalf("es before window = %+v", b)
	}
	if a.Episodes != 1 || a.Deltas != 1 || a.Matching != 1 || a.Rate == nil || *a.Rate != 1 ||
		a.MatchingEpisode != 1 || a.EpisodeRate == nil || *a.EpisodeRate != 1 {
		t.Fatalf("es after window = %+v", a)
	}
	// Axis fail windows: six pre-activation rows (E3's repaired pass was dropped on the id
	// collision — the stored corpus only ever holds the refused fail), three fails (E3, E4's two);
	// post-activation the E5c pass.
	af := rc.Value.AxisFail
	if af.BeforeN != 6 || af.BeforeFail != 3 || af.BeforeRate == nil || !closeTo(*af.BeforeRate, 0.5) ||
		af.AfterN != 1 || af.AfterFail != 0 || af.AfterRate == nil || *af.AfterRate != 0 {
		t.Fatalf("es axis fail = %+v", af)
	}
}

// assertNaqV1 covers the observed semantic tag whose unflagged pass is the false pass its own axis
// then learned from — E2's paragraph edit seeds next_action_quality:v2 (asserted below).
func assertNaqV1(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	r := reportOf(t, set, "next_action_quality:v1")
	if r.Registered || r.Status != "observed" {
		t.Fatalf("naq:v1 header = %+v", r)
	}
	ag := r.Metrics.HumanAgreement
	if ag.Status != "ok" || ag.Value == nil || ag.Value.EpisodesEvaluated != 1 ||
		catOf(ag, "false_pass") != 1 || ag.Value.Rate == nil || *ag.Value.Rate != 0 {
		t.Fatalf("naq agreement = %+v", *ag.Value)
	}
	fp := r.Metrics.FalsePass
	if fp.Status != "ok" || fp.Value == nil || fp.Value.AttributedEdits != 1 || fp.Value.FalsePass != 1 {
		t.Fatalf("naq false_pass = %+v", *fp.Value)
	}
	// The .9 pass on an attributed edit is unusable for calibration; a semantic eval never re-runs
	// at send -> both metrics are n/a with reasons.
	if r.Metrics.Calibration.Status != "n/a" || r.Metrics.Calibration.Reason == "" ||
		r.Metrics.Consistency.Status != "n/a" || r.Metrics.Consistency.Reason == "" {
		t.Fatalf("naq calibration/consistency = %+v / %+v", r.Metrics.Calibration, r.Metrics.Consistency)
	}
	cov := r.Metrics.Coverage
	if cov.Value == nil || cov.Value.ThisVersion.AxisDeltas != 2 || cov.Value.ThisVersion.SeededByThis {
		t.Fatalf("naq coverage = %+v", *cov.Value)
	}
	if di := r.Metrics.Discovery; di.Value == nil || di.Value.ThisSeeded {
		t.Fatalf("naq discovery = %+v", *di.Value)
	}
	if r.Metrics.RepeatCorrection.Status != "n/a" {
		t.Fatalf("naq repeat_correction = %+v", r.Metrics.RepeatCorrection)
	}
}

// assertSeededAndBareVersions: the candidates the unexplained deltas seeded (es:v2/v3 from the
// repeated attachment correction, naq:v2 from the paragraph edit — the first learned version of an
// axis is v2 because v1 is the shipped baseline) and the bare cta_calibration:v9 row the
// empty-world test left — registered but never run -> per-version metrics n/a, corpus metrics ok.
func assertSeededAndBareVersions(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	for _, tag := range []string{"evidence_sufficiency:v2", "evidence_sufficiency:v3", "next_action_quality:v2"} {
		r := reportOf(t, set, tag)
		if !r.Registered || r.Status != "candidate" || r.CreatedFrom != "human_delta" || r.SourceDeltaID == "" {
			t.Fatalf("%s header = %+v", tag, r)
		}
		if r.Metrics.HumanAgreement.Status != "n/a" || r.Metrics.RepeatCorrection.Status != "n/a" ||
			r.Metrics.Discovery.Value == nil || !r.Metrics.Discovery.Value.ThisSeeded {
			t.Fatalf("%s metrics = %+v", tag, r.Metrics)
		}
	}
	bare := reportOf(t, set, "cta_calibration:v9")
	if !bare.Registered || bare.Metrics.HumanAgreement.Status != "n/a" ||
		bare.Metrics.Consistency.Status != "n/a" {
		t.Fatalf("bare cta_calibration:v9 = %+v", bare.Metrics)
	}
	// next_step_quality:v1 — observed only — passed the discarded draft: a pass_rejected. Its .5 pass sits
	// on a rejection whose reason points at grounding, so it is dropped from calibration (unscoped).
	if c := reportOf(t, set, "next_step_quality:v1").Metrics.Calibration; c.Status != "n/a" ||
		!strings.Contains(c.Reason, "rejection") {
		t.Fatalf("nsq calibration = %+v", c)
	}
	nsq := reportOf(t, set, "next_step_quality:v1")
	if nsq.Registered || nsq.Metrics.HumanAgreement.Value == nil ||
		catOf(nsq.Metrics.HumanAgreement, "pass_rejected") != 1 ||
		nsq.Metrics.HumanAgreement.Value.Contradicted != 1 {
		t.Fatalf("nsq agreement = %+v", nsq.Metrics.HumanAgreement)
	}
}

// assertSelection covers the Options filter the CLI flags map to.
func assertSelection(t *testing.T) {
	t.Helper()
	set := generate(t, evalreport.Options{Evaluator: "evidence_sufficiency"})
	if len(set.Reports) != 3 {
		t.Fatalf("--evaluator evidence_sufficiency = %v, want v1,v2,v3", keysOf(set))
	}
	set = generate(t, evalreport.Options{Evaluator: "evidence_sufficiency", Version: 2})
	if len(set.Reports) != 1 || set.Reports["evidence_sufficiency:v2"] == nil {
		t.Fatalf("--evaluator --version selection = %v", keysOf(set))
	}
	if _, err := evalreport.Generate(context.Background(), env.DB,
		evalreport.Options{Evaluator: "nonexistent_axis"}); !errors.Is(err, evalreport.ErrUnknownSelection) {
		t.Fatalf("unknown evaluator: err = %v, want ErrUnknownSelection", err)
	}
	if _, err := evalreport.Generate(context.Background(), env.DB,
		evalreport.Options{Evaluator: "evidence_sufficiency", Version: 99}); !errors.Is(err, evalreport.ErrUnknownSelection) {
		t.Fatalf("unknown version: err = %v, want ErrUnknownSelection", err)
	}
	// The document marshals — the CLI emits exactly this.
	if _, err := json.Marshal(set); err != nil {
		t.Fatalf("marshal report set: %v", err)
	}
}

// assertAttribution (P3): every report carries its axis's attribution coverage. rc is reachable
// through recipient edits; momentum-like axes the kind map never reaches report unattributable.
func assertAttribution(t *testing.T, set evalreport.ReportSet) {
	t.Helper()
	rc := reportOf(t, set, "recipient_correctness:v1").Attribution
	if !rc.Attributable || rc.Axis != "recipient_correctness" || len(rc.ChangeKinds) == 0 ||
		rc.EditedEpisodes != 4 || rc.AttributedEdits != 1 || rc.Share == nil || !closeTo(*rc.Share, 0.25) {
		t.Fatalf("rc attribution = %+v", rc)
	}
	bare := reportOf(t, set, "cta_calibration:v9").Attribution
	if !bare.Attributable || bare.EditedEpisodes != 0 || bare.Share != nil {
		t.Fatalf("bare attribution = %+v", bare)
	}
}
