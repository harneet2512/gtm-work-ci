// Package bucket1 grades Bucket 1 of the HAR-97 eval architecture (B1-B9, context and organizational
// intelligence) over one real episode. Deterministic checks decide whatever is objectively checkable; a
// model judgment, recorded once per gate per episode and replayed from a cassette, covers only what needs
// semantic reading. A gate whose model judgment was not recorded reads "unknown: not measured", never a pass.
package bucket1

import (
	"fmt"
	"strings"
)

// Verdicts. "abstain" is the older spelling of "unknown" and is normalised on every read; neither is a pass.
const (
	Pass    = "pass"
	Warn    = "warn"
	Fail    = "fail"
	Unknown = "unknown"
	// NotApplicable marks an assertion with nothing to check in this episode. It is neither a pass nor a gap:
	// it carries no evidence and never lifts a gate to pass.
	NotApplicable = "not_applicable"
)

// Graders.
const (
	GraderDeterministic = "deterministic"
	GraderMixed         = "deterministic+model"
	GraderModel         = "model"
)

// NormalizeVerdict maps every stored spelling to one of the four verdicts; anything unrecognised is unknown.
func NormalizeVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case Pass:
		return Pass
	case Warn:
		return Warn
	case Fail:
		return Fail
	}
	return Unknown
}

// Ref is one piece of evidence: an activity, optionally a claim and the quote that supports the finding.
type Ref struct {
	ActivityID string `json:"activity_id,omitempty"`
	ClaimID    string `json:"claim_id,omitempty"`
	Quote      string `json:"quote,omitempty"`
	StepID     string `json:"step_id,omitempty"` // a trace record (agent_run_steps[build_context]) instead of an activity
	Note       string `json:"note,omitempty"`
}

func (r Ref) empty() bool {
	return r.ActivityID == "" && r.ClaimID == "" && r.StepID == "" && r.Quote == ""
}

// JudgedObject names what a result is about (the Bucket 2 EvalResult shape).
type JudgedObject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Assertion is one checked statement of a gate.
type Assertion struct {
	Name     string `json:"name"`
	Verdict  string `json:"verdict"`
	Observed string `json:"observed"`
	Why      string `json:"why"`
	Refs     []Ref  `json:"evidence_refs"`
	Grader   string `json:"grader"`
}

// Result is the stored EvalResult of one gate on one episode.
type Result struct {
	Gate         string       `json:"gate"`
	JudgedObject JudgedObject `json:"judged_object"`
	SpanID       string       `json:"span_id"`
	Verdict      string       `json:"verdict"`
	Question     string       `json:"question"`
	Observed     string       `json:"observed"`
	Why          string       `json:"why"`
	EvidenceRefs []Ref        `json:"evidence_refs"`
	Improves     string       `json:"improves"`
	Grader       string       `json:"grader"`
	Model        string       `json:"model,omitempty"` // the model that made the recorded judgment, when one did
	Calibrated   bool         `json:"calibrated"`
	Assertions   []Assertion  `json:"assertions"`
	Episode      string       `json:"episode"`
	AccountID    string       `json:"account_id"`
}

// GateInfo is a gate's question and what it protects (contracts/evals/eval_registry.json gates; parity-tested).
type GateInfo struct{ Name, Question, Improves, SpanID string }

// Gates are B1-B9.
var Gates = map[string]GateInfo{
	"B1": {"Evidence fidelity and inference boundary", "Did gtm_ai understand the new event correctly?", "Stops corrupted raw context from becoming company knowledge or account state.", "evidence"},
	"B2": {"Identity and context linkage", "Did gtm_ai attach the event to the correct people, account, opportunity and relationships?", "Makes sure the organizational world is about the right entities before any reasoning happens.", "resolution"},
	"B3": {"Prior-context integration", "How does the new event relate to what gtm_ai already believed?", "Turns isolated messages into a coherent, evolving account history instead of a pile of facts.", "graph_mutation"},
	"B4": {"State mutation and change interpretation", "What actually changed in the business or account because of this event?", "Produces the current account state that decisions are allowed to act on.", "state"},
	"B5": {"Precedent retrieval", "Has a materially similar situation happened before inside this organization?", "Gives gtm_ai institutional memory instead of generic model instinct.", "precedents"},
	"B6": {"Precedent interpretation", "What actually happened in those earlier cases, and what lesson, if any, is supported?", "Makes \"this happened before\" useful evidence rather than imitation.", "precedents"},
	"B7": {"Organizational-knowledge applicability", "Which existing company knowledge applies to this exact state?", "Keeps company knowledge contextual so broad rules do not leak into the wrong situations.", "knowledge_applicable"},
	"B8": {"Current-intelligence synthesis", "Given the event, prior context, precedents and applicable knowledge, what does gtm_ai now believe about the account?", "Gives decisions a trustworthy basis.", "state"},
	"B9": {"Knowledge revision from new evidence", "Should this new episode change company knowledge?", "Makes organizational intelligence compound over time without turning one observation into a universal rule.", "knowledge_mutation"},
}

// GateOrder is B1 to B9.
var GateOrder = []string{"B1", "B2", "B3", "B4", "B5", "B6", "B7", "B8", "B9"}

