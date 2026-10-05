package controlplane

import (
	"fmt"
	"slices"
	"strings"
)

// The spans of the human side of the chain: the Cliff messages, what the human did, the recomputed action and what the
// episode taught the company.

// cliffKinds are the three Cliff messages: Message 1 (bi), Message 2 (chooser) and Message 3 (judgment).
var cliffKinds = []string{"bi", "chooser", "judgment"}

// decided reports that the human's send decision is in.
func (in *traceInput) decided() bool {
	return in.decision != nil && in.decision.sendDecision != "pending"
}

func (in *traceInput) cliffSpans() []TraceSpan {
	out := make([]TraceSpan, 0, len(cliffKinds))
	for i, kind := range cliffKinds {
		var rows []surfaceRow
		for _, r := range in.surfaces {
			if r.kind == kind {
				rows = append(rows, r)
			}
		}
		out = append(out, in.cliffSpan(kind, i+1, rows))
	}
	return out
}

func (in *traceInput) cliffSpan(kind string, number int, rows []surfaceRow) TraceSpan {
	title := "Cliff message: " + kind
	label := fmt.Sprintf("Message %d (%s)", number, kind)
	if len(rows) == 0 {
		if kind == "judgment" && !in.decided() {
			s := span(kindCliff, kind, title, statusPending, label+" is posted after the human decides")
			s.Attributes["message_kind"] = kind
			return s
		}
		s := span(kindCliff, kind, title, statusNotRecorded, label+" has no reservation or post recorded")
		s.Attributes["message_kind"] = kind
		return s
	}
	surfaces := []map[string]any{}
	posted := false
	var names []string
	for _, r := range rows {
		isPosted := r.ts != nil
		posted = posted || isPosted
		names = append(names, r.surface)
		surfaces = append(surfaces, map[string]any{"surface": r.surface, "channel": r.channel, "ts": r.ts, "posted": isPosted, "reserved_at": r.reservedAt})
	}
	state := "reserved, not yet confirmed posted"
	if posted {
		state = "posted"
	}
	s := span(kindCliff, kind, title, statusRecorded, fmt.Sprintf("%s %s on %s", label, state, strings.Join(names, ", ")))
	s.OccurredAt = at(rows[0].reservedAt)
	for _, r := range rows {
		s.Refs = append(s.Refs, SpanRef{"surface_message", r.subjectID + "/" + r.surface + "/" + r.kind})
	}
	s.Attributes["message_kind"] = kind
	s.Attributes["posted"] = posted
	s.Attributes["surfaces"] = surfaces
	return s
}

func (in *traceInput) humanSpan() TraceSpan {
	d := in.decision
	if d == nil {
		return span(kindHuman, "0", "Human interaction", statusPending, "No human choice has been recorded yet")
	}
	chosen := in.cands.byID[d.selectedID]
	title := "the chosen candidate"
	if chosen != nil {
		title = chosen.ref.Title
	}
	summary := fmt.Sprintf("%s chose %s", d.actor, title)
	if d.selectedID != d.preferredID {
		if pick := in.cands.byID[d.preferredID]; pick != nil {
			summary += " over Ghost's pick, " + pick.ref.Title
		}
	}
	if d.edited {
		summary += ", edited it"
	}
	switch d.sendDecision {
	case "send":
		summary += " and sent it"
	case "discard":
		summary += " and discarded it"
	default:
		summary += "; the send decision is pending"
	}
	s := span(kindHuman, d.id, "Human interaction", statusRecorded, summary)
	s.OccurredAt = at(d.chosenAt)
	if d.sendDecidedAt != nil {
		s.OccurredAt = at(*d.sendDecidedAt)
	}
	s.Refs = in.humanRefs()
	for _, id := range in.reactions {
		s.Refs = append(s.Refs, SpanRef{"customer_reaction", id})
	}
	agreement := "overrode"
	if d.selectedID == d.preferredID {
		agreement = "agreed"
	}
	s.Attributes["agreement"] = agreement
	s.Attributes["edited"] = d.edited
	s.Attributes["send_decision"] = d.sendDecision
	s.Attributes["human_action"] = in.episode.HumanAction
	s.Attributes["customer_reaction_count"] = len(in.reactions)
	if in.inference != nil {
		s.Attributes["judgment_verdict"] = in.inference.verdict
	}
	return s
}

