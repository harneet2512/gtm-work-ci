// Package demomine is HAR-129 section 1's demo-case mining: it replays real CRMArena timelines one event at
// a time through the ingest -> state path, records what each event materially changed, scores candidate
// sequences for demo usefulness with transparent weights, and freezes the chosen case as a DemoManifest.
//
// Hard rule (HAR-129): the story is discovered from the data, never chosen first. Nothing here reorders
// events, writes expectations into state or consults an LLM.
package demomine

// Change dimensions (contracts common.v1.json#changeDimension).
const (
	DimStakeholder = "stakeholder_structure"
	DimOwnership   = "relationship_ownership"
	DimIntent      = "buyer_intent"
	DimRisk        = "blockers_risk"
	DimNextStep    = "next_step_commitment"
	DimAction      = "recommended_action"
)

// Dimensions lists the six dimensions in contract order.
var Dimensions = []string{DimStakeholder, DimOwnership, DimIntent, DimRisk, DimNextStep, DimAction}

// Provenance of the base replay. The synthetic layer (HAR-131) is not generated yet, so every event is base.
const (
	OriginDataset    = "dataset"
	ProvenanceCRMB2B = "crmarena-pro:b2b"
	LayerBase        = "base"
	LayerSynthetic   = "synthetic"
)

// EventRecord is what one replayed event did to the world, as measured after it was ingested and its
// recompute drained. It holds no database-assigned ids, so the same input always renders the same bytes.
type EventRecord struct {
	Position int `json:"position"`
	// EventID is the replay dataset's stable id of the event (EventUUID), the id a manifest holds it under.
	EventID        string `json:"event_id"`
	SourceSystem   string `json:"source_system"`
	SourceObjectID string `json:"source_object_id"`
	SourceEventKey string `json:"source_event_key"`
	OccurredAt     string `json:"occurred_at"`
	Origin         string `json:"origin"`
	Provenance     string `json:"provenance"`
	// Layer is "base" for the dataset replay and "synthetic" for the labelled synthetic layer.
	Layer string `json:"layer"`
	// Resolved is false when the event reached no account (nothing recomputed).
	Resolved bool `json:"resolved"`
	// AccountStateVersion and OpportunityStateVersion are the versions after the event (0 = none yet).
	AccountStateVersion     int `json:"account_state_version"`
	OpportunityStateVersion int `json:"opportunity_state_version"`
	// DealCreated marks the event that created the deal's first state: nothing changed yet, so it earns no dimension.
	DealCreated bool `json:"deal_created"`
	// MaterialDiff is the account StateDiff's is_material flag.
	MaterialDiff bool `json:"material_diff"`
	// ChangedFields are the deal's state fields (and buying_group / coverage_gaps) whose value changed.
	ChangedFields []string `json:"changed_fields"`
	Signals       []string `json:"signals"`
	// RelationshipChanged is the account's relationship state or open transition moving (detector on).
	RelationshipChanged bool     `json:"relationship_changed"`
	TriggerEligible     bool     `json:"trigger_eligible"`
	TriggerReasons      []string `json:"trigger_reasons"`
	// Dimensions are the distinct material change dimensions of the event; Evidence says why, as
	// "field:<name>", "signal:<type>", "relationship:state" or "trigger:<reason>".
	Dimensions []string `json:"dimensions"`
	Evidence   []string `json:"evidence"`
}

// IsMaterial reports whether the event changed at least one dimension.
func (r EventRecord) IsMaterial() bool { return len(r.Dimensions) > 0 }

// Has reports whether the event changed the dimension.
func (r EventRecord) Has(dim string) bool {
	for _, d := range r.Dimensions {
		if d == dim {
			return true
		}
	}
	return false
}
