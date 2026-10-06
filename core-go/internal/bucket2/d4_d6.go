package bucket2

import (
	"fmt"
	"sort"
	"strings"
)

// Selection is what D4 needs about the human's choice. Verdict maps are eval type to verdict of that
// candidate's EvalBundle.
type Selection struct {
	EpisodeID, DecisionID string
	Preferred, Chosen     string
	Order                 []string
	PreferredEvals        map[string]string
	ChosenEvals           map[string]string
	// Inference is nil until the worker has produced one; D4 is then unknown.
	Inference *Inference
}

// Inference is the part of the JudgmentInference the gates read.
type Inference struct {
	ID             string
	EditClasses    []string
	Labels         []string
	SignalStrength string
	Unknown        bool
	Statement      string
}

// Selection classes (the label of a D4 result).
const (
	SelAgree     = "AGREE"
	SelDefensibl = "DIFFERENT_BUT_DEFENSIBLE"
	SelCorrects  = "CORRECTS_STRATEGY"
	SelMissing   = "MISSING_CONTEXT"
	SelUnknown   = "UNKNOWN"
)

func issues(evals map[string]string) int {
	n := 0
	for _, v := range evals {
		if p := ParseVerdict(v); p == Warn || p == Fail {
			n++
		}
	}
	return n
}

func hasAny(have []string, want ...string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}

// ClassifySelection is D4: record gtm_ai's preferred candidate, the ranking, the chosen candidate and the eval
// bundles, and classify the choice as agreement, a different but defensible preference, a correction of
// gtm_ai's strategy, evidence of missing context, or unknown. The person is never assumed right: a different
// choice is only a correction when the chosen candidate was the better evaluated one.
func ClassifySelection(s Selection) Result {
	r := Result{Gate: "D4", JudgedType: "HumanStrategyDecision", JudgedID: s.DecisionID, SpanID: "human_interaction:" + s.DecisionID,
		Grader: Deterministic, EvidenceRefs: []string{"human_strategy_decision:" + s.DecisionID, "candidate:" + s.Preferred, "candidate:" + s.Chosen}}
	rank := fmt.Sprintf("ranking [%s]", strings.Join(s.Order, ", "))
	switch {
	case s.Chosen == "" || s.Preferred == "":
		r.Verdict, r.Label, r.Observed, r.Why = Unknown, SelUnknown, "no recorded choice or preference", "the choice is missing, so it cannot be interpreted"
		r.EvidenceRefs = []string{"human_strategy_decision:" + s.DecisionID}
	case s.Chosen == s.Preferred:
		r.Verdict, r.Label = Pass, SelAgree
		r.Observed = fmt.Sprintf("the person chose gtm_ai's preferred candidate %s; %s", s.Chosen, rank)
		r.Why = "agreement confirms the preference but is a weak signal on its own"
	case s.Inference == nil:
		r.Verdict, r.Label = Unknown, SelUnknown
		r.Observed = fmt.Sprintf("the person chose %s over the preferred %s; %s", s.Chosen, s.Preferred, rank)
		r.Why = "the judgment inference is not available yet, so the reason for the different choice is unknown"
	case s.Inference.Unknown:
		r.Verdict, r.Label = Unknown, SelUnknown
		r.Observed = fmt.Sprintf("the person chose %s over the preferred %s; %s", s.Chosen, s.Preferred, rank)
		r.Why = "the inference could not say why the person chose differently"
		r.EvidenceRefs = append(r.EvidenceRefs, "judgment_inference:"+s.Inference.ID)
	default:
		r = classifyDifferent(r, s, rank)
	}
	return r.Finalize()
}

func classifyDifferent(r Result, s Selection, rank string) Result {
	pi, ci := issues(s.PreferredEvals), issues(s.ChosenEvals)
	r.EvidenceRefs = append(r.EvidenceRefs, "judgment_inference:"+s.Inference.ID)
	r.Observed = fmt.Sprintf("the person chose %s (%d eval warnings or failures) over the preferred %s (%d); %s", s.Chosen, ci, s.Preferred, pi, rank)
	switch {
	case hasAny(s.Inference.EditClasses, "state", "factual") || hasAny(s.Inference.Labels, "corrected_fact"):
		r.Verdict, r.Label = Warn, SelMissing
		r.Why = "the person's change rests on a fact or state gtm_ai did not have, so context was missing"
	case pi > ci:
		r.Verdict, r.Label = Fail, SelCorrects
		r.Why = "the chosen candidate had fewer eval problems than gtm_ai's preferred one, so the person corrected the strategy"
	default:
		r.Verdict, r.Label = Pass, SelDefensibl
		r.Why = "the chosen candidate was not worse evaluated than the preferred one: a different but defensible preference"
	}
	return r
}