// humanRefs are the rows of the human's decision: the strategy decision, the human decision, the judgment inference
// and the human delta, whichever exist.
func (in *traceInput) humanRefs() []SpanRef {
	refs := []SpanRef{{"human_strategy_decision", in.decision.id}}
	humanDecision := in.episode.HumanDecisionID
	if humanDecision == nil {
		humanDecision = in.decision.humanDecisionID
	}
	if humanDecision != nil {
		refs = append(refs, SpanRef{"human_decision", *humanDecision})
	}
	if in.inference != nil {
		refs = append(refs, SpanRef{"judgment_inference", in.inference.id})
	}
	if in.episode.HumanDeltaID != nil {
		refs = append(refs, SpanRef{"human_delta", *in.episode.HumanDeltaID})
	}
	return refs
}

// hasSendTimeResults: the run has results written by the send-time re-evaluation. A re-evaluation that judged an
// unchanged artifact writes nothing new (its results are the generation-time rows), so the absence of such results is
// not a claim that no check ran, only that the trace has nothing to link.
func (in *traceInput) hasSendTimeResults() bool {
	for _, r := range in.results {
		if r.SendTime {
			return true
		}
	}
	return false
}

func (in *traceInput) recomputedSpan() TraceSpan {
	d := in.decision
	switch {
	case d == nil:
		return span(kindRecomputed, "0", "Recomputed action", statusPending, "No human choice yet, so no action was recomputed")
	case d.sendDecision == "pending":
		return span(kindRecomputed, d.id, "Recomputed action", statusPending, "The chosen draft is re-evaluated when the human sends it")
	}
	final, edited := "none", d.edited
	summary := "The human discarded the draft: no action will be taken"
	if d.sendDecision == "send" {
		final = "the chosen candidate's action"
		if c := in.cands.byID[d.selectedID]; c != nil {
			final = c.ref.ActionType
		}
		state := "unchanged"
		if edited {
			state = "edited"
		}
		summary = fmt.Sprintf("Final action: %s (%s)", final, state)
		if in.hasSendTimeResults() {
			summary += ", re-evaluated before it was sent"
		}
	}
	s := span(kindRecomputed, d.id, "Recomputed action", statusRecorded, summary)
	s.OccurredAt = at(d.chosenAt)
	if d.sendDecidedAt != nil {
		s.OccurredAt = at(*d.sendDecidedAt)
	}
	s.Refs = in.humanRefs()[:1]
	if in.episode.HumanDeltaID != nil {
		s.Refs = append(s.Refs, SpanRef{"human_delta", *in.episode.HumanDeltaID})
	}
	s.Attributes["final_action"] = final
	s.Attributes["edited"] = edited
	s.Attributes["send_decision"] = d.sendDecision
	s.Attributes["human_action"] = in.episode.HumanAction
	return s
}

func (in *traceInput) mutationSpan() TraceSpan {
	if len(in.mutations) == 0 {
		if in.decided() {
			return span(kindKnowledgeMut, "0", "Knowledge mutation", statusNotRecorded, "This episode changed no company knowledge")
		}
		return span(kindKnowledgeMut, "0", "Knowledge mutation", statusPending, "Knowledge changes are recorded after the human decides")
	}
	ops, mutationIDs, knowledgeIDs := []string{}, []string{}, []string{}
	var parts []string
	for _, m := range in.mutations {
		ops, mutationIDs = append(ops, m.Operation), append(mutationIDs, m.ID)
		if !slices.Contains(knowledgeIDs, m.KnowledgeID) {
			knowledgeIDs = append(knowledgeIDs, m.KnowledgeID)
		}
		name := m.KnowledgeID
		if m.KnowledgeKey != nil {
			name = *m.KnowledgeKey
		}
		parts = append(parts, m.Operation+" on "+name)
	}
	s := span(kindKnowledgeMut, knowledgeIDs[0], "Knowledge mutation", statusRecorded,
		countWord(len(in.mutations), "knowledge change", "knowledge changes")+": "+strings.Join(parts, ", "))
	s.OccurredAt = at(in.mutations[len(in.mutations)-1].OccurredAt)
	for _, id := range knowledgeIDs {
		s.Refs = append(s.Refs, SpanRef{"knowledge", id})
	}
	s.Attributes["operations"] = ops
	s.Attributes["mutation_ids"] = mutationIDs
	return s
}
