package ctxgraph

import (
	"fmt"
	"sort"
)

// Node labels (contracts/graph/ontology.v1.json labels).
const (
	LabelAccount         = "Account"
	LabelPerson          = "Person"
	LabelOpportunity     = "Opportunity"
	LabelActivity        = "Activity"
	LabelConversation    = "Conversation"
	LabelClaim           = "Claim"
	LabelCommitment      = "Commitment"
	LabelDocument        = "Document"
	LabelSignal          = "Signal"
	LabelDecisionEpisode = "DecisionEpisode"
	LabelKnowledge       = "Knowledge"
)

// Relationship types (contracts/graph/ontology.v1.json relationships).
const (
	RelWorksAt             = "WORKS_AT"
	RelBelongsTo           = "BELONGS_TO"
	RelChampionFor         = "CHAMPION_FOR"
	RelEconomicBuyerFor    = "ECONOMIC_BUYER_FOR"
	RelTechnicalEvaluator  = "TECHNICAL_EVALUATOR_FOR"
	RelInfluences          = "INFLUENCES"
	RelParticipatedIn      = "PARTICIPATED_IN"
	RelInvolves            = "INVOLVES"
	RelAbout               = "ABOUT"
	RelOwns                = "OWNS"
	RelSupports            = "SUPPORTS"
	RelReportsTo           = "REPORTS_TO"
	RelDelegatedTo         = "DELEGATED_TO"
	RelSharedWith          = "SHARED_WITH"
	RelSupportedBy         = "SUPPORTED_BY"
	RelAboutAccount        = "ABOUT_ACCOUNT"
	RelAboutPerson         = "ABOUT_PERSON"
	RelAboutOpportunity    = "ABOUT_OPPORTUNITY"
	RelMadeBy              = "MADE_BY"
	RelMadeIn              = "MADE_IN"
	RelDerivedFrom         = "DERIVED_FROM"
	RelTriggeredBy         = "TRIGGERED_BY"
	RelUsedKnowledge       = "USED_KNOWLEDGE"
	RelAppliesTo           = "APPLIES_TO"
	skipRelNotInOntology   = "rel_type_not_in_ontology"
	skipEndpointNotAllowed = "endpoint_type_not_in_ontology"
	skipEndpointMissing    = "endpoint_not_in_account_scope"
)

// LabelSpec mirrors one entry of ontology.v1.json labels.
type LabelSpec struct {
	Table    string
	Extends  string
	Global   bool
	Required []string
}

// RelSpec mirrors one entry of ontology.v1.json relationships.
type RelSpec struct {
	From     []string
	To       []string
	Required []string
}

var (
	commonNodeRequired = []string{"id", "pg_table", "evidence_activity_ids", "source_event_ids", "created_at", "updated_at", "h"}
	commonEdgeRequired = []string{"id", "account_id", "valid_from", "status", "evidence_activity_ids", "source_event_ids", "created_at", "updated_at", "h"}

	activityProps = []string{"activity_type", "source_system", "occurred_at", "visibility"}
	claimProps    = []string{"field_path", "standing", "confidence", "status", "valid_from"}
	roleRel       = RelSpec{Required: []string{"confidence", "standing"}}
)

// Labels is the node ontology.
var Labels = map[string]LabelSpec{
	LabelAccount:         {Table: "accounts", Required: []string{"name", "status"}},
	LabelPerson:          {Table: "people", Required: []string{"kind", "internal_only"}},
	LabelOpportunity:     {Table: "opportunities", Required: []string{"name", "motion"}},
	LabelActivity:        {Table: "activities", Required: activityProps},
	LabelConversation:    {Table: "activities", Extends: LabelActivity, Required: activityProps},
	LabelClaim:           {Table: "claims", Required: claimProps},
	LabelCommitment:      {Table: "claims", Required: claimProps},
	LabelDocument:        {Table: "source_events", Global: true, Required: []string{"source_document_id"}},
	LabelSignal:          {Table: "signals", Required: []string{"signal_type", "valid_from", "rule"}},
	LabelDecisionEpisode: {Table: "decision_episodes", Required: []string{"state_version", "valid_from"}},
	LabelKnowledge:       {Table: "knowledge", Global: true, Required: []string{"key", "title", "status"}},
}

func role(from, to []string) RelSpec { return RelSpec{From: from, To: to, Required: roleRel.Required} }

var (
	person  = []string{LabelPerson}
	account = []string{LabelAccount}
	opp     = []string{LabelOpportunity}
	act     = []string{LabelActivity, LabelConversation}
)

