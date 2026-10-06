package bucket2

import "fmt"

// The recompute engine's derived invalidation is passed in as plain values: bucket2 must not import recompute (its
// tests reach strategystore, which imports bucket2 for the eval-class map).

// Status of a dependency invalidation (dependency_invalidation.v1.json status).
type Status string

// The statuses D7 distinguishes.
const (
	StatusNotDecided  Status = "not_decided"
	StatusUnedited    Status = "unedited"
	StatusEditsPend   Status = "edits_pending"
	StatusReevaluated Status = "reevaluated"
	StatusDiscarded   Status = "discarded"
	StatusUnavailable Status = "recomputation_unavailable"
)

// SpanRef is one object an edit touched or left alone (an invalidation span reference).
type SpanRef struct {
	Kind, RefID, Field, Label string
	Verdict                   string // "" when none
}

// Entry is one edit and what it did to the trace.
type Entry struct {
	Index                                  int
	Invalidated, Recomputed, NotRecomputed []SpanRef
}

// Invalidation is the part of the derived DependencyInvalidation D7 reads.
type Invalidation struct {
	RunID                       string
	Status                      Status
	Edited                      bool
	AccountID                   string
	VersionBefore, VersionAfter *int
	Preserved                   *bool
	Entries                     []Entry
}

// PropagationInput is what D7 reads: the recompute engine's derived invalidation of the run, plus whether the
// semantic judges were re-run for the edited candidate (needed only when the intent changed).
type PropagationInput struct {
	Invalidation  Invalidation
	IntentChanged bool
	// RejudgedAfterEdit lists the gate sub-results (D2/D8 judges) stored for the edited candidate after the edit.
	RejudgedAfterEdit []string
}

func refID(r SpanRef) string {
	switch {
	case r.RefID != "":
		return r.Kind + ":" + r.RefID
	case r.Field != "":
		return r.Kind + ":" + r.Field
	}
	return r.Kind + ":" + r.Label
}

func refsOf(entries []Entry, pick func(Entry) []SpanRef) []string {
	var out []string
	for _, e := range entries {
		for _, r := range pick(e) {
			out = append(out, refID(r))
		}
	}
	return out
}

// PropagationResults is D7: after the person changed something, did every dependent object recompute? It emits
// one result per assertion (dependents invalidated, no stale blocking descendant, unaffected state preserved,
// recompute finished before send and, when the intent changed, the semantic judges re-run). An unedited or
// discarded episode emits nothing: there was no override to propagate, so the gate reads "not measured".
func PropagationResults(in PropagationInput) []Result {
	inv := in.Invalidation
	if !inv.Edited || inv.Status == StatusUnedited || inv.Status == StatusDiscarded || inv.Status == StatusNotDecided {
		return nil
	}
	base := Result{Gate: "D7", JudgedType: "DependencyInvalidation", JudgedID: inv.RunID, SpanID: "recomputed_action:" + inv.RunID, Grader: Deterministic}
	out := []Result{invalidatedResult(base, inv), staleResult(base, inv), stateResult(base, inv), beforeSendResult(base, inv)}
	if in.IntentChanged {
		out = append(out, rejudgedResult(base, in))
	}
	for i := range out {
		out[i] = out[i].Finalize()
	}
	return out
}

func invalidatedResult(b Result, inv Invalidation) Result {
	b.SubGate = "dependents_invalidated"
	b.Verdict = Pass
	var empty []int
	for _, e := range inv.Entries {
		if len(e.Invalidated) == 0 {
			empty = append(empty, e.Index)
		}
	}
	b.EvidenceRefs = refsOf(inv.Entries, func(e Entry) []SpanRef { return e.Invalidated })
	b.Observed = fmt.Sprintf("%d edit(s) invalidated %d dependent object(s)", len(inv.Entries), len(b.EvidenceRefs))
	b.Why = "every edit invalidated the evals and artifact parts that read the edited field"
	if len(empty) > 0 {
		b.Verdict, b.Why = Fail, fmt.Sprintf("edit(s) %v invalidated nothing, but a changed artifact always has dependents", empty)
		b.EvidenceRefs = append(b.EvidenceRefs, "dependency_invalidation:"+inv.RunID)
	}
	return b
}

