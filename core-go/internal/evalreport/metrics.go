package evalreport

import (
	"sort"
)

// kindAxes maps a human_delta literal-change kind to the eval axes whose domain the edit touches.
// It mirrors strategystore's fallbackCriterion axis map (recipient -> stakeholder_selection, channel
// -> channel_appropriateness, attachment -> evidence_sufficiency, else next_action_quality) extended
// to the full human_delta.v1.json kind vocabulary. It is a coarse attribution, not ground truth: the
// delta's own candidate_criterion.suggested_eval_type always wins, and explanations (the evals that
// provably predicted the edit) settle what actually caught it.
var kindAxes = map[string][]string{
	"recipient_added":        {"stakeholder_selection", "stakeholder_coverage", "recipient_correctness"},
	"recipient_removed":      {"stakeholder_selection", "stakeholder_coverage", "recipient_correctness"},
	"recipient_role_changed": {"stakeholder_selection", "stakeholder_coverage", "recipient_correctness"},
	"channel_changed":        {"channel_appropriateness"},
	"attachment_added":       {"evidence_sufficiency", "provenance_coverage"},
	"attachment_removed":     {"evidence_sufficiency", "provenance_coverage"},
	"subject_changed":        {"next_action_quality", "next_step_quality"},
	"paragraph_added":        {"grounding", "next_action_quality", "evidence_sufficiency"},
	"paragraph_removed":      {"grounding", "next_action_quality"},
	"paragraph_edited":       {"grounding", "next_action_quality", "rep_style"},
	"cta_changed":            {"cta_calibration", "next_action_quality"},
	"timing_changed":         {"timing_cadence", "next_action_quality"},
	"action_type_changed":    {"next_action_quality", "action_stage_fit"},
	"crm_next_step_changed":  {"crm_writeback", "next_step_quality"},
}

// semanticLabelVocab mirrors the human_delta.v1.json semantic_labels enum (the correction categories
// the coverage metric reports).
var semanticLabelVocab = []string{
	"reduced_pressure", "increased_pressure", "kept_champion_involved", "removed_unnecessary_stakeholders",
	"added_missing_stakeholder", "delayed_cta", "removed_cta", "smaller_ask", "larger_ask",
	"changed_channel", "corrected_fact", "deferred_to_buyer_timing", "style_only",
}

const falsePassBasis = "a delta is attributed to an axis by its candidate_criterion.suggested_eval_type " +
	"or by the literal-change-kind axis map (mirrors the seed-time fallbackCriterion heuristic); " +
	"a false pass is an attributed edit where no stored " +
	"judgment of the version ever failed the draft (explanation-cited fails are true detections)"

// trackedGaps are the explicit gaps every report carries. E19.7 (repeated-judge variance) needs a
// sampled re-judge pass: result ids are content-hashed over the whole input, so re-running a judge on an
// identical input reuses the stored row and the database never holds two independent judgments of one
// input. A re-judge pass needs a live model, which this read-only report never calls.
var trackedGaps = []TrackedGap{{
	ID:     "E19.7",
	Name:   "repeated-judge variance",
	Status: "open",
	Reason: "identical inputs reuse the stored eval_runs id, so no pair of independent judgments exists to " +
		"compare; measuring it needs a sampled re-judge pass with a live model, which this read-only report " +
		"never runs",
	Interim: "repeat_consistency.spec_replay (deterministic re-derivation of executable shadow_specs) and " +
		"same-artifact re-judgments on unchanged approvals",
}}

// report computes one version's document.
func (w *world) report(k versionKey) *Report {
	v := w.versions[k.tag]
	rep := &Report{Evaluator: k.evaluator, Version: k.version, Tag: k.tag, Metrics: Metrics{}}
	if v == nil {
		v = &versionRow{Evaluator: k.evaluator, Version: k.version, Status: "observed"}
	}
	rep.Registered = v.Status != "observed"
	rep.Status = v.Status
	rep.Kind = v.Kind
	rep.CreatedFrom = v.CreatedFrom
	rep.AccountID = v.AccountID
	rep.PromotedAt = v.PromotedAt
	rep.SourceDeltaID = v.SourceDeltaID
	rep.SpecError = v.SpecErr

	byEpisode, all := w.evalsByTag(k.tag)
	rep.TrackedGaps = trackedGaps
	rep.Attribution = w.attribution(k, byEpisode)
	rep.Metrics.HumanAgreement = w.agreement(k, byEpisode)
	rep.Metrics.FalsePass = w.falsePass(k, byEpisode)
	rep.Metrics.FalseBlock = w.falseBlock(k, byEpisode)
	rep.Metrics.Calibration = w.calibration(k, byEpisode)
	rep.Metrics.Consistency = w.consistency(k, v)
	rep.Metrics.Coverage = w.coverage(k, v)
	rep.Metrics.Explained = w.explained(k)
	rep.Metrics.Discovery = w.discovery(v)
	rep.Metrics.RepeatCorrection = w.repeatCorrection(k, v, all)
	rep.Metrics.InferenceAgree = w.inferenceAgreement(byEpisode)
	return rep
}

