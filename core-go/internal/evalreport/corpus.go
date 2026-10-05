package evalreport

import (
	"encoding/json"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
)

// consistency measures same-input determinism two ways: (a) repeated stored judgments on the SAME
// artifact — eval_runs groups sharing (agent_run, draft_index, tag, kind) only repeat when the send
// re-evaluated the draft (result ids are content-hashed on the whole input: identical input reuses
// the id, so a second row means some input field drifted). On an APPROVE_UNCHANGED episode the
// artifact is byte-identical — a verdict change there is a real instability; on an edited episode a
// change is the expected detection of the edit, so only unchanged re-judgments are scored; and
// (b) spec replay — for 'human_delta' results of a version with an executable shadow_spec, the spec
// is re-run on the stored draft (candidate row, else the final sent draft) and compared.
func (w *world) consistency(k versionKey, v *versionRow) Metric[Consistency] {
	type grp struct {
		run, kind string
		draft     int
	}
	groups := map[grp][]*evalRow{}
	for _, r := range w.allEvals {
		if r.Tag != k.tag {
			continue
		}
		g := grp{r.RunID, r.Kind, r.DraftIndex}
		groups[g] = append(groups[g], r)
	}
	var skipped int
	c := Consistency{Basis: "stored eval_runs grouped by (agent_run, draft_index, tag, kind): only " +
		"APPROVE_UNCHANGED re-judgments score (the artifact is provably identical); " +
		"spec_replay re-runs executable shadow_specs on the stored draft and compares verdicts"}
	for _, rows := range groups {
		if len(rows) < 2 {
			continue
		}
		ep := w.byRun[rows[0].RunID]
		if ep == nil || ep.HumanAction != "APPROVE_UNCHANGED" {
			skipped++ // the artifact was edited (or never sent): a verdict change is detection, not instability
			continue
		}
		c.RepeatedGroups++
		same := true
		for _, r := range rows[1:] {
			if r.Verdict != rows[0].Verdict {
				same = false
			}
		}
		if same {
			c.Agreeing++
		}
	}
	c.SkippedInputChanged = skipped
	if rp := w.specReplay(k, v); rp.Replayable > 0 || rp.Excluded > 0 {
		c.Replay = &rp
	}
	if v != nil && v.SpecErr != "" {
		c.ReplayNote = "spec replay n/a: the version's shadow_spec is malformed (" + v.SpecErr + ")"
	}
	if c.RepeatedGroups == 0 && c.Replay == nil {
		why := "no same-artifact re-judgment of " + k.tag +
			" exists (every stored judgment is unique, or every repeat followed an edit) and no replayable " +
			"shadow-spec results exist — same-input consistency is unobserved"
		if v != nil && v.SpecErr != "" {
			why += "; spec replay is n/a because the version's shadow_spec is malformed (" + v.SpecErr + ")"
		}
		return na[Consistency](why)
	}
	if c.RepeatedGroups > 0 {
		r := float64(c.Agreeing) / float64(c.RepeatedGroups)
		c.Rate = &r
	}
	return avail(c)
}

// specReplay re-runs the version's executable shadow spec on each stored human_delta result's judged
// draft and compares verdicts — the same Spec.RunSpec + violated->fail mapping axisResult applies.
func (w *world) specReplay(k versionKey, v *versionRow) Replay {
	var rp Replay
	if v == nil || !v.Spec.Executable() {
		return rp
	}
	for _, r := range w.allEvals {
		if r.Tag != k.tag || r.Kind != "human_delta" {
			continue
		}
		draft, ok := w.judgedDraft(r)
		if !ok {
			continue
		}
		violated, _ := v.Spec.RunSpec(draft)
		want := "pass"
		if violated {
			want = "fail"
		}
		if r.Verdict == "fail" && want == "pass" && w.judgedSupersededDraft(r) {
			rp.Excluded++
			rp.ExcludedRows = append(rp.ExcludedRows, ExcludedReplay{RowID: r.ID, Reason: supersededReason})
			continue
		}
		rp.Replayable++
		if r.Verdict == want {
			rp.Matching++
		}
	}
	if rp.Replayable > 0 {
		rate := float64(rp.Matching) / float64(rp.Replayable)
		rp.Rate = &rate
	}
	return rp
}

