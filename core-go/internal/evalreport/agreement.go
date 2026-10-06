package evalreport

// agreement.go — the human-agreement categories: how one version's stored verdicts on a decided
// episode's final draft compare with the human's action.

const (
	catTruePass          = "true_pass"
	catTrueBlockReject   = "true_block_reject"
	catTrueBlockRepaired = "true_block_repaired"
	catPassOtherAxis     = "pass_other_axis" // pass on an edit another axis caused: unscored (P3)
	catPassRejected      = "pass_rejected"
	catFalsePass         = "false_pass"
	catFalseBlock        = "false_block"
	catFailUnresolved    = "fail_unresolved_edit"
	catAbstain           = "abstain"  // the version abstained (E19.5): its own bucket, counted in abstain_rate
	catWarn              = "warn"     // the version only warned: its own bucket, never a pass or a fail
	catUnscored          = "unscored" // IGNOREd episodes, no linked delta/decision, verdictless rows
)

// categorize places one episode into an agreement bucket:
//
//   - IGNORE, an episode with no linked human decision, and an edit with no linked delta are
//     unscored (no supervision outcome to agree with).
//   - A version with neither pass nor fail rows is abstain (any abstain row) or warn — separate
//     buckets so abstention is measured (E19.5) instead of silently dropping out of the rates.
//   - APPROVE_UNCHANGED: any fail is a false block (the human approved the artifact as-is); otherwise
//     a pass is a true pass.
//   - REJECT: any fail is a true block (flag and rejection agree); a pass-only judgment passed what
//     the human rejected — pass_rejected, a contradiction.
//   - Edited (APPROVE_WITH_EDIT, MANUAL_REPLACEMENT): a delta-cited fail is a true block repaired by
//     the edit; an uncited fail is a flag the edit neither repaired nor addressed (fail_unresolved_edit
//     — unscored rather than a contradiction); no fail at all means the version never caught the
//     correction — a false pass when the edit is attributed to its axis, else pass_other_axis, which
//     is unscored: a pass on an edit this axis was never responsible for confirms nothing.
func (w *world) categorize(ev *episodeVerdict, evaluator, tag string) string {
	ep := ev.episode
	if ep.HumanAction == "IGNORE" || !ep.HasDecision {
		return catUnscored
	}
	d := w.deltaOf(ep)
	edited := ep.HumanAction == "APPROVE_WITH_EDIT" || ep.HumanAction == "MANUAL_REPLACEMENT"
	failed := hasFail(ev.rows)
	passed := hasPass(ev.rows)
	if edited && d != nil {
		if byVersion, _ := w.citedBy(d.ID, tag, evaluator); byVersion {
			return catTrueBlockRepaired
		}
	}
	if !failed && !passed {
		return nonDecisive(ev.rows)
	}
	switch {
	case ep.HumanAction == "APPROVE_UNCHANGED":
		if failed {
			return catFalseBlock
		}
		return catTruePass
	case ep.HumanAction == "REJECT":
		if failed {
			return catTrueBlockReject
		}
		return catPassRejected
	case edited && d != nil:
		if failed {
			return catFailUnresolved
		}
		if attributedAxes(d)[evaluator] {
			return catFalsePass
		}
		return catPassOtherAxis
	}
	return catUnscored
}

// nonDecisive buckets a verdict group with no pass and no fail: abstain beats warn; no rows at all is
// unscored.
func nonDecisive(rows []*evalRow) string {
	warned := false
	for _, r := range rows {
		switch r.Verdict {
		case "abstain", "unknown": // unknown is the current word, abstain the older spelling
			return catAbstain
		case "warn":
			warned = true
		}
	}
	if warned {
		return catWarn
	}
	return catUnscored
}

func (w *world) agreement(k versionKey, byEpisode map[string][]*evalRow) Metric[Agreement] {
	vs := w.attachEpisodes(byEpisode)
	if len(vs) == 0 {
		return na[Agreement]("no eval_runs tagged " + k.tag + " on a decided episode's final draft")
	}
	a := Agreement{Categories: map[string]int{}}
	for _, ev := range vs {
		cat := w.categorize(ev, k.evaluator, k.tag)
		a.Categories[cat]++
		a.EpisodesEvaluated++
		switch cat {
		case catTruePass, catTrueBlockReject, catTrueBlockRepaired:
			a.Consistent++
		case catFalsePass, catFalseBlock, catPassRejected:
			a.Contradicted++
		case catAbstain:
			a.Abstained++
		case catWarn:
			a.Warned++
		default: // catUnscored, catFailUnresolved, catPassOtherAxis — no provable agreement either way
			a.Unscored++
		}
	}
	if n := a.Consistent + a.Contradicted; n > 0 {
		r := float64(a.Consistent) / float64(n)
		a.Rate = &r
	}
	if n := a.Consistent + a.Contradicted + a.Abstained; n > 0 {
		r := float64(a.Consistent) / float64(n)
		a.WorstCaseRate = &r
	}
	ab := float64(a.Abstained) / float64(a.EpisodesEvaluated)
	a.AbstainRate = &ab
	a.AcceptUnchanged = a.Categories[catTruePass]
	ar := float64(a.AcceptUnchanged) / float64(a.EpisodesEvaluated)
	a.AcceptRate = &ar
	return avail(a)
}
