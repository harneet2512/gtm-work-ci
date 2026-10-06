package recompute

import "fmt"

var editSentence = map[string]string{
	"recipient_added":        "The human added a recipient to the draft.",
	"recipient_removed":      "The human removed a recipient from the draft.",
	"recipient_role_changed": "The human moved a recipient between to and cc.",
	"subject_changed":        "The human changed the subject.",
	"channel_changed":        "The human changed the channel.",
	"paragraph_added":        "The human added a paragraph to the body.",
	"paragraph_removed":      "The human removed a paragraph from the body.",
	"paragraph_edited":       "The human rewrote a paragraph of the body.",
	"attachment_added":       "The human added an attachment.",
	"attachment_removed":     "The human removed an attachment.",
}

const (
	reasonNotRanked     = "The strategy set is not re-ranked after a send."
	reasonKnowledge     = "Knowledge use is not re-judged at send time."
	reasonSemanticStale = "Semantic evals are not re-run at send time; the old verdict is stale and no new one exists."
	reasonNoRerun       = "The send-time evaluation produced no result for this eval; the old verdict is stale and no new one exists."
	reasonPending       = "The send has not happened: nothing has been recomputed yet."
	reasonUnlinked      = "The send-time evaluation batch of this decision is not linked (it was sent before the link was recorded), so what was recomputed cannot be shown."
)

// entryFor derives what one edit invalidated, recomputed, left unrecomputed and preserved.
func entryFor(f facts, status Status, idx int, edit Edit, state []SpanRef, undeclared map[string]string) Entry {
	e := Entry{Index: idx, Edit: edit, Invalidated: []SpanRef{}, Recomputed: []SpanRef{}, NotRecomputed: []SpanRef{}, Preserved: append([]SpanRef{}, state...)}
	field := fieldPtr(edit.Field)
	e.Invalidated = append(e.Invalidated, ref(ArtifactField, f.CandidateID, field, fieldLabel[edit.Field], editSentence[edit.Kind]))

	var stale []judged
	for _, o := range f.Old {
		switch {
		case dependsOn(o.EvalType, edit.Field):
			reason := fmt.Sprintf("%s reads the %s, which this edit changed: its verdict on the old draft no longer applies.", o.EvalType, edit.Field)
			e.Invalidated, stale = append(e.Invalidated, evalRef(o, field, reason, nil)), append(stale, o)
		case undeclared[o.ID] != "":
			e.Invalidated, stale = append(e.Invalidated, evalRef(o, nil, undeclared[o.ID], nil)), append(stale, o)
		default:
			e.Preserved = append(e.Preserved, evalRef(o, nil, preservedReason(f, status, o, edit.Field), nil))
		}
	}
	ranking := ref(RankingRat, f.CandidateID, nil, fmt.Sprintf("Why this candidate ranked %d", f.Ranking), "")
	if len(stale) > 0 {
		ranking.Reason = "The ranking rested on eval verdicts of the old draft that this edit invalidated."
		e.Invalidated = append(e.Invalidated, ranking)
	} else {
		ranking.Reason = fmt.Sprintf("No eval the ranking rested on depends on the %s.", edit.Field)
		e.Preserved = append(e.Preserved, ranking)
	}
	claims := make([]SpanRef, 0, len(f.KnowledgeIDs))
	for _, k := range f.KnowledgeIDs {
		c := ref(KnowledgeUse, k, field, "Applies company knowledge", "The claim that the draft applies this knowledge was made about the old draft.")
		claims = append(claims, c)
		e.Invalidated = append(e.Invalidated, c)
	}

	if status != Reevaluated {
		reason := reasonPending
		if status == Unavailable {
			reason = reasonUnlinked
		}
		for _, r := range e.Invalidated {
			r.Reason = reason
			e.NotRecomputed = append(e.NotRecomputed, r)
		}
		return e
	}
	return recomputed(e, f, edit, stale, ranking, claims)
}

// recomputed fills the parts of an entry that a send-time re-evaluation really produced: the final artifact, the
// new result of each stale eval that has one, and the new results the edited field feeds. What no row proves was
// re-derived goes to not_recomputed.
func recomputed(e Entry, f facts, edit Edit, stale []judged, ranking SpanRef, claims []SpanRef) Entry {
	field := fieldPtr(edit.Field)
	e.Recomputed = append(e.Recomputed, ref(FinalArtifact, f.DecisionID, nil, "Final artifact as sent", "Re-evaluated at send time at the run's replay clock."))
	paired := map[string]bool{}
	for _, o := range stale {
		if n, ok := newOf(f.New, o.EvalType, o.Kind); ok {
			paired[n.ID] = true
			e.Recomputed = append(e.Recomputed, evalRef(n, field, "Re-run on the final artifact at send time.", str(o.ID)))
			continue
		}
		reason := reasonNoRerun
		if o.Kind == "semantic" {
			reason = reasonSemanticStale
		}
		e.NotRecomputed = append(e.NotRecomputed, evalRef(o, field, reason, nil))
	}
	for _, n := range f.New {
		if paired[n.ID] || !dependsOn(n.EvalType, edit.Field) {
			continue
		}
		if _, hadOld := oldOf(f.Old, n.EvalType, n.Kind); hadOld {
			continue // an old counterpart that this edit does not invalidate keeps its place in preserved
		}
		e.Recomputed = append(e.Recomputed, evalRef(n, field, "Evaluated on the final artifact at send time; the candidate's bundle holds no result for this eval.", nil))
	}
	if hasKind(e.Invalidated, RankingRat) {
		ranking.Reason = reasonNotRanked
		e.NotRecomputed = append(e.NotRecomputed, ranking)
	}
	for _, c := range claims {
		c.Reason = reasonKnowledge
		e.NotRecomputed = append(e.NotRecomputed, c)
	}
	return e
}

func oldOf(old []judged, evalType, kind string) (judged, bool) { return newOf(old, evalType, kind) }

func hasKind(refs []SpanRef, k RefKind) bool {
	for _, r := range refs {
		if r.Kind == k {
			return true
		}
	}
	return false
}

func preservedReason(f facts, status Status, o judged, field Field) string {
	base := fmt.Sprintf("Declared independent of the %s.", field)
	if status != Reevaluated {
		return base
	}
	if n, ok := newOf(f.New, o.EvalType, o.Kind); ok && n.Verdict == o.Verdict {
		return fmt.Sprintf("Declared independent of the %s; its re-run verdict is unchanged (%s).", field, n.Verdict)
	}
	return base
}