const supersededReason = "stored fail, replay passes on an edited episode: the row judged a draft the " +
	"human's edit later replaced (a refused send, repaired and re-sent) and the stored final no longer holds it"

// judgedSupersededDraft reports whether a human_delta row's replay draft is the human's stored final of
// an EDITED episode — the draft a refused send's fail row can no longer be rebuilt against.
func (w *world) judgedSupersededDraft(r *evalRow) bool {
	ep := w.anyByRun[r.RunID]
	if ep == nil || (ep.HumanAction != "APPROVE_WITH_EDIT" && ep.HumanAction != "MANUAL_REPLACEMENT") {
		return false
	}
	h, ok := w.decisions[ep.ID]
	return ok && len(h.Final.Artifact) > 0 && h.SelectedDraft == r.DraftIndex
}

// judgedDraft reconstructs the draft an eval row judged. kind 'human_delta' rows only ever judge the
// artifact about to be sent — the stored final draft of the episode's decision (also the artifact a
// refused send's persisted rows judged, since the pending decision keeps final_*): prefer it whenever
// the row's (run, draft_index) is the episode's selected draft. Everything else is a generation-time
// judgment of the strategy_candidates payload.
func (w *world) judgedDraft(r *evalRow) (deterministic.Output, bool) {
	if ep := w.anyByRun[r.RunID]; ep != nil {
		if h, ok := w.decisions[ep.ID]; ok && len(h.Final.Artifact) > 0 && h.SelectedDraft == r.DraftIndex {
			return decodeDraft(h.Final.ActionType, h.Final.To, h.Final.CC, h.Final.Artifact)
		}
	}
	if cand, ok := w.candidates[r.RunID][r.DraftIndex]; ok {
		return decodeDraft(cand.ActionType, cand.To, cand.CC, cand.Artifact)
	}
	return deterministic.Output{}, false
}

// decodeDraft parses the stored recipient + artifact payloads into the deterministic draft shape the
// spec checks read (person/role objects plus channel/subject/body/attachments).
func decodeDraft(actionType string, to, cc, artifact []byte) (deterministic.Output, bool) {
	var out deterministic.Output
	if len(artifact) == 0 {
		return out, false
	}
	if err := json.Unmarshal(artifact, &out.FinishedArtifact); err != nil {
		return deterministic.Output{}, false
	}
	var toList, ccList []deterministic.Recipient
	if err := json.Unmarshal(to, &toList); err != nil {
		return deterministic.Output{}, false
	}
	if err := json.Unmarshal(cc, &ccList); err != nil {
		return deterministic.Output{}, false
	}
	out.ProposedActionType = actionType
	out.Recipients = append(toList, ccList...)
	return out, true
}

const coverageBasis = "footprint of the human_deltas.semantic_labels vocabulary and suggested_eval_type " +
	"axes in the stored deltas: a label is covered when an existing eval explained it or a candidate " +
	"version was seeded from it"

// coverageLimits are the blind spots the metric inherits from HAR-139's delta capture.
var coverageLimits = []string{
	"unobserved_labels means unobserved in the stored deltas, not impossible",
	"edit capture sees recipients and the artifact only: timing, CTA and CRM-only edits produce no delta and are invisible here",
	"semantic labels come from the worker labeler; fallback deltas are unlabeled and count as no label",
	"a send the human never edited writes no delta, so unedited approvals contribute no coverage",
}

