package recompute

import "fmt"

// classify maps a literal change kind of human_delta.literal_changes to the field it changes and its coarse class.
// A kind outside the vocabulary is an error: an unknown change must not be given a field it may not have.
func classify(kind string) (Field, string, error) {
	switch kind {
	case "recipient_added", "recipient_removed", "recipient_role_changed":
		return Recipients, "stakeholder_change", nil
	case "subject_changed":
		return Subject, "content_change", nil
	case "paragraph_added", "paragraph_removed", "paragraph_edited":
		return Body, "content_change", nil
	case "channel_changed":
		return Channel, "channel_change", nil
	case "attachment_added", "attachment_removed":
		return Attachments, "attachment_change", nil
	default:
		return "", "", fmt.Errorf("recompute: unknown edit kind %q", kind)
	}
}

// evalReads declares which parts of the action each eval reads: the edit fields that can change its verdict. It
// is the E11 dependency table. An eval is listed with an empty set only when it reads none of the editable parts
// (it judges the state, the run trace or the action type, none of which a human edit changes). An eval that is not
// listed depends on every field: preservation is declared, never assumed.
var evalReads = map[string][]Field{
	// deterministic evals (core-go/internal/evals/deterministic)
	"recipient_correctness":       {Recipients},
	"date_commitment_consistency": {Subject, Body},
	"pricing_integrity":           {Subject, Body, Attachments},
	"crm_writeback":               {Channel, Body},
	"duplicate_action":            {Recipients, Channel, Subject},
	"provenance_coverage":         {Subject, Body},
	"permission_policy":           {Recipients, Channel, Attachments},
	// semantic evals about the state, the run or the action type: a human edit of the text does not change them
	"state_transition_support": {},
	"buyer_readiness":          {},
	"champion_strength":        {},
	"momentum":                 {},
	"action_stage_fit":         {},
	"expansion_readiness":      {},
	"trajectory":               {},
	// semantic evals that read the recipients
	"stakeholder_selection":   {Recipients},
	"stakeholder_coverage":    {Recipients},
	"economic_buyer_coverage": {Recipients},
	"channel_appropriateness": {Channel, Recipients},
	"champion_continuity":     {Recipients, Body},
	"relationship_pressure":   {Recipients, Subject, Body},
	"next_action_quality":     {Recipients, Subject, Body},
	// semantic evals that read the text
	"cta_calibration":           {Subject, Body},
	"next_step_quality":         {Subject, Body},
	"decision_process":          {Body},
	"business_case":             {Subject, Body},
	"customer_risk_sensitivity": {Subject, Body},
	"timing_cadence":            {Subject, Body},
	"grounding":                 {Subject, Body},
	"commitment_consistency":    {Subject, Body},
	"state_change_relevance":    {Subject, Body},
	"rep_style":                 {Subject, Body},
	"knowledge_applicability":   {Subject, Body},
	"exception_awareness":       {Body},
	"evidence_sufficiency":      {Subject, Body},
}

// dependsOn reports whether the eval reads the field. The learned axis kind (human_delta) can read anything, and
// an unlisted eval is treated the same way.
func dependsOn(evalType string, f Field) bool {
	fields, declared := evalReads[evalType]
	if !declared {
		return true
	}
	for _, d := range fields {
		if d == f {
			return true
		}
	}
	return false
}
