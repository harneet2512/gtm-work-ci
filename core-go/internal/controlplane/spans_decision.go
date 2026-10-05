package controlplane

import (
	"encoding/json"
	"fmt"
	"slices"
)

// The spans of the decision side of the chain: what knowledge was in play, the candidates and how they were ranked.

// knowledgeSpan builds one of the three knowledge spans. Retrieval is not influence, so retrieved, applicable and used
// are three spans over the E7 record the orchestrator keeps on the build_context step; a run without that record has
// all three not_recorded.
func (in *traceInput) knowledgeSpan(kind string) TraceSpan {
	titles := map[string]string{kindRetrieved: "Knowledge retrieved", kindApplicable: "Knowledge applicable", kindUsed: "Knowledge cited"}
	a := in.attr
	if a == nil {
		s := span(kind, "0", titles[kind], statusNotRecorded, "No knowledge attribution is recorded for this run")
		s.Attributes["reason"] = "no_attribution_recorded"
		return s
	}
	var ids []string
	var summary string
	switch kind {
	case kindRetrieved:
		ids, summary = a.Retrieved, countWord(len(a.Retrieved), "knowledge object", "knowledge objects")+" retrieved at the replay clock"
	case kindApplicable:
		ids = a.Applicable
		summary = fmt.Sprintf("%d of %d retrieved knowledge objects applied; %d blocked by an exception", len(a.Applicable), len(a.Retrieved), len(a.ExceptionBlocked))
	default:
		ids, summary = a.Used, countWord(len(a.Used), "knowledge object", "knowledge objects")+" cited by a candidate"
	}
	s := span(kind, "0", titles[kind], statusRecorded, summary)
	s.OccurredAt = a.AsOf
	s.Attributes["knowledge_ids"] = nonNilIDs(ids)
	for _, id := range ids {
		s.Refs = append(s.Refs, SpanRef{"knowledge", id})
	}
	switch kind {
	case kindUsed:
		// The data is a citation (a candidate named the knowledge); whether it changed the decision is not measured.
		s.Attributes["influence_measured"] = false
	case kindApplicable:
		s.Attributes["exception_blocked_ids"] = nonNilIDs(a.ExceptionBlocked)
		s.EvidenceRefs = in.applicableEvidence()
		if in.episode.GuidanceID != nil {
			s.Refs = append(s.Refs, SpanRef{"decision_guidance", *in.episode.GuidanceID})
		}
	}
	return s
}

func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return slices.Clone(ids)
}

// applicableEvidence is the current evidence behind the knowledge that applies, from the DecisionGuidance.
func (in *traceInput) applicableEvidence() json.RawMessage {
	var refs []json.RawMessage
	for _, g := range in.guidance {
		if g.Applies {
			refs = append(refs, g.Evidence...)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	if len(refs) > maxEvidenceRefs {
		refs = refs[:maxEvidenceRefs]
	}
	raw, _ := json.Marshal(refs)
	return raw
}

// maxEvidenceRefs bounds the evidence references one span carries.
const maxEvidenceRefs = 50

func (in *traceInput) candidatesSpan() TraceSpan {
	c := in.cands
	if c.setID == nil {
		return span(kindCandidates, "0", "Candidates", statusNotRecorded, "No strategy set is recorded for this episode")
	}
	s := span(kindCandidates, *c.setID, "Candidates", statusRecorded, countWord(len(c.byRank), "candidate", "candidates")+" in the strategy set")
	s.OccurredAt = in.setAt
	s.Refs = []SpanRef{{"strategy_set", *c.setID}}
	ids, types := []string{}, []string{}
	for _, cand := range c.byRank {
		s.Refs = append(s.Refs, SpanRef{"strategy_candidate", cand.ref.CandidateID}, SpanRef{"eval_bundle", cand.bundleID})
		ids, types = append(ids, cand.ref.CandidateID), append(types, cand.ref.StrategyType)
	}
	s.Attributes["candidate_ids"] = ids
	s.Attributes["strategy_types"] = types
	return s
}

func (in *traceInput) rankingSpan() TraceSpan {
	c := in.cands
	if c.setID == nil || len(c.byRank) == 0 {
		return span(kindRanking, "0", "Ranking", statusNotRecorded, "No strategy set is recorded for this episode, so nothing was ranked")
	}
	pick := c.byRank[0].ref
	s := span(kindRanking, *c.setID, "Ranking", statusRecorded, "Ghost preferred "+pick.Title)
	s.OccurredAt = in.setAt
	s.Refs = []SpanRef{{"strategy_set", *c.setID}}
	order := []string{}
	for _, cand := range c.byRank {
		order = append(order, cand.ref.CandidateID)
	}
	s.Attributes["order"] = order
	s.Attributes["preferred_candidate_id"] = pick.CandidateID
	return s
}
