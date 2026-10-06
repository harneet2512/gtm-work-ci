// Package evalarea rolls EvalResult eval types up into the four product areas of the HAR-145 Ghost Eval Control
// Plane: eval_type -> registry family (E1-E22) -> area. contracts/evals/eval_areas.json is the source of truth; the
// tables here are compiled from it by hand and a parity test (evalarea_test.go) keeps them identical, the same pattern
// as the web verdict vocabulary. The operational diagnostics M1-M5 are metrics and belong to no area. HAR-145 puts the
// whole decision and feedback loop (E5-E17) under Decision & Learning, ; Cliff / Experience holds the
// messages and no eval family.
package evalarea

import "maps"

// Area is one of the four product areas of the control plane.
type Area string

// The four areas, in display order.
const (
	Intelligence     Area = "intelligence"
	DecisionLearning Area = "decision_learning"
	CliffExperience  Area = "cliff_experience"
	System           Area = "system"
)

// Info is one area: its id, display label and order, and the registry families it holds.
type Info struct {
	ID       Area
	Label    string
	Order    int
	Families []string
}

var areas = []Info{
	{Intelligence, "Intelligence", 1, []string{"E1", "E2", "E3", "E4"}},
	{DecisionLearning, "Decision & Learning", 2, []string{"E5", "E6", "E7", "E8", "E9", "E10", "E11", "E12", "E13", "E14", "E15", "E16", "E17"}},
	{CliffExperience, "Cliff / Experience", 3, []string{}},
	{System, "System", 4, []string{"E18", "E19", "E20", "E21", "E22"}},
}

// familyNames are the registry family names (contracts/evals/eval_registry.json), E families only.
var familyNames = map[string]string{
	"E1": "Source / evidence fidelity", "E2": "Entity / account resolution", "E3": "Graph / world mutation",
	"E4": "State / transition interpretation", "E5": "Precedent retrieval", "E6": "Company-knowledge applicability",
	"E7": "Knowledge influence attribution", "E8": "Decision construction", "E9": "Decision ranking / recommendation",
	"E10": "Human interaction interpretation", "E11": "Edit / override propagation", "E12": "Action / output generation",
	"E13": "Tool use / permissions / writes", "E14": "Environment / workflow outcome", "E15": "Knowledge mutation",
	"E16": "Knowledge to future-behavior realization", "E17": "Continual knowledge revision", "E18": "Trace integrity",
	"E19": "Grader validity", "E20": "Stochastic reliability", "E21": "Regression + capability envelope",
	"E22": "Adversarial / proxy gaming",
}

// evalTypeFamily is the primary family of every EvalResult eval type (eval_areas.json eval_types).
var evalTypeFamily = map[string]string{
	"recipient_correctness": "E12", "date_commitment_consistency": "E12", "pricing_integrity": "E12", "crm_writeback": "E13",
	"duplicate_action": "E14", "provenance_coverage": "E1", "permission_policy": "E13", "state_transition_support": "E4",
	"buyer_readiness": "E8", "cta_calibration": "E12", "next_step_quality": "E12", "stakeholder_selection": "E8",
	"stakeholder_coverage": "E8", "economic_buyer_coverage": "E8", "champion_strength": "E8", "champion_continuity": "E8",
	"decision_process": "E8", "business_case": "E8", "momentum": "E8", "action_stage_fit": "E8", "expansion_readiness": "E8",
	"customer_risk_sensitivity": "E8", "relationship_pressure": "E8", "timing_cadence": "E8", "next_action_quality": "E8",
	"grounding": "E12", "commitment_consistency": "E12", "state_change_relevance": "E8", "channel_appropriateness": "E12",
	"rep_style": "E12", "knowledge_applicability": "E6", "exception_awareness": "E6", "evidence_sufficiency": "E9",
	"trajectory": "E18", "human_delta": "E10",
	// The split of grounding and timing_cadence by what they judge: the strategy (E8) or the action artifact (E12).
	"decision_grounding": "E8", "artifact_grounding": "E12", "decision_timing": "E8", "artifact_timing": "E12",
}

// familySpan is the TraceSpan kind (episode_trace.v1.json) the EvalResults of a family attach to (eval_areas.json
// family_spans). Families absent here (E18-E22, the validation and safety families) have no span.
var familySpan = map[string]string{
	"E1": "evidence", "E2": "resolution", "E3": "graph_mutation", "E4": "state", "E5": "precedents",
	"E6": "knowledge_applicable", "E7": "knowledge_used", "E8": "candidates", "E9": "ranking",
	"E10": "human_interaction", "E11": "recomputed_action", "E12": "candidates", "E13": "tool_call",
	"E14": "execution", "E15": "knowledge_mutation", "E16": "knowledge_used", "E17": "knowledge_mutation",
}

// SpanOfFamily is the trace span kind a registry family's results attach to.
func SpanOfFamily(family string) (string, bool) {
	s, ok := familySpan[family]
	return s, ok
}

// SpanOfEvalType is the trace span kind an EvalResult of the eval type attaches to.
func SpanOfEvalType(evalType string) (string, bool) {
	f, ok := FamilyOf(evalType)
	if !ok {
		return "", false
	}
	return SpanOfFamily(f)
}

var familyArea = func() map[string]Area {
	m := map[string]Area{}
	for _, a := range areas {
		for _, f := range a.Families {
			m[f] = a.ID
		}
	}
	return m
}()

// Areas returns the four areas in display order. The slice and its family lists are copies.
func Areas() []Info {
	out := make([]Info, len(areas))
	for i, a := range areas {
		a.Families = append([]string(nil), a.Families...)
		out[i] = a
	}
	return out
}

// EvalTypes returns a copy of the eval type -> primary family table.
func EvalTypes() map[string]string { return maps.Clone(evalTypeFamily) }

// FamilyOf is the primary registry family of an EvalResult eval type.
func FamilyOf(evalType string) (string, bool) {
	f, ok := evalTypeFamily[evalType]
	return f, ok
}

// AreaOfFamily is the area of a registry family; metric families (M1-M5) and unknown ids have none.
func AreaOfFamily(family string) (Area, bool) {
	a, ok := familyArea[family]
	return a, ok
}

// AreaOf is the area of an EvalResult eval type.
func AreaOf(evalType string) (Area, bool) {
	f, ok := FamilyOf(evalType)
	if !ok {
		return "", false
	}
	return AreaOfFamily(f)
}

// FamilyName is the registry's name of an E family.
func FamilyName(family string) (string, bool) {
	n, ok := familyNames[family]
	return n, ok
}