// Relationships is the edge ontology.
var Relationships = map[string]RelSpec{
	RelWorksAt:            role(person, account),
	RelBelongsTo:          role(opp, account),
	RelChampionFor:        role(person, opp),
	RelEconomicBuyerFor:   role(person, opp),
	RelTechnicalEvaluator: role(person, opp),
	RelInfluences:         role(person, opp),
	RelParticipatedIn:     role(person, []string{LabelConversation}),
	RelInvolves:           role(act, person),
	RelAbout:              role(act, []string{LabelOpportunity, LabelAccount}),
	RelOwns:               role(person, []string{LabelOpportunity, LabelAccount}),
	RelSupports:           role(person, opp),
	RelReportsTo:          role(person, person),
	RelDelegatedTo:        role(person, person),
	RelSharedWith:         role([]string{LabelDocument}, person),
	RelSupportedBy: {From: []string{LabelClaim, LabelKnowledge},
		To: []string{LabelActivity, LabelConversation, LabelDocument, LabelDecisionEpisode}},
	RelAboutAccount:     {From: []string{LabelClaim, LabelCommitment, LabelSignal, LabelDecisionEpisode}, To: account},
	RelAboutPerson:      {From: []string{LabelClaim, LabelCommitment, LabelSignal}, To: person},
	RelAboutOpportunity: {From: []string{LabelClaim, LabelCommitment, LabelSignal, LabelDecisionEpisode}, To: opp},
	RelMadeBy:           {From: []string{LabelCommitment}, To: person},
	RelMadeIn:           {From: []string{LabelCommitment}, To: []string{LabelConversation, LabelActivity}},
	RelDerivedFrom: {From: []string{LabelSignal},
		To: []string{LabelClaim, LabelCommitment, LabelActivity, LabelConversation}},
	RelTriggeredBy:   {From: []string{LabelDecisionEpisode}, To: []string{LabelSignal, LabelActivity, LabelConversation}},
	RelUsedKnowledge: {From: []string{LabelDecisionEpisode}, To: []string{LabelKnowledge}},
	RelAppliesTo:     {From: []string{LabelKnowledge}, To: []string{LabelAccount, LabelOpportunity}, Required: []string{"basis"}},
}

// labelConforms reports whether a node with primary label l may stand where one of allowed is named.
func labelConforms(l string, allowed []string) bool {
	for _, a := range allowed {
		if a == l {
			return true
		}
	}
	if ext := Labels[l].Extends; ext != "" {
		return labelConforms(ext, allowed)
	}
	return false
}

// ValidateNode checks a node against the ontology: a known label, the common properties, the
// label's own properties and account scoping.
func ValidateNode(n Node) error {
	spec, ok := Labels[n.Primary()]
	if !ok {
		return fmt.Errorf("ctxgraph: label %q is not in the ontology", n.Primary())
	}
	if spec.Extends != "" && (len(n.Labels) < 2 || n.Labels[1] != spec.Extends) {
		return fmt.Errorf("ctxgraph: %s %s must also carry label %s", n.Primary(), n.ID, spec.Extends)
	}
	if err := requireProps(n.Props, commonNodeRequired, spec.Required); err != nil {
		return fmt.Errorf("ctxgraph: %s %s: %w", n.Primary(), n.ID, err)
	}
	_, scoped := n.Props["account_id"]
	if scoped == (n.Scope == "") {
		return fmt.Errorf("ctxgraph: %s %s: account_id must be present exactly when the node is scoped", n.Primary(), n.ID)
	}
	if spec.Global && n.Scope != "" {
		return fmt.Errorf("ctxgraph: %s %s is a global label but carries an account scope", n.Primary(), n.ID)
	}
	return nil
}

// ValidateEdge checks an edge against the ontology: a known type, allowed endpoint labels and the
// required properties.
func ValidateEdge(e Edge) error {
	spec, ok := Relationships[e.Type]
	if !ok {
		return fmt.Errorf("ctxgraph: relationship %q is not in the ontology", e.Type)
	}
	if !labelConforms(e.FromLabel, spec.From) || !labelConforms(e.ToLabel, spec.To) {
		return fmt.Errorf("ctxgraph: %s from %s to %s is not allowed by the ontology", e.Type, e.FromLabel, e.ToLabel)
	}
	if err := requireProps(e.Props, commonEdgeRequired, spec.Required); err != nil {
		return fmt.Errorf("ctxgraph: %s %s: %w", e.Type, e.ID, err)
	}
	return nil
}

func requireProps(props map[string]any, lists ...[]string) error {
	for _, list := range lists {
		for _, k := range list {
			if _, ok := props[k]; !ok {
				return fmt.Errorf("missing required property %q", k)
			}
		}
	}
	return nil
}

// ValidateSnapshot checks every node and edge and that every edge endpoint is a node of the snapshot
// (or a global node the caller supplies separately via extra).
func ValidateSnapshot(s Snapshot) error {
	have := map[string]bool{}
	for _, n := range s.Nodes {
		if err := ValidateNode(n); err != nil {
			return err
		}
		have[n.Key()] = true
	}
	for _, e := range s.Edges {
		if err := ValidateEdge(e); err != nil {
			return err
		}
		if !have[NodeKey(e.FromLabel, e.FromID)] || !have[NodeKey(e.ToLabel, e.ToID)] {
			return fmt.Errorf("ctxgraph: %s %s has an endpoint outside the snapshot", e.Type, e.ID)
		}
	}
	return nil
}

// sortedLabels returns the ontology labels in a stable order.
func sortedLabels() []string {
	out := make([]string, 0, len(Labels))
	for l := range Labels {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// sortedRelTypes returns the ontology relationship types in a stable order.
func sortedRelTypes() []string {
	out := make([]string, 0, len(Relationships))
	for r := range Relationships {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
