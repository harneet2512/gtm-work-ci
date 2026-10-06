package bucket1

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// ErrNoAttribution: the build_context step carries no knowledge_attribution record.
var ErrNoAttribution = errors.New("bucket1: the build_context step has no knowledge_attribution record")

// ParseAttribution reads the real record from agent_run_steps[build_context].detail.knowledge_attribution
// (contracts/schemas/knowledge_attribution.v1.json). An absent or malformed record is an error, never an
// empty attribution: B7 must judge what the run actually recorded.
func ParseAttribution(stepID string, detail []byte) (*Attribution, error) {
	var d struct {
		KA *struct {
			AsOf             time.Time `json:"as_of"`
			Retrieved        []string  `json:"retrieved"`
			Applicable       []string  `json:"applicable"`
			ExceptionBlocked []string  `json:"exception_blocked"`
			Used             []string  `json:"used"`
		} `json:"knowledge_attribution"`
	}
	if err := json.Unmarshal(detail, &d); err != nil {
		return nil, fmt.Errorf("bucket1: build_context detail of step %s: %w", stepID, err)
	}
	if d.KA == nil {
		return nil, fmt.Errorf("%w (step %s)", ErrNoAttribution, stepID)
	}
	return &Attribution{StepID: stepID, AsOf: d.KA.AsOf, Retrieved: d.KA.Retrieved, Applicable: d.KA.Applicable,
		ExceptionBlocked: d.KA.ExceptionBlocked, Used: d.KA.Used}, nil
}

// applicableStatuses mirrors contracts/knowledge/lifecycle.v1.json applicable_statuses.
var applicableStatuses = []string{knowledge.StatusProvisional, knowledge.StatusSupported, knowledge.StatusConfirmed}

// GradeB7 judges knowledge applicability from the run's real knowledge_attribution record: the matcher is
// re-run over the retrieved knowledge and the record's applicable and exception-blocked sets are compared
// with it. With nothing applicable the result is UNKNOWN (no applicable knowledge), never a pass.
func GradeB7(ep Episode) Result {
	att := ep.Attribution
	if att == nil {
		return Unmeasured(ep, "B7", ErrNoAttribution.Error()+": nothing to judge")
	}
	step := Ref{StepID: att.StepID, Note: "knowledge_attribution"}
	as := []Assertion{b7Consistent(att, step)}
	if ep.Situation == nil || (len(att.Retrieved) > 0 && len(ep.Knowledge) == 0) {
		as = append(as, unknown("scope_preconditions_exceptions", "the situation or the retrieved knowledge objects are not in the episode, so the matcher cannot be re-run"))
	} else {
		as = append(as, b7Rematch(ep, att, step)...)
	}
	as = append(as, b7Freshness(ep, att, step), b7Contradictions(ep, att, step))
	r := rollup(ep, "B7", JudgedObject{Type: "AgentRun", ID: att.StepID}, as)
	if len(att.Applicable) == 0 && r.Verdict != Fail {
		r.Verdict = Unknown
		r.Why = "no applicable knowledge for this state (" + fmt.Sprintf("%d retrieved, %d blocked by an exception", len(att.Retrieved), len(att.ExceptionBlocked)) + ")"
		r.EvidenceRefs = appendRefs(r.EvidenceRefs, []Ref{step})
	}
	return r
}

func subset(a, of []string) []string {
	var out []string
	for _, x := range a {
		if !contains(of, x) {
			out = append(out, x)
		}
	}
	return out
}

func b7Consistent(att *Attribution, step Ref) Assertion {
	const name = "record_consistent"
	var problems []string
	if x := subset(att.Applicable, att.Retrieved); len(x) > 0 {
		problems = append(problems, fmt.Sprintf("applicable but never retrieved: %v", x))
	}
	if x := subset(att.ExceptionBlocked, att.Retrieved); len(x) > 0 {
		problems = append(problems, fmt.Sprintf("exception-blocked but never retrieved: %v", x))
	}
	for _, id := range att.Applicable {
		if contains(att.ExceptionBlocked, id) {
			problems = append(problems, id+" is both applicable and exception-blocked")
		}
	}
	if x := subset(att.Used, att.Applicable); len(x) > 0 {
		problems = append(problems, fmt.Sprintf("used but not applicable: %v", x))
	}
	if len(problems) > 0 {
		return fail(name, "the knowledge_attribution record contradicts itself", strings.Join(problems, "; "), step)
	}
	return pass(name, fmt.Sprintf("%d retrieved, %d applicable, %d exception-blocked, %d used: nested as required",
		len(att.Retrieved), len(att.Applicable), len(att.ExceptionBlocked), len(att.Used)), step)
}

