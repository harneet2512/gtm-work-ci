package bucket2

import (
	"fmt"
	"sort"
	"strings"
)

// Effect is one recorded dry-run effect of the run's execute step.
type Effect struct {
	Kind, IdempotencyKey string
	Targets              []string // person ids the effect was addressed to
}

// Execution is what D9 reads. Everything is a stored fact; the Slack or action delivery is judged from the
// recorded effect, not from the claim that it happened.
type Execution struct {
	EpisodeID, RunID, DecisionID string
	SendDecision                 string // send, discard or pending
	Effects                      []Effect
	ExpectedTargets              []string // the final to and cc
	BlockingFailAtSend           bool     // a blocking eval still failed on the final artifact
	EpisodeLinked                bool     // the run and the decision belong to the DecisionEpisode
	RequiredWrites               map[string]bool
}

// ExecutionResults is D9: the delivery happened once, to the correct target; a blocked action was not executed;
// the action is linked to the DecisionEpisode; and the required writes happened.
func ExecutionResults(x Execution) []Result {
	base := Result{Gate: "D9", JudgedType: "DecisionEpisode", JudgedID: x.EpisodeID, SpanID: "recomputed_action:" + x.EpisodeID, Grader: Deterministic}
	out := []Result{deliveredOnce(base, x), correctTarget(base, x), blockedNotExecuted(base, x), linked(base, x), writes(base, x)}
	for i := range out {
		out[i] = out[i].Finalize()
	}
	return out
}

func effectRefs(x Execution) []string {
	refs := []string{"agent_run:" + x.RunID}
	for _, e := range x.Effects {
		refs = append(refs, "effect:"+e.IdempotencyKey)
	}
	return refs
}

func deliveredOnce(b Result, x Execution) Result {
	b.SubGate = "delivered_once"
	b.EvidenceRefs = effectRefs(x)
	b.Observed = fmt.Sprintf("send decision %q with %d recorded effect(s)", x.SendDecision, len(x.Effects))
	keys := map[string]int{}
	for _, e := range x.Effects {
		keys[e.IdempotencyKey]++
	}
	switch {
	case x.SendDecision == "pending":
		b.Verdict, b.Why = Unknown, "the person has not decided yet, so there is nothing to execute"
	case x.SendDecision == "discard" && len(x.Effects) == 0:
		b.Verdict, b.Why = Pass, "the action was discarded and nothing was executed"
	case x.SendDecision == "discard":
		b.Verdict, b.Why = Fail, "a discarded action has a recorded effect"
	case len(x.Effects) == 0:
		b.Verdict, b.Why = Fail, "the person sent the action but no effect was recorded"
	case len(x.Effects) > 1 || hasDuplicate(keys):
		b.Verdict, b.Why = Fail, "more than one durable effect was recorded for one send"
	default:
		b.Verdict, b.Why = Pass, "exactly one effect was recorded for the send"
	}
	return b
}

func hasDuplicate(m map[string]int) bool {
	for _, n := range m {
		if n > 1 {
			return true
		}
	}
	return false
}

func correctTarget(b Result, x Execution) Result {
	b.SubGate = "correct_target"
	b.EvidenceRefs = effectRefs(x)
	if len(x.Effects) == 0 {
		b.Verdict, b.Observed, b.Why = Unknown, "no effect was recorded", "with no effect there is no target to check"
		b.EvidenceRefs = []string{"agent_run:" + x.RunID}
		return b
	}
	got := append([]string{}, x.Effects[0].Targets...)
	want := append([]string{}, x.ExpectedTargets...)
	sort.Strings(got)
	sort.Strings(want)
	b.Observed = fmt.Sprintf("the effect went to [%s]; the final recipients are [%s]", strings.Join(got, ", "), strings.Join(want, ", "))
	if strings.Join(got, ",") == strings.Join(want, ",") && len(want) > 0 {
		b.Verdict, b.Why = Pass, "the recorded target equals the final to and cc"
		return b
	}
	b.Verdict, b.Why = Fail, "the recorded target differs from the final recipients"
	return b
}

func blockedNotExecuted(b Result, x Execution) Result {
	b.SubGate = "blocked_not_executed"
	b.EvidenceRefs = effectRefs(x)
	b.Observed = fmt.Sprintf("blocking failure at send: %t; effects: %d", x.BlockingFailAtSend, len(x.Effects))
	if x.BlockingFailAtSend && len(x.Effects) > 0 {
		b.Verdict, b.Why = Fail, "an action that a blocking eval refused was executed"
		return b
	}
	b.Verdict, b.Why = Pass, "no action was executed while a blocking eval failed"
	return b
}