// Gap classes (the label of a D6 result).
const (
	GapCovered  = "COVERED"
	GapMisgrade = "MISGRADED"
	GapMissing  = "MISSING_CRITERION"
	GapPrefOnly = "HUMAN_PREFERENCE_ONLY"
	GapUnknown  = "UNKNOWN"
)

// StoredEval is one stored eval result of the chosen candidate or of the send-time batch.
type StoredEval struct {
	ID, EvalType, Verdict string
}

// EvalGapInput is what D6 reads: the interpreted edit classes and every stored eval, semantic ones included.
type EvalGapInput struct {
	EpisodeID   string
	Inference   *Inference
	EditCount   int
	StoredEvals []StoredEval
}

// DetectEvalGaps is D6: for each edit class of the interpreted delta, did a stored eval already warn or fail on
// that dimension? Covered means yes; misgraded means an eval covering the dimension ran and passed; missing
// criterion means none covered it; human preference only means a style edit. One result per class.
func DetectEvalGaps(in EvalGapInput) []Result {
	base := Result{Gate: "D6", JudgedType: "HumanDelta", JudgedID: in.EpisodeID, SpanID: "human_interaction:" + in.EpisodeID, Grader: Deterministic}
	if in.Inference == nil || in.EditCount == 0 || len(in.Inference.EditClasses) == 0 {
		r := base
		r.Verdict, r.Label = Unknown, GapUnknown
		r.Observed, r.Why = "no interpreted edit to compare against the stored evals", "without an interpreted edit there is no correction to explain"
		r.EvidenceRefs = []string{"decision_episode:" + in.EpisodeID}
		return []Result{r.Finalize()}
	}
	classes := append([]string(nil), in.Inference.EditClasses...)
	sort.Strings(classes)
	out := make([]Result, 0, len(classes))
	for _, c := range classes {
		out = append(out, gapForClass(base, in, c).Finalize())
	}
	return out
}

func gapForClass(base Result, in EvalGapInput, class string) Result {
	r := base
	r.SubGate = class
	covering := []StoredEval{}
	for _, e := range in.StoredEvals {
		if hasAny(EvalClasses(e.EvalType), class) {
			covering = append(covering, e)
		}
	}
	var warned, passed []string
	for _, e := range covering {
		switch ParseVerdict(e.Verdict) {
		case Warn, Fail:
			warned = append(warned, "eval:"+e.ID)
		case Pass:
			passed = append(passed, "eval:"+e.ID)
		}
	}
	switch {
	case class == "style":
		r.Verdict, r.Label, r.EvidenceRefs = Pass, GapPrefOnly, []string{"judgment_inference:" + in.Inference.ID}
		r.Observed, r.Why = "the edit is a style change", "a style edit is a preference, not a quality defect"
	case len(warned) > 0:
		r.Verdict, r.Label, r.EvidenceRefs = Pass, GapCovered, warned
		r.Observed, r.Why = fmt.Sprintf("a stored %s eval had already warned or failed", class), "an existing eval identified the dimension the person corrected"
	case len(passed) > 0:
		r.Verdict, r.Label, r.EvidenceRefs = Fail, GapMisgrade, passed
		r.Observed, r.Why = fmt.Sprintf("a %s eval ran and passed, yet the person corrected that dimension", class), "the evaluator existed but judged it too leniently"
	default:
		r.Verdict, r.Label, r.EvidenceRefs = Warn, GapMissing, []string{"judgment_inference:" + in.Inference.ID}
		r.Observed, r.Why = fmt.Sprintf("no stored eval covers the %s dimension the person corrected", class), "no current evaluator represents this semantic correction: a candidate criterion"
	}
	return r
}
