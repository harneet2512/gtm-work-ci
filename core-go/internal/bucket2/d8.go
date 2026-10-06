package bucket2

import (
	"fmt"
	"strings"
)

// FinalArtifact is what the deterministic part of D8 reads about the exact artifact about to be sent.
type FinalArtifact struct {
	EpisodeID, CandidateID string
	To, CC                 []string        // person ids
	AccountPeople          map[string]bool // people of the account
	InternalOnly           map[string]bool // people who must never receive an external message
	Body                   string
	SelectedBody           string   // the selected candidate's original body
	OtherBodies            []string // the bodies of the candidates that were NOT selected
	SendEvals              []SendEval
}

// SendEval is one send-time deterministic eval result of the final artifact.
type SendEval struct {
	ID, EvalType, Verdict string
	Blocking              bool
}

const staleShingle = 60 // characters: a sentence this long copied from a rejected candidate is stale content

// FinalArtifactResults is the deterministic part of D8: recipients and CC, no stale content from another
// candidate, and the policy gates the send-time evaluation applied.
func FinalArtifactResults(f FinalArtifact) []Result {
	base := Result{Gate: "D8", JudgedType: "FinalArtifact", JudgedID: f.CandidateID, SpanID: "recomputed_action:" + f.EpisodeID, Grader: Deterministic}
	out := []Result{recipientsResult(base, f), staleContentResult(base, f), policyResult(base, f)}
	for i := range out {
		out[i] = out[i].Finalize()
	}
	return out
}

func recipientsResult(b Result, f FinalArtifact) Result {
	b.SubGate = "recipients"
	all := append(append([]string{}, f.To...), f.CC...)
	b.EvidenceRefs = prefixed("person:", all)
	b.Observed = fmt.Sprintf("%d to and %d cc recipient(s)", len(f.To), len(f.CC))
	seen := map[string]bool{}
	for _, p := range all {
		switch {
		case !f.AccountPeople[p]:
			b.Verdict, b.Why = Fail, "a recipient is not a person of this account"
			return b
		case f.InternalOnly[p]:
			b.Verdict, b.Why = Fail, "an internal-only person is addressed on an external message"
			return b
		case seen[p]:
			b.Verdict, b.Why = Fail, "a recipient appears twice"
			return b
		}
		seen[p] = true
	}
	if len(f.To) == 0 {
		b.Verdict, b.Why = Fail, "the message has no recipient"
		b.EvidenceRefs = []string{"candidate:" + f.CandidateID}
		return b
	}
	b.Verdict, b.Why = Pass, "every recipient belongs to the account, none is internal-only, none is duplicated"
	return b
}

func sentences(body string) []string {
	var out []string
	for _, s := range strings.FieldsFunc(body, func(r rune) bool { return r == '.' || r == '\n' || r == '!' || r == '?' }) {
		if s = strings.TrimSpace(s); len(s) >= staleShingle {
			out = append(out, s)
		}
	}
	return out
}

func staleContentResult(b Result, f FinalArtifact) Result {
	b.SubGate = "no_stale_content"
	b.EvidenceRefs = []string{"candidate:" + f.CandidateID}
	b.Observed = "the final body was compared with the bodies of the candidates that were not selected"
	own := strings.ToLower(f.SelectedBody)
	final := strings.ToLower(f.Body)
	for _, other := range f.OtherBodies {
		for _, s := range sentences(other) {
			ls := strings.ToLower(s)
			if strings.Contains(final, ls) && !strings.Contains(own, ls) {
				b.Verdict, b.Why = Fail, "the final body carries a passage that exists only in a candidate that was not selected"
				return b
			}
		}
	}
	b.Verdict, b.Why = Pass, "no passage unique to a rejected candidate appears in the final body"
	return b
}

func policyResult(b Result, f FinalArtifact) Result {
	b.SubGate = "policy_gates"
	var blocked []string
	for _, e := range f.SendEvals {
		b.EvidenceRefs = append(b.EvidenceRefs, "eval:"+e.ID)
		if e.Blocking && ParseVerdict(e.Verdict) == Fail {
			blocked = append(blocked, e.EvalType)
		}
	}
	b.Observed = fmt.Sprintf("%d send-time deterministic eval(s) ran on the final artifact", len(f.SendEvals))
	switch {
	case len(f.SendEvals) == 0:
		b.Verdict, b.Why = Unknown, "no send-time evaluation of the final artifact is stored"
		b.EvidenceRefs = []string{"candidate:" + f.CandidateID}
	case len(blocked) > 0:
		b.Verdict, b.Why = Fail, "a blocking policy eval still fails: "+strings.Join(blocked, ", ")
	default:
		b.Verdict, b.Why = Pass, "no blocking policy eval fails on the final artifact"
	}
	return b
}

// d8BlockingEvals maps a deterministic D8 sub-gate to the eval catalog type whose blocking_rule lets it block a
// send (contracts/evals/eval_catalog.json). Only a sub-gate named here can ever block; a test keeps every entry in
// step with the catalog. policy_gates is absent on purpose: it restates the send-time evals, which block on their own
// catalog flag. The model judgment of D8 is absent too: its dimensions do not carry the catalog rule's conditions,
// so it warns and never blocks.
var d8BlockingEvals = map[string]string{
	"recipients":       "recipient_correctness", // recipients exist, belong to the account, none internal-only, none duplicated
	"no_stale_content": "grounding",             // a passage only a rejected candidate carries is not in the chosen candidate's evidence
}

// D8BlockingEvals returns a copy of the sub-gate to catalog-eval-type table (for the catalog parity test).
func D8BlockingEvals() map[string]string {
	out := make(map[string]string, len(d8BlockingEvals))
	for k, v := range d8BlockingEvals {
		out[k] = v
	}
	return out
}

// D8Blockers are the D8 results that refuse a send: a deterministic FAIL of a sub-gate with a catalog blocking
// rule. Every other D8 FAIL or WARN, and every unknown, is a warning that is stored and shown, never a block.
func D8Blockers(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if _, ok := d8BlockingEvals[r.SubGate]; ok && r.Gate == "D8" && r.Grader.Kind == Deterministic.Kind && r.Verdict == Fail {
			out = append(out, r)
		}
	}
	return out
}

func prefixed(p string, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, p+id)
	}
	return out
}
