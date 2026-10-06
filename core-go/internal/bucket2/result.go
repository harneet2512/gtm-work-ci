// Package bucket2 builds the Bucket 2 gate results (HAR-97 D1-D10): the decision, the human's judgment and the
// action. A gate result is one structured judgment of one object of one episode (gate_result.v1.json):
// what was asked, what was observed, the verdict, why, the evidence it rests on, what it protects and which
// grader produced it. Rule R1 holds everywhere: no evidence means unknown, never pass. Calibration is deferred,
// so every result says calibrated=false and the surfaces say "not yet calibrated".
package bucket2

import (
	"fmt"
	"strings"
)

// Verdict is the gate verdict vocabulary. The older spelling "abstain" is read as unknown.
type Verdict string

// The verdicts a gate result can carry.
const (
	Pass    Verdict = "pass"
	Warn    Verdict = "warn"
	Fail    Verdict = "fail"
	Unknown Verdict = "unknown"
)

// ParseVerdict reads a stored or model verdict. abstain and anything unrecognised (including not_relevant)
// read as unknown: an unreadable verdict must never read as a pass.
func ParseVerdict(s string) Verdict {
	switch Verdict(strings.ToLower(strings.TrimSpace(s))) {
	case Pass:
		return Pass
	case Warn:
		return Warn
	case Fail:
		return Fail
	default:
		return Unknown
	}
}

// Grader says what produced a result.
type Grader struct {
	Kind          string `json:"kind"` // deterministic or model
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

// Deterministic is the grader of a state or arithmetic check.
var Deterministic = Grader{Kind: "deterministic"}

// ModelGrader is the grader of a model judge.
func ModelGrader(model, promptVersion string) Grader {
	return Grader{Kind: "model", Model: model, PromptVersion: promptVersion}
}

// Result is the stored gate result.
type Result struct {
	Gate         string   `json:"gate"`
	SubGate      string   `json:"sub_gate,omitempty"`
	Label        string   `json:"label,omitempty"`
	JudgedType   string   `json:"judged_type"`
	JudgedID     string   `json:"judged_id"`
	SpanID       string   `json:"span_id"`
	Verdict      Verdict  `json:"verdict"`
	Question     string   `json:"question"`
	Observed     string   `json:"observed"`
	Why          string   `json:"why"`
	EvidenceRefs []string `json:"evidence_refs"`
	Improves     string   `json:"improves"`
	Grader       Grader   `json:"grader"`
	Calibrated   bool     `json:"calibrated"`
}

// Finalize applies rule R1 and fills the gate's question and improves text when the builder left them empty.
// The receiver is not modified. A pass, warn or fail with no evidence becomes unknown and says why.
func (r Result) Finalize() Result {
	out := r
	if meta, ok := gateMeta[r.Gate]; ok {
		if out.Question == "" {
			out.Question = meta.Question
		}
		if out.Improves == "" {
			out.Improves = meta.Improves
		}
	}
	out.Verdict = ParseVerdict(string(r.Verdict))
	out.EvidenceRefs = dedupe(r.EvidenceRefs)
	out.Calibrated = false
	if out.Verdict != Unknown && len(out.EvidenceRefs) == 0 {
		out.Why = fmt.Sprintf("no evidence was cited, so this is unknown rather than %s (was: %s)", out.Verdict, r.Why)
		out.Verdict = Unknown
	}
	return out
}

// Validate reports whether a finalized result can be stored.
func (r Result) Validate() error {
	switch {
	case !GatePattern.MatchString(r.Gate):
		return fmt.Errorf("bucket2: %q is not a gate (B1-B9, D1-D10, S1-S5)", r.Gate)
	case gateMeta[r.Gate].Question == "" && (r.Question == "" || r.Improves == ""):
		return fmt.Errorf("bucket2: %s result must state its question and what it improves", r.Gate)
	case r.JudgedType == "" || r.JudgedID == "" || r.SpanID == "":
		return fmt.Errorf("bucket2: %s result needs judged_object and span_id", r.Gate)
	case r.Observed == "" || r.Why == "":
		return fmt.Errorf("bucket2: %s result needs what was observed and why", r.Gate)
	case r.Grader.Kind != "deterministic" && r.Grader.Kind != "model":
		return fmt.Errorf("bucket2: %s result has no grader kind", r.Gate)
	case r.Verdict != Unknown && len(r.EvidenceRefs) == 0:
		return fmt.Errorf("bucket2: %s result says %s with no evidence (rule R1)", r.Gate, r.Verdict)
	}
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// worst is the most severe verdict of vs (fail > warn > unknown > pass); unknown beats pass so a gate is never
// reported as passing on partial information.
func worst(vs ...Verdict) Verdict {
	rank := map[Verdict]int{Pass: 0, Unknown: 1, Warn: 2, Fail: 3}
	out := Pass
	for _, v := range vs {
		if rank[v] > rank[out] {
			out = v
		}
	}
	if len(vs) == 0 {
		return Unknown
	}
	return out
}