// b7Rematch re-runs the deterministic matcher: scope and preconditions (applicable set), exceptions
// (blocked set) and non-applicable knowledge rejected.
func b7Rematch(ep Episode, att *Attribution, step Ref) []Assertion {
	var retrieved []knowledge.Knowledge
	for _, k := range ep.Knowledge {
		if contains(att.Retrieved, k.ID) {
			retrieved = append(retrieved, k)
		}
	}
	results, err := knowledge.MatchAll(retrieved, *ep.Situation)
	if err != nil {
		return []Assertion{unknown("scope_preconditions_exceptions", "the matcher could not evaluate the retrieved knowledge: "+err.Error())}
	}
	var wrongApplicable, missedApplicable, wrongBlocked, missedBlocked []string
	for _, r := range results {
		id := r.Entry.KnowledgeID
		claimedApplicable, claimedBlocked := contains(att.Applicable, id), contains(att.ExceptionBlocked, id)
		switch r.Label {
		case knowledge.LabelApplies:
			if !claimedApplicable {
				missedApplicable = append(missedApplicable, id)
			}
			if claimedBlocked {
				wrongBlocked = append(wrongBlocked, id)
			}
		case knowledge.LabelExceptionTriggered:
			if claimedApplicable {
				wrongApplicable = append(wrongApplicable, id+" (an exception fires)")
			}
			if !claimedBlocked {
				missedBlocked = append(missedBlocked, id)
			}
		default:
			if claimedApplicable {
				wrongApplicable = append(wrongApplicable, id+" (preconditions unmet: "+strings.Join(r.Entry.UnmatchedConditions, ", ")+")")
			}
			if claimedBlocked {
				wrongBlocked = append(wrongBlocked, id)
			}
		}
	}
	scope := pass("scope_and_preconditions", fmt.Sprintf("%d retrieved knowledge objects re-matched: applicable set agrees", len(retrieved)), step)
	if len(wrongApplicable)+len(missedApplicable) > 0 {
		scope = fail("scope_and_preconditions", "the recorded applicable set differs from the matcher's",
			fmt.Sprintf("claimed applicable but not: %v; applicable but not claimed: %v", wrongApplicable, missedApplicable), step)
	}
	exc := pass("exceptions_checked", "exception-blocked set agrees with the matcher", step)
	if len(wrongBlocked)+len(missedBlocked) > 0 {
		exc = fail("exceptions_checked", "the recorded exception-blocked set differs from the matcher's",
			fmt.Sprintf("blocked without a firing exception: %v; exception fires but not blocked: %v", wrongBlocked, missedBlocked), step)
	}
	return []Assertion{scope, exc}
}

// b7Freshness: applicable knowledge must hold an applicable lifecycle status and exist at the replay clock.
func b7Freshness(ep Episode, att *Attribution, step Ref) Assertion {
	const name = "freshness_and_version"
	if len(ep.Knowledge) == 0 {
		return unknown(name, "the knowledge objects are not in the episode")
	}
	var bad []string
	for _, k := range ep.Knowledge {
		if !contains(att.Applicable, k.ID) {
			continue
		}
		if !contains(applicableStatuses, k.Status) {
			bad = append(bad, fmt.Sprintf("%s is %s", k.ID, k.Status))
		}
		if k.CreatedAt.After(att.AsOf) {
			bad = append(bad, fmt.Sprintf("%s was created %s, after the replay clock %s", k.ID, k.CreatedAt.Format(time.RFC3339), att.AsOf.Format(time.RFC3339)))
		}
	}
	if len(bad) > 0 {
		return fail(name, "knowledge that should not be applicable was", strings.Join(bad, "; "), step)
	}
	return pass(name, "every applicable knowledge object has an applicable status and existed at the replay clock", step)
}

// b7Contradictions surfaces applicable knowledge that gives opposite guidance: one object's do equals
// another's dont. It warns (the contradiction is the finding) and names both.
func b7Contradictions(ep Episode, att *Attribution, step Ref) Assertion {
	const name = "contradictory_knowledge_surfaced"
	var app []knowledge.Knowledge
	for _, k := range ep.Knowledge {
		if contains(att.Applicable, k.ID) {
			app = append(app, k)
		}
	}
	for i := range app {
		for j := range app {
			if i == j {
				continue
			}
			for _, do := range app[i].Guidance.Do {
				for _, dont := range app[j].Guidance.Dont {
					if norm(do) == norm(dont) {
						return warn(name, app[i].ID+" says do and "+app[j].ID+" says don't: "+do,
							"two applicable knowledge objects contradict each other", step)
					}
				}
			}
		}
	}
	return pass(name, fmt.Sprintf("no contradiction among %d applicable knowledge objects", len(app)), step)
}
