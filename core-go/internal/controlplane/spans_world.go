package controlplane

import (
	"encoding/json"
	"fmt"
)

// The spans of the world side of the chain: what arrived, what it was attached to and what it changed.

func (in *traceInput) sourceSpan() TraceSpan {
	if len(in.triggers) == 0 {
		s := span(kindSource, "0", "Source event", statusNotRecorded, "The trigger activities of this run are not readable")
		return s
	}
	first := in.triggers[0]
	s := span(kindSource, first.id, "Source event", statusRecorded, fmt.Sprintf("%s %s at %s", first.sourceSystem, first.activityType, first.occurredAt.Format("2006-01-02T15:04:05Z")))
	if first.summary != nil && *first.summary != "" {
		s.Summary += ": " + *first.summary
	}
	s.OccurredAt = at(first.occurredAt)
	ids := []string{}
	for _, t := range in.triggers {
		s.Refs = append(s.Refs, SpanRef{"activity", t.id}, SpanRef{"source_event", t.sourceEventID})
		if len(ids) < maxListed {
			ids = append(ids, t.id)
		}
	}
	s.Attributes["trigger_activity_count"] = len(in.triggers)
	s.Attributes["activity_ids"] = ids
	return s
}

func (in *traceInput) evidenceSpan() TraceSpan {
	c := in.change
	if c == nil {
		return span(kindEvidence, "0", "Evidence", statusNotRecorded, "No account change is recorded for this episode, so no evidence references are listed")
	}
	var refs []json.RawMessage
	if err := json.Unmarshal(c.evidenceRefs, &refs); err != nil {
		// An unreadable list is not an empty one: say so rather than claim "0 evidence references".
		s := span(kindEvidence, c.id, "Evidence", statusNotRecorded, "The evidence references behind the account change could not be read")
		s.OccurredAt = at(c.createdAt)
		s.Refs = []SpanRef{{"account_change", c.id}}
		s.Attributes["reason"] = "unreadable"
		return s
	}
	s := span(kindEvidence, c.id, "Evidence", statusRecorded, countWord(len(refs), "evidence reference", "evidence references")+" behind the account change")
	s.OccurredAt = at(c.createdAt)
	s.Refs = []SpanRef{{"account_change", c.id}}
	s.EvidenceRefs = c.evidenceRefs
	s.Attributes["material_change"] = c.material
	return s
}

func (in *traceInput) resolutionSpan() TraceSpan {
	if len(in.triggers) == 0 {
		return span(kindResolution, "0", "Resolution", statusNotRecorded, "The trigger activities of this run are not readable")
	}
	t := in.triggers[0]
	summary := fmt.Sprintf("Attached to %s; %d of %d participants resolved to people", in.episode.AccountName, t.resolved, t.participants)
	if t.accountID == nil {
		summary = "The trigger activity is not attached to an account"
	}
	s := span(kindResolution, t.id, "Resolution", statusRecorded, summary)
	s.OccurredAt = at(t.occurredAt)
	s.Refs = []SpanRef{{"activity", t.id}}
	s.Attributes["participants_total"] = t.participants
	s.Attributes["participants_resolved"] = t.resolved
	s.Attributes["identity_mappings"] = t.mappings
	if t.accountHint != nil {
		s.Attributes["account_hint"] = *t.accountHint
	}
	return s
}

func (in *traceInput) graphSpan() TraceSpan {
	g := in.graph
	if g == nil {
		return span(kindGraph, "0", "Graph mutation", statusNotRecorded, "No change to the account graph is recorded for the trigger event")
	}
	s := span(kindGraph, g.id, "Graph mutation", statusRecorded, fmt.Sprintf("The account graph changed by %s for this event", countWord(g.changes, "update", "updates")))
	s.OccurredAt = at(g.createdAt)
	s.Refs = []SpanRef{{"graph_diff", g.id}}
	s.Attributes["change_count"] = g.changes
	return s
}

func (in *traceInput) stateSpan() TraceSpan {
	d := in.diff
	if d == nil {
		s := span(kindState, fmt.Sprintf("%d", in.episode.StateVersion), "State", statusRecorded, fmt.Sprintf("The episode was decided on state v%d (no change summary is linked)", in.episode.StateVersion))
		s.Attributes["state_version"] = in.episode.StateVersion
		return s
	}
	material := "not material"
	if d.material {
		material = "material"
	}
	s := span(kindState, fmt.Sprintf("%d", d.to), "State", statusRecorded, fmt.Sprintf("State v%d to v%d: %s changed (%s)", d.from, d.to, countWord(d.changes, "field", "fields"), material))
	s.OccurredAt = at(d.createdAt)
	s.Refs = []SpanRef{{"state_diff", d.id}}
	s.Attributes["from_version"] = d.from
	s.Attributes["to_version"] = d.to
	s.Attributes["is_material"] = d.material
	s.Attributes["changed_fields"] = d.fields
	s.Attributes["state_version"] = in.episode.StateVersion
	return s
}

// precedentsSpan is always not_recorded: no precedent retrieval is persisted (E5 is planned in the registry), and the
// trace says so instead of inventing precedents.
func (in *traceInput) precedentsSpan() TraceSpan {
	s := span(kindPrecedents, "0", "Precedents", statusNotRecorded, "Precedent retrieval is not recorded yet")
	s.Attributes["reason"] = "not_persisted"
	return s
}
