package deterministic

// Evaluate runs the seven HAR-97 §4 deterministic evals on one draft, in a fixed order. It never
// consults a model, a clock or a database: the same Input gives the same Judgments.
func Evaluate(in Input) []Judgment {
	return []Judgment{
		RecipientCorrectness(in),
		DateCommitmentConsistency(in),
		PricingIntegrity(in),
		CRMWriteback(in),
		DuplicateAction(in),
		ProvenanceCoverage(in),
		PermissionPolicy(in),
	}
}

// Results are the EvalResults of judgments.
func Results(js []Judgment) []EvalResult {
	out := make([]EvalResult, len(js))
	for i, j := range js {
		out[i] = j.Result
	}
	return out
}

// Gate is what the pre-action flow does with a set of judgments (HAR-97 §7).
type Gate string

// Gate outcomes: proceed to the semantic evals, revise the draft, or stop the run.
const (
	GateProceed Gate = "proceed"
	GateRevise  Gate = "revise"
	GateStop    Gate = "stop"
)

// Decide maps blocking failures to stop when revision cannot fix them and otherwise to revise.
// Non-blocking failures do not hold the draft back; the semantic evals see them.
func Decide(js []Judgment) Gate {
	gate := GateProceed
	for _, j := range js {
		for _, f := range j.Findings {
			switch {
			case f.Blocking && f.Remedy == RemedyStop:
				return GateStop
			case f.Blocking:
				gate = GateRevise
			}
		}
	}
	return gate
}