// episodeVerdict is the version's whole stored judgment of one decided episode's final draft. Rows of
// one (run, draft, tag, kind) slot cannot be temporally ordered — eval_runs.created_at is the run's
// replay clock, not the write time — so agreement uses EXISTS semantics: a cited explanation row is
// provably a pre-decision failure the human's edit repaired; an uncited fail is a flag that persisted
// or appeared on the final artifact; "no fail at all" is the only provable pass-on-the-reviewed-draft.
type episodeVerdict struct {
	episode *episodeRow
	rows    []*evalRow
}

// attachEpisodes resolves the episode pointer of each (episode, tag) group.
func (w *world) attachEpisodes(byEpisode map[string][]*evalRow) []*episodeVerdict {
	out := make([]*episodeVerdict, 0, len(byEpisode))
	for epID, rows := range byEpisode {
		ep := w.episodes[epID]
		if ep == nil {
			continue
		}
		out = append(out, &episodeVerdict{episode: ep, rows: rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].episode.ID < out[j].episode.ID })
	return out
}

// deltaOf resolves the episode's delta row.
func (w *world) deltaOf(ep *episodeRow) *deltaRow {
	if ep.HumanDeltaID == "" {
		return nil
	}
	return w.deltas[ep.HumanDeltaID]
}

// attributedAxes is the axis set a delta's correction touches: its suggested_eval_type plus the
// literal-change-kind map. Explanations are not attribution — they are the evals that DID catch it.
func attributedAxes(d *deltaRow) map[string]bool {
	out := map[string]bool{}
	if d.Suggested != "" {
		out[d.Suggested] = true
	}
	for _, ch := range d.Changes {
		for _, ax := range kindAxes[ch.Kind] {
			out[ax] = true
		}
	}
	return out
}

// citedBy reports whether the delta's explanations cite an eval result of the tag (version-exact)
// or of the evaluator (any version).
func (w *world) citedBy(deltaID, tag, evaluator string) (byVersion, byAxis bool) {
	for _, x := range w.explains[deltaID] {
		if x.Tag == tag {
			byVersion = true
		}
		if x.Evaluator == evaluator {
			byAxis = true
		}
	}
	return byVersion, byAxis
}

// hasFail reports whether any row of the group verdicts fail.
func hasFail(rows []*evalRow) bool {
	for _, r := range rows {
		if r.Verdict == "fail" {
			return true
		}
	}
	return false
}

// hasPass reports whether any row of the group verdicts pass.
func hasPass(rows []*evalRow) bool {
	for _, r := range rows {
		if r.Verdict == "pass" {
			return true
		}
	}
	return false
}

// axisKinds inverts kindAxes: the literal-change kinds that attribute an edit to an axis.
var axisKinds = func() map[string][]string {
	out := map[string][]string{}
	for kind, axes := range kindAxes {
		for _, ax := range axes {
			out[ax] = append(out[ax], kind)
		}
	}
	for _, kinds := range out {
		sort.Strings(kinds)
	}
	return out
}()