func linked(b Result, x Execution) Result {
	b.SubGate = "linked_to_episode"
	b.EvidenceRefs = []string{"decision_episode:" + x.EpisodeID, "agent_run:" + x.RunID, "human_strategy_decision:" + x.DecisionID}
	b.Observed = fmt.Sprintf("run %s and decision %s linked to episode %s: %t", x.RunID, x.DecisionID, x.EpisodeID, x.EpisodeLinked)
	if x.EpisodeLinked {
		b.Verdict, b.Why = Pass, "the executed action traces back to the DecisionEpisode"
	} else {
		b.Verdict, b.Why = Fail, "the action is not linked to the DecisionEpisode"
	}
	return b
}

func writes(b Result, x Execution) Result {
	b.SubGate = "required_writes"
	var missing []string
	names := make([]string, 0, len(x.RequiredWrites))
	for n := range x.RequiredWrites {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !x.RequiredWrites[n] {
			missing = append(missing, n)
		}
	}
	b.EvidenceRefs = []string{"agent_run:" + x.RunID, "human_strategy_decision:" + x.DecisionID}
	b.Observed = fmt.Sprintf("%d required write(s) checked: %s", len(names), strings.Join(names, ", "))
	switch {
	case len(names) == 0:
		b.Verdict, b.Why = Unknown, "no required write was declared"
		b.EvidenceRefs = []string{"agent_run:" + x.RunID}
	case len(missing) > 0:
		b.Verdict, b.Why = Fail, "required write(s) missing: "+strings.Join(missing, ", ")
	default:
		b.Verdict, b.Why = Pass, "every required write is present"
	}
	return b
}

// Feedback lists the records an episode emitted, each its own row with its own id. An empty id means the
// record does not exist (yet).
type Feedback struct {
	EpisodeID string
	// What happened in the episode, so a missing record can be told from one that is not due yet.
	Chose, Edited, Sent bool
	SelectionID         string
	SemanticEditID      string // the judgment inference id (the interpreted edit)
	ExplanationID       string // the judgment verdict row carrying a note or correction
	ExecutedActionID    string
	ReactionID          string
	OutcomeID           string
}

// FeedbackResults is D10: separate records for the selection, the semantic edit, the explicit explanation, the
// executed action and, when they arrive, the customer reaction and the business outcome. They are never
// collapsed into one truth label: the check proves each record is its own row.
func FeedbackResults(f Feedback) []Result {
	base := Result{Gate: "D10", JudgedType: "DecisionEpisode", JudgedID: f.EpisodeID, SpanID: "human_interaction:" + f.EpisodeID, Grader: Deterministic}
	type rec struct {
		sub, id string
		due     bool // the record must exist by now
		later   bool // may legitimately be absent
	}
	recs := []rec{
		{"selection", f.SelectionID, f.Chose, false},
		{"semantic_edit", f.SemanticEditID, f.Edited, false},
		{"explicit_explanation", f.ExplanationID, false, true},
		{"executed_action", f.ExecutedActionID, f.Sent, false},
		{"customer_reaction", f.ReactionID, false, true},
		{"business_outcome", f.OutcomeID, false, true},
	}
	var out []Result
	ids := map[string]string{}
	present := 0
	for _, r := range recs {
		x := base
		x.SubGate = r.sub
		switch {
		case r.id != "":
			x.Verdict, x.EvidenceRefs = Pass, []string{evidenceKind(r.sub) + ":" + r.id}
			x.Observed, x.Why = r.sub+" is stored as its own record", "emitted as a separate record, not folded into a single label"
			ids[r.id] = r.sub
			present++
		case r.due:
			x.Verdict, x.EvidenceRefs = Fail, []string{"decision_episode:" + f.EpisodeID}
			x.Observed, x.Why = r.sub+" has no record", "the episode reached this step but emitted no record for it"
		default:
			x.Verdict, x.EvidenceRefs = Unknown, []string{"decision_episode:" + f.EpisodeID}
			x.Observed, x.Why = r.sub+" has not been recorded", "not recorded (yet): it is absent or arrives later, never assumed"
		}
		out = append(out, x.Finalize())
	}
	return append(out, noCollapse(base, present, ids).Finalize())
}

func noCollapse(b Result, present int, distinct map[string]string) Result {
	b.SubGate = "not_collapsed"
	b.Observed = fmt.Sprintf("%d record(s) present, %d distinct id(s)", present, len(distinct))
	for id, sub := range distinct {
		b.EvidenceRefs = append(b.EvidenceRefs, sub+":"+id)
	}
	sort.Strings(b.EvidenceRefs)
	switch {
	case present == 0:
		b.Verdict, b.Why, b.EvidenceRefs = Unknown, "no record was emitted yet", []string{"decision_episode:" + b.JudgedID}
	case present != len(distinct):
		b.Verdict, b.Why = Fail, "two feedback kinds share one record, collapsing them into one label"
	default:
		b.Verdict, b.Why = Pass, "each feedback kind is its own record"
	}
	return b
}

// evidenceKind is the ref kind a feedback record is cited as: the executed action is the recorded effect itself.
func evidenceKind(sub string) string {
	if sub == "executed_action" {
		return "effect"
	}
	return sub
}