func staleResult(b Result, inv Invalidation) Result {
	b.SubGate = "no_stale_descendant"
	notRe := refsOf(inv.Entries, func(e Entry) []SpanRef { return e.NotRecomputed })
	b.EvidenceRefs = append([]string{"dependency_invalidation:" + inv.RunID}, notRe...)
	if inv.Status == StatusUnavailable || inv.Status == StatusEditsPend {
		b.Verdict, b.Observed = Unknown, "the send-time re-evaluation is not linked to this run"
		b.Why = "without the send-time batch it cannot be shown that no stale verdict survived"
		return b
	}
	var stale []string
	for _, e := range inv.Entries {
		for _, r := range e.NotRecomputed {
			if r.Kind == "eval_result" { // an old-artifact eval with no replacement is stale whatever its old verdict
				stale = append(stale, refID(r))
			}
		}
	}
	b.Observed = fmt.Sprintf("%d object(s) were reported as not recomputed (honestly listed, not shown as current)", len(notRe))
	b.Verdict, b.Why = Pass, "no eval of the old artifact is left without a replacement; what was not recomputed (ranking, knowledge use) is listed"
	if len(stale) > 0 {
		b.Verdict, b.Why, b.EvidenceRefs = Fail, "an eval of the old artifact was neither recomputed nor retired, so a stale verdict can still read as current", stale
	}
	return b
}

func stateResult(b Result, inv Invalidation) Result {
	b.SubGate = "state_preserved"
	s := inv
	b.EvidenceRefs = []string{"account_state:" + s.AccountID}
	b.Observed = fmt.Sprintf("account state version before %s, after %s", intText(s.VersionBefore), intText(s.VersionAfter))
	switch {
	case s.Preserved == nil:
		b.Verdict, b.Why = Unknown, "the state version and digest could not be compared"
	case *s.Preserved:
		b.Verdict, b.Why = Pass, "the stored version and content digest match: unaffected state was preserved"
	default:
		b.Verdict, b.Why = Fail, "the account state changed across a human edit, which never writes state"
	}
	return b
}

func intText(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprint(*v)
}

func beforeSendResult(b Result, inv Invalidation) Result {
	b.SubGate = "recomputed_before_send"
	recomputed := refsOf(inv.Entries, func(e Entry) []SpanRef { return e.Recomputed })
	b.EvidenceRefs = recomputed
	b.Observed = fmt.Sprintf("status %s with %d recomputed object(s)", inv.Status, len(recomputed))
	switch {
	case inv.Status == StatusReevaluated && len(recomputed) > 0:
		b.Verdict, b.Why = Pass, "the final artifact was re-evaluated at send time, before anything left the system"
	case inv.Status == StatusReevaluated:
		b.Verdict, b.Why = Fail, "the send happened but no recomputed result is stored"
		b.EvidenceRefs = []string{"dependency_invalidation:" + inv.RunID}
	default:
		b.Verdict, b.Why = Unknown, "the send has not happened or its re-evaluation is not linked"
		b.EvidenceRefs = []string{"dependency_invalidation:" + inv.RunID}
	}
	return b
}

func rejudgedResult(b Result, in PropagationInput) Result {
	b.SubGate = "intent_rejudged"
	b.EvidenceRefs = in.RejudgedAfterEdit
	b.Observed = fmt.Sprintf("the intent changed; %d semantic judge result(s) stored for the edited candidate after the edit", len(in.RejudgedAfterEdit))
	if len(in.RejudgedAfterEdit) > 0 {
		b.Verdict, b.Why = Pass, "the D2 and D8 judges were re-run for the edited candidate"
		return b
	}
	b.Verdict, b.Why = Fail, "the intent changed but the semantic judges were not re-run for the edited candidate"
	b.EvidenceRefs = []string{"dependency_invalidation:" + in.Invalidation.RunID}
	return b
}
