package demomine

import "sort"

// fieldDimension maps a deal state field to the dimension it reports. Fields that change on almost every
// activity or are free text (last_customer_interaction, last_meaningful_change, summary) map to nothing,
// as statediff excludes them from materiality.
var fieldDimension = map[string]string{
	"champion": DimStakeholder, "economic_buyer": DimStakeholder, "champion_status": DimStakeholder,
	"buying_group": DimStakeholder, "coverage_gaps": DimStakeholder,
	"owner": DimOwnership,
	"stage": DimIntent, "motion": DimIntent, "amount": DimIntent, "product_use_case": DimIntent, "commercial_issue": DimIntent,
	"blockers": DimRisk, "objections": DimRisk, "health": DimRisk, "relationship_risk": DimRisk,
	"current_commitments": DimNextStep, "next_milestone": DimNextStep, "next_meeting": DimNextStep,
	"decision_process": DimNextStep, "decision_criteria": DimNextStep,
}

// signalDimension maps a signal type (migration 0004) to the dimension it reports. Signals with no entry
// (customer_replied, field_contradicted) report engagement or data quality, not a business change.
var signalDimension = map[string]string{
	"new_stakeholder_entered": DimStakeholder, "stakeholder_gap": DimStakeholder, "champion_reactivated": DimStakeholder,
	"champion_delegated": DimOwnership,
	"pricing_interest":   DimIntent, "expansion_interest": DimIntent, "stage_advanced": DimIntent, "product_usage_increased": DimIntent,
	"security_blocker_appeared": DimRisk, "blocker_resolved": DimRisk, "support_risk_spike": DimRisk,
	"champion_weakened": DimRisk, "customer_went_silent": DimRisk, "stage_regressed": DimRisk,
	"next_meeting_missing": DimNextStep, "commitment_overdue": DimNextStep, "meeting_accepted": DimNextStep,
}

// Observation is the raw material of one event's record, read from the stores after it was processed.
type Observation struct {
	// DealCreated: this event produced the deal's first state version.
	DealCreated bool
	// ChangedFields are the deal-state fields that changed against the previous version.
	ChangedFields []string
	// Signals are the signal types of the event's StateDiff scoped to the deal (or to no deal).
	Signals []string
	// RelationshipChanged: the account's relationship state or open transition moved (detector on).
	RelationshipChanged bool
	// TriggerEligible and TriggerReasons are the trigger evaluation of the event's recompute.
	TriggerEligible bool
	TriggerReasons  []string
}

// Classify turns an observation into the event's dimensions and the evidence for each, both in a
// deterministic order. The first state of a deal earns no dimension from field changes (the deal merely
// appeared); signals and the trigger still count.
func Classify(o Observation) (dims, evidence []string) {
	seen := map[string]bool{}
	add := func(dim, why string) {
		seen[dim] = true
		evidence = append(evidence, why)
	}
	if !o.DealCreated {
		for _, f := range o.ChangedFields {
			if dim, ok := fieldDimension[f]; ok {
				add(dim, "field:"+f)
			}
		}
		if o.RelationshipChanged {
			add(DimOwnership, "relationship:state")
		}
	}
	for _, s := range o.Signals {
		if dim, ok := signalDimension[s]; ok {
			add(dim, "signal:"+s)
		}
	}
	// The decision point: the trigger would run the account agent on this event, i.e. the next action is open.
	if o.TriggerEligible {
		for _, r := range o.TriggerReasons {
			add(DimAction, "trigger:"+r)
		}
	}
	for _, d := range Dimensions {
		if seen[d] {
			dims = append(dims, d)
		}
	}
	sort.Strings(evidence)
	return dims, evidence
}