// attribution reports how well edits can be attributed to the version's axis: whether any change kind
// or any corpus delta's suggested_eval_type reaches it, and the share of the edited episodes the
// version judged that were attributed to it. An unattributable axis can never show a false pass, so
// its false-pass rate is reported as unattributable rather than as "no edits".
func (w *world) attribution(k versionKey, byEpisode map[string][]*evalRow) AttributionCoverage {
	ac := AttributionCoverage{Axis: k.evaluator, ChangeKinds: axisKinds[k.evaluator]}
	for _, d := range w.deltas {
		if d.Suggested == k.evaluator {
			ac.SuggestedDeltas++
		}
	}
	ac.Attributable = len(ac.ChangeKinds) > 0 || ac.SuggestedDeltas > 0
	for _, ev := range w.attachEpisodes(byEpisode) {
		ep := ev.episode
		if ep.HumanAction != "APPROVE_WITH_EDIT" && ep.HumanAction != "MANUAL_REPLACEMENT" {
			continue
		}
		d := w.deltaOf(ep)
		if d == nil {
			continue
		}
		ac.EditedEpisodes++
		if attributedAxes(d)[k.evaluator] {
			ac.AttributedEdits++
		}
	}
	if ac.EditedEpisodes > 0 {
		s := float64(ac.AttributedEdits) / float64(ac.EditedEpisodes)
		ac.Share = &s
	}
	ac.Basis = "an edit is attributed to an axis by candidate_criterion.suggested_eval_type or the " +
		"literal-change-kind map; an axis neither reaches is unattributable (false_pass cannot be measured)"
	return ac
}

// falsePass counts attributed edits the version never flagged: on an edited episode whose delta is
// attributed to the evaluator's axis, a cited fail is a true detection, an uncited fail is a flag
// that did not drive the fix, and no fail at all is a false pass. An axis nothing attributes edits to
// is reported n/a "unattributable" — distinct from an attributable axis that simply saw no edits.
func (w *world) falsePass(k versionKey, byEpisode map[string][]*evalRow) Metric[FalsePass] {
	fp := FalsePass{Basis: falsePassBasis}
	for _, ev := range w.attachEpisodes(byEpisode) {
		ep := ev.episode
		edited := ep.HumanAction == "APPROVE_WITH_EDIT" || ep.HumanAction == "MANUAL_REPLACEMENT"
		if !edited {
			continue
		}
		d := w.deltaOf(ep)
		if d == nil || !attributedAxes(d)[k.evaluator] {
			continue
		}
		byVersion, _ := w.citedBy(d.ID, k.tag, k.evaluator)
		fp.AttributedEdits++
		switch {
		case byVersion:
			fp.TrueDetection++
		case hasFail(ev.rows):
			fp.FailNotCited++
		default:
			fp.FalsePass++
		}
	}
	if fp.AttributedEdits == 0 {
		if !w.attribution(k, nil).Attributable {
			return na[FalsePass]("unattributable: no literal-change kind and no suggested_eval_type in the corpus " +
				"attributes an edit to " + k.evaluator + ", so " + k.tag + " can never show a false pass")
		}
		return na[FalsePass]("no human edit attributed to " + k.evaluator + " carries a " + k.tag + " verdict")
	}
	r := float64(fp.FalsePass) / float64(fp.AttributedEdits)
	fp.Rate = &r
	return avail(fp)
}

// falseBlock counts fail verdicts on drafts the human approved unchanged. rate = false blocks over the
// unchanged approvals the version judged; fail_precision is the legacy false blocks / fail verdicts. An
// uncited fail on an edited episode is reported separately (it flagged but was not what the human fixed).
func (w *world) falseBlock(k versionKey, byEpisode map[string][]*evalRow) Metric[FalseBlock] {
	fb := FalseBlock{}
	for _, ev := range w.attachEpisodes(byEpisode) {
		failed := hasFail(ev.rows)
		unchanged := ev.episode.HumanAction == "APPROVE_UNCHANGED"
		if unchanged && (failed || hasPass(ev.rows)) {
			fb.UnchangedApprovals++
		}
		if !failed {
			continue
		}
		fb.FailVerdicts++
		switch {
		case unchanged:
			fb.FalseBlock++
		case ev.episode.HumanAction == "APPROVE_WITH_EDIT" || ev.episode.HumanAction == "MANUAL_REPLACEMENT":
			if d := w.deltaOf(ev.episode); d != nil {
				if byVersion, _ := w.citedBy(d.ID, k.tag, k.evaluator); !byVersion {
					fb.FailUnresolved++
				}
			}
		}
	}
	if fb.FailVerdicts == 0 && fb.UnchangedApprovals == 0 {
		return na[FalseBlock](k.tag + " has no pass/fail verdict on an unchanged approval and never failed a decided episode's reviewed draft")
	}
	if fb.UnchangedApprovals > 0 {
		r := float64(fb.FalseBlock) / float64(fb.UnchangedApprovals)
		fb.Rate = &r
	}
	if fb.FailVerdicts > 0 {
		p := float64(fb.FalseBlock) / float64(fb.FailVerdicts)
		fb.FailPrecision = &p
	}
	return avail(fb)
}
