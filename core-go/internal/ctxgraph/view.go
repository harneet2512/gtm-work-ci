package ctxgraph

import (
	"sort"
)

// Sections of a neighborhood, in the order the agent and the web map read them:
// account -> opportunity -> stakeholders -> activities -> claims/commitments -> signals -> decisions -> knowledge.
const (
	SectionAccount       = "account"
	SectionOpportunities = "opportunities"
	SectionStakeholders  = "stakeholders"
	SectionActivities    = "activities"
	SectionClaims        = "claims"
	SectionCommitments   = "commitments"
	SectionSignals       = "signals"
	SectionDecisions     = "decisions"
	SectionKnowledge     = "knowledge"
	SectionEdges         = "edges"
)

// SectionNames lists the node sections in reading order.
var SectionNames = []string{SectionAccount, SectionOpportunities, SectionStakeholders, SectionActivities, SectionClaims,
	SectionCommitments, SectionSignals, SectionDecisions, SectionKnowledge}

// Bounds of a neighborhood.
const (
	DefaultSectionLimit = 10
	MaxSectionLimit     = 20
)

// WithheldVisibility marks a node whose evidence the reader may not see (corectx.WithheldVisibility).
const WithheldVisibility = "visibility"

// EvidenceRef points at the Postgres activity behind a graph element; Postgres serves the exact evidence.
type EvidenceRef struct {
	ActivityID string `json:"activity_id"`
}

// ViewNode is a node as a reader sees it: ids, a short label, status and evidence refs. Claim values and
// quotes are not here: the graph answers what is connected, Postgres proves what was said.
type ViewNode struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Label          string         `json:"label"`
	Status         string         `json:"status,omitempty"`
	ValidFrom      string         `json:"valid_from,omitempty"`
	ValidTo        string         `json:"valid_to,omitempty"`
	Data           map[string]any `json:"data,omitempty"`
	EvidenceRefs   []EvidenceRef  `json:"evidence_refs"`
	SourceEventIDs []string       `json:"source_event_ids"`
	Withheld       string         `json:"withheld,omitempty"`
}

// ViewEdge is an edge as a reader sees it.
type ViewEdge struct {
	ID             string        `json:"id"`
	Source         string        `json:"source"`
	Target         string        `json:"target"`
	RelType        string        `json:"rel_type"`
	Status         string        `json:"status"`
	Standing       string        `json:"standing,omitempty"`
	Confidence     *float64      `json:"confidence,omitempty"`
	ValidFrom      string        `json:"valid_from,omitempty"`
	ValidTo        string        `json:"valid_to,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs"`
	SourceEventIDs []string      `json:"source_event_ids"`
}

// ProjectionStatus tells the reader how current the graph is.
type ProjectionStatus struct {
	Complete    bool    `json:"complete"` // no unfinished projection job: the graph reflects Postgres
	ProjectedAt *string `json:"projected_at"`
}

// View is the bounded neighborhood of an account (core.yaml#Graph).
type View struct {
	AccountID  string              `json:"account_id"`
	Nodes      []ViewNode          `json:"nodes"`
	Edges      []ViewEdge          `json:"edges"`
	Sections   map[string][]string `json:"sections"`
	Truncated  bool                `json:"truncated"`
	Withheld   bool                `json:"withheld"`
	Projection ProjectionStatus    `json:"projection"`
}

// nodeLabel is the short display label of a stored node.
func nodeLabel(typ string, p map[string]any) string {
	str := func(k string) string { s, _ := p[k].(string); return s }
	switch typ {
	case LabelAccount, LabelOpportunity:
		return str("name")
	case LabelPerson:
		if n := str("display_name"); n != "" {
			return n
		}
		return typ // a world read carries no display name (ADR-0019)
	case LabelActivity, LabelConversation:
		return str("activity_type")
	case LabelClaim:
		return str("field_path")
	case LabelCommitment:
		return "commitment"
	case LabelSignal:
		return str("signal_type")
	case LabelDecisionEpisode:
		if a := str("human_action"); a != "" {
			return a
		}
		return typ // a world read carries no verdict (ADR-0019)
	case LabelKnowledge:
		return str("key") + " " + str("title")
	case LabelDocument:
		return str("source_document_id")
	}
	return typ
}

