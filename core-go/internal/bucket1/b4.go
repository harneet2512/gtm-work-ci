package bucket1

import (
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

// GradeB4 judges state mutation: the recorded state diff against /events/{id}/graph-diff, every change
// traceable to evidence, locality, materiality by the statediff rule, no unsupported transition and
// UNRESOLVED kept. Deterministic.
func GradeB4(ep Episode) Result {
	if len(ep.StateDiff) == 0 && !ep.GraphDiffKnown && !ep.GraphProjected && ep.Transition == nil {
		return Unmeasured(ep, "B4", "the episode recorded no state diff, graph-diff or transition")
	}
	as := []Assertion{b4GraphDiff(ep), b4Traceable(ep), b4Locality(ep), b4Materiality(ep), b4Transition(ep), b4Unresolved(ep)}
	return rollup(ep, "B4", JudgedObject{Type: "StateDiff", ID: ep.ID}, as)
}

func diffFields(ep Episode) []string {
	var out []string
	for _, c := range ep.StateDiff {
		out = append(out, c.Field)
	}
	sort.Strings(out)
	return out
}

// derivedStateFields change from the whole account (the transition detector, the reducer's derived fields), not from
// one claim, so a claim of the event need not speak about them.
var derivedStateFields = map[string]bool{
	"relationship_state": true, "open_transition": true, "last_meaningful_change": true, "last_customer_interaction": true,
	"primary_opportunity_changed": true, "coverage_gaps": true, "buying_group": true, "summary": true,
}

// anchor is a real activity of the episode to hang a failure on; with no activity it is empty (never an invented id).
func anchor(ep Episode) Ref {
	if a := firstActivity(ep); a != "" {
		return Ref{ActivityID: a, Note: "event " + ep.ID}
	}
	return Ref{}
}

// diffRefs is every evidence ref the state diff cites.
func diffRefs(ep Episode) []Ref {
	var out []Ref
	for _, c := range ep.StateDiff {
		out = append(out, c.Refs...)
	}
	return out
}

func b4GraphDiff(ep Episode) Assertion {
	const name = "diff_matches_graph_diff"
	if !ep.GraphDiffKnown && ep.GraphProjected {
		// The graph-diff counts node and edge changes, not state fields: compared by presence.
		stateChanged, graphChanged := len(ep.StateDiff) > 0, ep.GraphChanges > 0
		if stateChanged != graphChanged {
			return fail(name, fmt.Sprintf("state diff has %d changes, graph-diff %d attributed to the event", len(ep.StateDiff), ep.GraphChanges),
				"one of the two reports a change the other does not", append(diffRefs(ep), anchor(ep))...)
		}
		if !stateChanged {
			return na(name, "neither the state diff nor the graph-diff reports a change")
		}
		return pass(name, fmt.Sprintf("state diff (%d changes) and graph-diff (%d changes) both report this event's change", len(ep.StateDiff), ep.GraphChanges), diffRefs(ep)...)
	}
	if !ep.GraphDiffKnown {
		return unknown(name, "/events/{id}/graph-diff was not read for this episode")
	}
	got, want := diffFields(ep), append([]string(nil), ep.GraphDiffFields...)
	sort.Strings(want)
	var extra, missing []string
	for _, f := range got {
		if !contains(want, f) {
			extra = append(extra, f)
		}
	}
	for _, f := range want {
		if !contains(got, f) {
			missing = append(missing, f)
		}
	}
	if len(extra)+len(missing) > 0 {
		return fail(name, fmt.Sprintf("state diff [%s] vs graph-diff [%s]", strings.Join(got, ","), strings.Join(want, ",")),
			fmt.Sprintf("in the state diff only: %v; in the graph-diff only: %v", extra, missing), anchor(ep))
	}
	if len(got) == 0 {
		return na(name, "neither the state diff nor the graph-diff reports a change")
	}
	return pass(name, "state diff and graph-diff name the same fields: "+strings.Join(got, ", "), diffRefs(ep)...)
}

func b4Traceable(ep Episode) Assertion {
	const name = "diff_traceable_to_evidence"
	var refs, bad []Ref
	for _, c := range ep.StateDiff {
		if len(c.Refs) == 0 || len(ep.unknownRefs(c.Refs)) > 0 {
			bad = append(bad, Ref{Note: "field " + c.Field})
			continue
		}
		refs = append(refs, c.Refs...)
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d changes cite no evidence that exists", len(bad)), "an untraceable state change", append(bad, anchor(ep))...)
	}
	if len(refs) == 0 {
		return na(name, "no state change to trace")
	}
	return pass(name, fmt.Sprintf("%d changes, each cite evidence in the episode", len(ep.StateDiff)), refs...)
}

// b4Locality: a field may change only if a new claim of this event speaks about it.
func b4Locality(ep Episode) Assertion {
	const name = "mutation_locality"
	var bad []Ref
	for _, c := range ep.StateDiff {
		if !derivedStateFields[c.Field] && !contains(ep.NewClaimFields, c.Field) {
			bad = append(bad, Ref{Note: "field " + c.Field})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d fields changed that no new claim speaks about", len(bad)), "state outside the event's dependencies was mutated", append(bad, anchor(ep))...)
	}
	if len(ep.StateDiff) == 0 {
		return na(name, "no field changed")
	}
	return pass(name, "every changed field is spoken about by a claim of this event", diffRefs(ep)...)
}

func b4Materiality(ep Episode) Assertion {
	const name = "material_vs_nonmaterial"
	var bad []Ref
	for _, c := range ep.StateDiff {
		if c.Material != statediff.IsMaterialField(c.Field) {
			bad = append(bad, Ref{Note: fmt.Sprintf("field %s recorded material=%v", c.Field, c.Material)})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d changes carry the wrong materiality", len(bad)), "materiality differs from the statediff rule", append(bad, anchor(ep))...)
	}
	if len(ep.StateDiff) == 0 {
		return na(name, "no field changed")
	}
	return pass(name, "materiality of every change follows the statediff rule", diffRefs(ep)...)
}

func b4Transition(ep Episode) Assertion {
	const name = "no_unsupported_transition"
	t := ep.Transition
	if t == nil {
		return na(name, "this event opened no transition")
	}
	switch t.Status {
	case "candidate", "confirmed":
		if len(t.Support) == 0 || len(ep.unknownRefs(t.Support)) > 0 {
			return fail(name, "transition "+t.ID+" is "+t.Status+" with no supporting evidence in the episode", "an unsupported transition", anchor(ep))
		}
		return pass(name, "transition "+t.ID+" ("+t.Status+") cites its support", t.Support...)
	case "unresolved", "rejected":
		return pass(name, "transition "+t.ID+" kept "+t.Status, t.Support...)
	}
	return fail(name, "transition status "+t.Status, "not a known transition status", anchor(ep))
}

func b4Unresolved(ep Episode) Assertion {
	const name = "unresolved_kept"
	var bad []Ref
	for _, f := range ep.Unresolved {
		if v := strings.ToUpper(ep.StateAfter[f]); v != "" && v != "UNRESOLVED" && v != "UNKNOWN" {
			bad = append(bad, Ref{Note: "field " + f + " set to " + ep.StateAfter[f]})
		}
	}
	if len(bad) > 0 {
		return fail(name, fmt.Sprintf("%d fields the evidence cannot settle were given a value", len(bad)), "UNRESOLVED was resolved without evidence", append(bad, anchor(ep))...)
	}
	if len(ep.Unresolved) == 0 {
		return na(name, "no field is unresolved")
	}
	return pass(name, fmt.Sprintf("%d unresolved fields stay unresolved", len(ep.Unresolved)), diffRefs(ep)...)
}
