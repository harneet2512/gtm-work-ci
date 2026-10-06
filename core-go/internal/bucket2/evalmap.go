package bucket2

// Dimension classes link what a human changed (an edit class of the judgment inference) to the evals that
// should have predicted it. The same table backs D6 (eval-gap detection) and the send-time rule that lets a
// semantic warn or fail explain an edit (strategystore.semanticPredictors).

var evalClasses = map[string][]string{
	"stakeholder_selection": {"stakeholder", "recipient"}, "stakeholder_coverage": {"stakeholder", "recipient"},
	"economic_buyer_coverage": {"stakeholder", "recipient"}, "recipient_correctness": {"recipient", "stakeholder"},
	"champion_strength": {"stakeholder", "strategy"}, "champion_continuity": {"stakeholder", "strategy"},
	"timing_cadence": {"timing"}, "date_commitment_consistency": {"timing", "factual"}, "decision_timing": {"timing"},
	"artifact_timing": {"timing"}, "commitment_consistency": {"timing", "factual"},
	"cta_calibration": {"cta"}, "next_step_quality": {"cta"},
	"grounding": {"factual"}, "pricing_integrity": {"factual"}, "provenance_coverage": {"factual"},
	"artifact_grounding": {"factual"}, "decision_grounding": {"strategy", "state", "new_info"},
	"action_stage_fit": {"strategy"}, "state_change_relevance": {"strategy", "state"}, "next_action_quality": {"strategy"},
	"customer_risk_sensitivity": {"strategy", "risk"}, "relationship_pressure": {"strategy", "cta", "risk"},
	"buyer_readiness": {"state", "strategy"}, "state_transition_support": {"state", "new_info"}, "momentum": {"state", "strategy"},
	"rep_style": {"style", "tone", "wording"}, "channel_appropriateness": {"style", "tone"},
}

// EvalClasses returns the edit classes an eval type is responsible for (nil for an unmapped eval).
func EvalClasses(evalType string) []string { return evalClasses[evalType] }

// EvalTypesFor returns the eval types responsible for an edit class, sorted for stable output.
func EvalTypesFor(class string) []string {
	var out []string
	for t, cs := range evalClasses {
		for _, c := range cs {
			if c == class {
				out = append(out, t)
				break
			}
		}
	}
	sortStrings(out)
	return out
}

// LiteralKindClasses maps a literal change kind of a human edit to the edit classes it can express. A
// paragraph edit is ambiguous and so can express any substantive class except a recipient one.
func LiteralKindClasses(kind string) []string {
	switch kind {
	case "recipient_added", "recipient_removed", "recipient_role_changed":
		return []string{"recipient", "stakeholder"}
	case "cta_changed":
		return []string{"cta"}
	case "timing_changed":
		return []string{"timing"}
	case "subject_changed":
		return []string{"style", "wording", "tone"}
	case "paragraph_added", "paragraph_removed", "paragraph_edited":
		return []string{"cta", "factual", "timing", "strategy"}
	case "channel_changed", "action_type_changed":
		return []string{"strategy", "style"}
	case "crm_next_step_changed":
		return []string{"cta", "strategy"}
	default:
		return nil
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// EditKindNamesItsClass is true for a structured edit that names the dimension it changed (a recipient, a CTA, a
// time, the CRM next step); a paragraph, subject, channel or action edit is ambiguous about its dimension.
func EditKindNamesItsClass(kind string) bool {
	switch kind {
	case "recipient_added", "recipient_removed", "recipient_role_changed", "cta_changed", "timing_changed", "crm_next_step_changed":
		return true
	}
	return false
}
