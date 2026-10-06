package bucket2

import (
	"fmt"
	"strings"
)

// Dimension is one dimension verdict of a model judge (the worker's /v1/decision-judge answer).
type Dimension struct {
	Name, Verdict, Why string
	EvidenceRefs       []string
}

// Judged describes the object a model judge looked at and the judge that produced the dimensions.
type Judged struct {
	Gate, SubGate, Type, ID, SpanID string
	Model, PromptVersion            string
	Dimensions                      []Dimension
	Summary                         string
	// Extra is appended to the observation (for D1 the reaction class the judge thought right).
	Extra string
}

// FromDimensions folds the per-dimension verdicts of one model judgment into one result. A failing or warning
// dimension decides the result; otherwise at least one passing dimension makes it a pass and the unknown
// dimensions are named; with nothing but unknown the result is unknown. Rule R1 is applied on every dimension
// again here (core refuses what the worker should already have coerced) and again by Finalize.
func FromDimensions(j Judged) Result {
	r := Result{Gate: j.Gate, SubGate: j.SubGate, JudgedType: j.Type, JudgedID: j.ID, SpanID: j.SpanID,
		Grader: ModelGrader(j.Model, j.PromptVersion)}
	var obs, why []string
	var fails, warns, passes, unknowns int
	for _, d := range j.Dimensions {
		v := ParseVerdict(d.Verdict)
		if v != Unknown && len(d.EvidenceRefs) == 0 {
			v = Unknown
		}
		switch v {
		case Fail:
			fails++
		case Warn:
			warns++
		case Pass:
			passes++
		default:
			unknowns++
		}
		obs = append(obs, d.Name+"="+string(v))
		if v == Fail || v == Warn {
			why = append(why, d.Name+": "+d.Why)
		}
		if v != Unknown {
			r.EvidenceRefs = append(r.EvidenceRefs, d.EvidenceRefs...)
		}
	}
	r.Observed = strings.Join(obs, ", ")
	if j.Extra != "" {
		r.Observed += "; " + j.Extra
	}
	switch {
	case fails > 0:
		r.Verdict, r.Why = Fail, strings.Join(why, "; ")
	case warns > 0:
		r.Verdict, r.Why = Warn, strings.Join(why, "; ")
	case unknowns > 0:
		// Every dimension is required: a pass needs each one to pass with evidence, so one that could not be judged
		// makes the whole judgment unknown rather than a pass on partial information.
		r.Verdict = Unknown
		r.Why = fmt.Sprintf("%d of %d dimension(s) could not be judged from the evidence; %d passed", unknowns, len(j.Dimensions), passes)
	case passes > 0:
		r.Verdict, r.Why = Pass, firstNonEmpty(j.Summary, "every judged dimension passed with evidence")
	default:
		r.Verdict, r.Why = Unknown, firstNonEmpty(j.Summary, "the evidence did not settle any dimension")
	}
	return r.Finalize()
}

// InferenceResult is D5: the interpretation of the person's edit as a gate result. It rests on the evidence the
// inference cited; an inference that says unknown is reported as unknown, never as a confident class.
func InferenceResult(episodeID string, inf Inference, model string, evidence []string) Result {
	r := Result{Gate: "D5", JudgedType: "JudgmentInference", JudgedID: inf.ID, SpanID: "human_interaction:" + episodeID,
		Grader: ModelGrader(model, "judgment_inference:v1"), EvidenceRefs: evidence}
	classes := "no edit class"
	if len(inf.EditClasses) > 0 {
		classes = "edit class " + strings.Join(inf.EditClasses, ", ")
	}
	r.Observed = fmt.Sprintf("%s; signal %s; %s", classes, firstNonEmpty(inf.SignalStrength, "not stated"), inf.Statement)
	if inf.Unknown {
		r.Verdict, r.Why = Unknown, "the inference could not say what the person's choice or edit meant"
		return r.Finalize()
	}
	r.Verdict, r.Why = Pass, "the choice and edit were interpreted from cited evidence; the person's verdict can still correct it"
	return r.Finalize()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