// coverage reports the semantic-label vocabulary's footprint plus every suggested_eval_type axis.
// With no human_deltas the categories are simply unobserved — the metric is n/a like every other
// input-less metric rather than an all-zero table.
func (w *world) coverage(k versionKey, v *versionRow) Metric[Coverage] {
	if len(w.deltas) == 0 {
		return na[Coverage]("no human_deltas recorded — no correction categories observed")
	}
	c := Coverage{}
	type labelStat struct {
		deltas, unexplained, seeded int
	}
	stats := map[string]*labelStat{}
	axes := map[string]*AxisCoverage{}
	for _, d := range w.deltas {
		seeded := d.SeededTag != ""
		for _, l := range d.Labels {
			s := stats[l]
			if s == nil {
				s = &labelStat{}
				stats[l] = s
			}
			s.deltas++
			if d.Unexplained {
				s.unexplained++
			}
			if seeded {
				s.seeded++
			}
		}
		if d.Suggested != "" {
			a := axes[d.Suggested]
			if a == nil {
				a = &AxisCoverage{Axis: d.Suggested}
				axes[d.Suggested] = a
			}
			a.Deltas++
			if seeded {
				a.Seeded++
			}
		}
	}
	seen := map[string]bool{}
	for _, l := range semanticLabelVocab {
		s := stats[l]
		if s == nil {
			c.Unobserved = append(c.Unobserved, l)
			c.Labels = append(c.Labels, LabelCoverage{Label: l})
			continue
		}
		seen[l] = true
		c.Labels = append(c.Labels, LabelCoverage{
			Label: l, Deltas: s.deltas, Unexplained: s.unexplained, Seeded: s.seeded,
			Covered: s.deltas-s.unexplained > 0 || s.seeded > 0})
	}
	var extra []string
	for l := range stats {
		if !seen[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(extra)
	for _, l := range extra {
		s := stats[l]
		c.Labels = append(c.Labels, LabelCoverage{
			Label: l, Deltas: s.deltas, Unexplained: s.unexplained, Seeded: s.seeded,
			Covered: s.deltas-s.unexplained > 0 || s.seeded > 0})
	}
	for _, a := range axes {
		c.SuggestedAxes = append(c.SuggestedAxes, *a)
	}
	sort.Slice(c.SuggestedAxes, func(i, j int) bool { return c.SuggestedAxes[i].Axis < c.SuggestedAxes[j].Axis })

	for _, d := range w.deltas {
		if attributedAxes(d)[k.evaluator] {
			c.ThisVersion.AxisDeltas++
		}
	}
	c.ThisVersion.SeededByThis = v != nil && seededVersion(v)
	c.Basis = coverageBasis
	c.Limits = coverageLimits
	return avail(c)
}

// explained is the share of human edits whose human_deltas.unexplained is false — an existing eval
// predicted them — with this version's and axis's slice of the explanations.
func (w *world) explained(k versionKey) Metric[Explained] {
	if len(w.deltas) == 0 {
		return na[Explained]("no human_deltas recorded")
	}
	e := Explained{Deltas: len(w.deltas)}
	for _, d := range w.deltas {
		if !d.Unexplained {
			e.ExplainedDeltas++
		}
		byVersion, byAxis := w.citedBy(d.ID, k.tag, k.evaluator)
		if byVersion {
			e.ByThisVersion++
		}
		if byAxis {
			e.ByThisAxis++
		}
		if attributedAxes(d)[k.evaluator] {
			e.AxisAttributed++
		}
	}
	r := float64(e.ExplainedDeltas) / float64(e.Deltas)
	e.Rate = &r
	return avail(e)
}

// discovery is the candidate-criterion discovery rate: evaluator_versions seeded by unexplained
// deltas (created_from='human_delta') or by corrected judgment verdicts (the SeedVerdictCriterion
// shape: evaluator 'human_delta', created_from 'manual', a linked knowledge row) — per decided
// episode and per unexplained delta.
func (w *world) discovery(v *versionRow) Metric[Discovery] {
	if len(w.episodes) == 0 && len(w.deltas) == 0 {
		return na[Discovery]("no decided episodes or human_deltas to measure discovery against")
	}
	d := Discovery{Episodes: len(w.episodes)}
	seededSources := map[string]bool{}
	for _, ver := range w.versions {
		if seededVersion(ver) {
			d.Seeded++
			if src := w.deltas[ver.SourceDeltaID]; src != nil && src.Unexplained {
				seededSources[ver.SourceDeltaID] = true // yield counts only unexplained deltas that seeded something
			}
		}
	}
	for _, delta := range w.deltas {
		if delta.Unexplained {
			d.Unexplained++
		}
	}
	if d.Unexplained > 0 {
		y := float64(len(seededSources)) / float64(d.Unexplained)
		d.Yield = &y
	}
	if d.Episodes > 0 {
		p := float64(d.Seeded) / float64(d.Episodes)
		d.PerEpisodes = &p
	}
	d.ThisSeeded = v != nil && seededVersion(v)
	return avail(d)
}
