package recompute

import (
	"fmt"
	"time"
)

// judged is one eval result: an old one from the candidate's bundle, or a new one from the send-time batch.
type judged struct{ ID, EvalType, Kind, Verdict string }

// literalEdit is one literal change as stored (human_delta.literal_changes / human_strategy_decisions.edits).
type literalEdit struct {
	Kind          string
	Before, After any
}

// facts is everything the derivation reads, already loaded: it is pure data in, so the rules are testable
// without a database.
type facts struct {
	RunID, EpisodeID, AccountID string
	DecisionID, DeltaID         string // the human strategy decision and the HumanDelta; "" when absent
	Labels                      []string
	Decided                     bool   // a candidate was chosen
	SendDecision                string // pending | send | discard
	Edits                       []literalEdit
	CandidateID                 string
	Ranking                     int
	KnowledgeIDs                []string
	Old, New                    []judged // the bundle's results of the chosen candidate; the linked send-time batch
	Linked                      bool     // the send-time batch is linked to the decision
	StateBefore, StateAfter     *int     // the version the run read; the version the send-time evaluation stored
	HashBefore, HashAfter       string   // the digests of those two states (empty when unknown)
	Now                         time.Time
}

func str(s string) *string { return &s }

func fieldPtr(f Field) *Field { return &f }

func ref(kind RefKind, id string, field *Field, label, reason string) SpanRef {
	r := SpanRef{Kind: kind, Field: field, Label: label, Reason: reason}
	if id != "" {
		r.RefID = &id
	}
	return r
}

func evalRef(j judged, field *Field, reason string, replaces *string) SpanRef {
	r := ref(EvalResult, j.ID, field, j.EvalType, reason)
	r.Verdict, r.Replaces = str(j.Verdict), replaces
	return r
}

// derive computes the invalidation from the facts. It never returns more than the facts prove.
func derive(f facts) (Invalidation, error) {
	if !f.Decided || f.SendDecision != "send" {
		f.StateAfter, f.HashAfter = nil, "" // only a send evaluates the final artifact: nothing else has a state to compare
	}
	status := statusOf(f)
	doc := Invalidation{RunID: f.RunID, DecisionEpisodeID: f.EpisodeID, Status: status, Edited: len(f.Edits) > 0 && status != NotDecided,
		SemanticLabels: append([]string{}, f.Labels...), Entries: []Entry{}, PreservedOverall: []SpanRef{}, GeneratedAt: f.Now.UTC()}
	if f.DeltaID != "" {
		doc.HumanDeltaID = str(f.DeltaID)
	}
	if status == Unedited || status == NotDecided {
		doc.Edited = false
	}
	doc.AccountState = StateRef{AccountID: f.AccountID, VersionBefore: f.StateBefore, VersionAfter: f.StateAfter, Preserved: statePreserved(f)}
	state := stateRefs(f, doc.AccountState.Preserved)

	switch status {
	case NotDecided:
		doc.PreservedOverall = state
		return doc, nil
	case Unedited, Discarded:
		doc.PreservedOverall = unchanged(f, state)
		return doc, nil
	}

	undeclared := map[string]string{}
	if status == Reevaluated {
		undeclared = undeclaredDependencies(f)
	}
	invalidatedBy := map[string]bool{} // old eval ids some entry invalidated
	for i, e := range f.Edits {
		field, class, err := classify(e.Kind)
		if err != nil {
			return Invalidation{}, err
		}
		entry := entryFor(f, status, i, Edit{Field: field, Kind: e.Kind, Before: e.Before, After: e.After, SemanticClass: class}, state, undeclared)
		for _, r := range entry.Invalidated {
			if r.Kind == EvalResult && r.RefID != nil {
				invalidatedBy[*r.RefID] = true
			}
		}
		doc.Entries = append(doc.Entries, entry)
	}
	doc.PreservedOverall = state
	for _, o := range f.Old {
		if !invalidatedBy[o.ID] {
			doc.PreservedOverall = append(doc.PreservedOverall, evalRef(o, nil, "No edit touches a field this eval reads.", nil))
		}
	}
	return doc, nil
}

func statusOf(f facts) Status {
	switch {
	case !f.Decided:
		return NotDecided
	case f.SendDecision == "discard":
		return Discarded
	case len(f.Edits) == 0:
		return Unedited
	case f.SendDecision != "send":
		return EditsPend
	case f.Linked:
		return Reevaluated
	default:
		return Unavailable
	}
}

// statePreserved proves, from stored values, whether the account state is the one the run read: nil (unknown)
// unless both versions are known; false when the versions differ; and, for equal versions, true only when both
// content digests are known and equal (a missing digest cannot prove anything).
func statePreserved(f facts) *bool {
	if f.StateBefore == nil || f.StateAfter == nil {
		return nil
	}
	same := false
	switch {
	case *f.StateBefore != *f.StateAfter:
	case f.HashBefore == "" || f.HashAfter == "":
		return nil
	default:
		same = f.HashBefore == f.HashAfter
	}
	return &same
}

// stateRefs is the account state as a preserved object, only when it is proven preserved: no proof, no claim.
func stateRefs(f facts, preserved *bool) []SpanRef {
	if preserved == nil || !*preserved {
		return []SpanRef{}
	}
	return []SpanRef{ref(AccountState, f.AccountID, nil, fmt.Sprintf("Account state v%d", *f.StateBefore),
		fmt.Sprintf("An edit never writes account state; the send-time evaluation read the same version (v%d) and the same content (sha-256) the run read.", *f.StateBefore))}
}

// unchanged is everything a run with no (used) edit keeps: the account state, every judged eval of the chosen
// candidate, its ranking rationale and its knowledge-use claims.
func unchanged(f facts, state []SpanRef) []SpanRef {
	out := append([]SpanRef{}, state...)
	for _, o := range f.Old {
		out = append(out, evalRef(o, nil, "Nothing was edited.", nil))
	}
	out = append(out, ref(RankingRat, f.CandidateID, nil, fmt.Sprintf("Why this candidate ranked %d", f.Ranking), "Nothing was edited."))
	for _, k := range f.KnowledgeIDs {
		out = append(out, ref(KnowledgeUse, k, nil, "Applies company knowledge", "Nothing was edited."))
	}
	return out
}

var fieldLabel = map[Field]string{Recipients: "Recipients of the chosen action", Subject: "Subject of the chosen action",
	Body: "Body of the chosen action", Channel: "Channel of the chosen action", Attachments: "Attachments of the chosen action"}

// undeclaredDependencies finds old results whose re-run verdict changed although no edit field is declared to
// feed them: the table was wrong or incomplete for that eval, and reporting it preserved would be a lie.
func undeclaredDependencies(f facts) map[string]string {
	out := map[string]string{}
	for _, o := range f.Old {
		declared := false
		for _, e := range f.Edits {
			if field, _, err := classify(e.Kind); err == nil && dependsOn(o.EvalType, field) {
				declared = true
			}
		}
		if declared {
			continue
		}
		if n, ok := newOf(f.New, o.EvalType, o.Kind); ok && n.Verdict != o.Verdict {
			out[o.ID] = fmt.Sprintf("Its re-run verdict changed from %s to %s although no edited field is declared to feed it (an undeclared dependency).", o.Verdict, n.Verdict)
		}
	}
	return out
}

// newOf finds the result of the same eval AND the same kind: a deterministic and a semantic result may share an
// eval type, and a result is only ever paired with its own kind.
func newOf(batch []judged, evalType, kind string) (judged, bool) {
	for _, n := range batch {
		if n.EvalType == evalType && n.Kind == kind {
			return n, true
		}
	}
	return judged{}, false
}