// viewData picks the few properties a reader needs besides the label.
var viewData = map[string][]string{
	LabelPerson:          {"kind", "title"},
	LabelOpportunity:     {"motion"},
	LabelActivity:        {"activity_type", "source_system", "occurred_at", "visibility"},
	LabelConversation:    {"activity_type", "source_system", "occurred_at", "visibility"},
	LabelClaim:           {"field_path", "standing", "confidence"},
	LabelCommitment:      {"standing", "confidence"},
	LabelSignal:          {"signal_type", "rule"},
	LabelDecisionEpisode: {"human_action", "state_version"},
	LabelKnowledge:       {"key"},
}

func viewNode(s Stored) ViewNode {
	typ := s.Labels[0]
	n := ViewNode{ID: stringProp(s.Props, "id"), Type: typ, Label: nodeLabel(typ, s.Props), Status: stringProp(s.Props, "status"),
		ValidFrom: stringProp(s.Props, "valid_from"), ValidTo: stringProp(s.Props, "valid_to"),
		EvidenceRefs: refsOf(s.Props), SourceEventIDs: eventsOf(s.Props)}
	if n.ValidFrom == "" && typ != LabelAccount {
		n.ValidFrom = stringProp(s.Props, "created_at")
	}
	for _, k := range viewData[typ] {
		if v, ok := s.Props[k]; ok {
			if n.Data == nil {
				n.Data = map[string]any{}
			}
			n.Data[k] = v
		}
	}
	return n
}

func refsOf(p map[string]any) []EvidenceRef {
	ids := evidenceIDs(p)
	out := make([]EvidenceRef, len(ids))
	for i, id := range ids {
		out[i] = EvidenceRef{ActivityID: id}
	}
	return out
}

func evidenceIDs(p map[string]any) []string {
	switch v := p["evidence_activity_ids"].(type) {
	case []string:
		return v
	case []any:
		return anyStrings(v)
	}
	return []string{}
}

func viewEdge(s Stored) ViewEdge {
	e := ViewEdge{ID: stringProp(s.Props, "id"), Source: s.FromID, Target: s.ToID, RelType: s.Type, Status: stringProp(s.Props, "status"),
		Standing: stringProp(s.Props, "standing"), ValidFrom: stringProp(s.Props, "valid_from"), ValidTo: stringProp(s.Props, "valid_to"),
		EvidenceRefs: refsOf(s.Props), SourceEventIDs: eventsOf(s.Props)}
	if c, ok := s.Props["confidence"].(float64); ok {
		e.Confidence = &c
	}
	return e
}

// touchesHidden reports whether any evidence activity of the element is hidden.
func touchesHidden(p map[string]any, hidden map[string]bool) bool {
	if len(hidden) == 0 {
		return false
	}
	for _, id := range evidenceIDs(p) {
		if hidden[id] {
			return true
		}
	}
	return false
}

// stub replaces a node whose evidence is hidden: the id stays (it leads nowhere without Postgres
// access), everything else is withheld.
func stub(n ViewNode) ViewNode {
	return ViewNode{ID: n.ID, Type: n.Type, Label: n.Type, EvidenceRefs: []EvidenceRef{}, SourceEventIDs: []string{}, Withheld: WithheldVisibility}
}

func sortEdges(e []ViewEdge) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].RelType != e[j].RelType {
			return e[i].RelType < e[j].RelType
		}
		return e[i].ID < e[j].ID
	})
}

// openStatuses are the edge statuses of facts that currently hold.
var openStatuses = map[string]bool{"open": true, "active": true, statusRecorded: true}