func pass(name, observed string, refs ...Ref) Assertion {
	return Assertion{Name: name, Verdict: Pass, Observed: observed, Why: "holds", Refs: refs, Grader: GraderDeterministic}
}

func fail(name, observed, why string, refs ...Ref) Assertion {
	return Assertion{Name: name, Verdict: Fail, Observed: observed, Why: why, Refs: refs, Grader: GraderDeterministic}
}

func warn(name, observed, why string, refs ...Ref) Assertion {
	return Assertion{Name: name, Verdict: Warn, Observed: observed, Why: why, Refs: refs, Grader: GraderDeterministic}
}

// na is an assertion with nothing to check in this episode (rule R1: a vacuous check is never a pass).
func na(name, why string) Assertion {
	return Assertion{Name: name, Verdict: NotApplicable, Observed: "nothing to check", Why: why, Grader: GraderDeterministic}
}

func unknown(name, why string) Assertion {
	return Assertion{Name: name, Verdict: Unknown, Observed: "not measured", Why: why, Grader: GraderDeterministic}
}

// notMeasured is the model assertion of a gate whose judgment was never recorded.
func notMeasured(name, gate string) Assertion {
	return Assertion{Name: name, Verdict: Unknown, Observed: "not measured",
		Why: "no recorded model judgment for " + gate + " on this episode (cassette replay only; no live call was made)", Grader: GraderModel}
}

func hasModel(as []Assertion) bool {
	for _, a := range as {
		if a.Grader == GraderModel {
			return true
		}
	}
	return false
}

// rollup turns a gate's assertions into one Result. fail beats warn beats unknown beats pass; a pass needs
// every assertion measured and at least one evidence ref (rule R1: no evidence, no pass).
func rollup(ep Episode, gate string, obj JudgedObject, as []Assertion) Result {
	info := Gates[gate]
	r := Result{Gate: gate, JudgedObject: obj, SpanID: info.SpanID + ":" + obj.ID, Question: info.Question, Improves: info.Improves,
		Grader: GraderDeterministic, Calibrated: false, Assertions: as, Episode: ep.ID, AccountID: ep.AccountID, Verdict: Pass}
	if hasModel(as) {
		r.Grader = GraderMixed
	}
	var bad, unmeasured []string
	rank := map[string]int{Pass: 0, Unknown: 1, Warn: 2, Fail: 3}
	proven := false
	for i := range as {
		if as[i].Verdict != NotApplicable {
			as[i].Verdict = NormalizeVerdict(as[i].Verdict)
		}
		a := as[i]
		as[i].Refs = ep.resolvable(a.Refs)
		if a.Verdict == NotApplicable {
			continue
		}
		if a.Verdict == Pass && len(as[i].Refs) > 0 {
			proven = true
		}
		if rank[a.Verdict] > rank[r.Verdict] {
			r.Verdict = a.Verdict
		}
		switch a.Verdict {
		case Fail, Warn:
			bad = append(bad, a.Name+": "+a.Why)
		case Unknown:
			unmeasured = append(unmeasured, a.Name)
		}
		r.EvidenceRefs = appendRefs(r.EvidenceRefs, as[i].Refs)
	}
	if len(as) == 0 {
		r.Verdict = Unknown
	}
	if r.Verdict == Pass && !proven {
		r.Verdict = Unknown
		bad = append(bad, "no assertion passed on evidence that resolves in the episode, so there is nothing to pass on")
	}
	r.Observed = fmt.Sprintf("%d assertions: %d pass, %d warn/fail, %d not measured", len(as),
		countOf(as, Pass), len(bad), len(unmeasured))
	switch {
	case len(bad) > 0:
		r.Why = strings.Join(bad, "; ")
	case len(unmeasured) > 0:
		r.Why = "not measured: " + strings.Join(unmeasured, ", ")
	default:
		r.Why = "every assertion holds on cited evidence"
	}
	return r
}

// Unmeasured is the Result of a gate that could not run at all (its input is absent).
func Unmeasured(ep Episode, gate, reason string) Result {
	info := Gates[gate]
	return Result{Gate: gate, JudgedObject: JudgedObject{Type: "Episode", ID: ep.ID}, SpanID: info.SpanID + ":" + ep.ID,
		Verdict: Unknown, Question: info.Question, Observed: "not measured", Why: reason, EvidenceRefs: []Ref{},
		Improves: info.Improves, Grader: GraderDeterministic, Assertions: []Assertion{}, Episode: ep.ID, AccountID: ep.AccountID}
}

func countOf(as []Assertion, v string) int {
	n := 0
	for _, a := range as {
		if a.Verdict == v {
			n++
		}
	}
	return n
}

// resolvable keeps the refs that point at something real in the episode: an activity, claim, precedent or the
// build_context step. A ref that names nothing (or an id the episode does not hold) is not evidence.
func (ep Episode) resolvable(refs []Ref) []Ref {
	var out []Ref
	for _, r := range refs {
		if r.ActivityID == "" && r.ClaimID == "" && r.StepID == "" {
			continue
		}
		if len(ep.unknownRefs([]Ref{r})) == 0 {
			out = append(out, r)
		}
	}
	return out
}

func appendRefs(dst, src []Ref) []Ref {
	for _, r := range src {
		if r.empty() {
			continue
		}
		dup := false
		for _, d := range dst {
			if d == r {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, r)
		}
	}
	return dst
}
