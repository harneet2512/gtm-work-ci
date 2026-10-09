package knowledge

import (
	"errors"
	"fmt"
)

// Entry is one decision_guidance.supporting_knowledge item (decision_guidance.v1.json).
type Entry struct {
	KnowledgeID         string           `json:"knowledge_id"`
	Applies             bool             `json:"applies"`
	MatchedConditions   []string         `json:"matched_conditions"`
	UnmatchedConditions []string         `json:"unmatched_conditions"`
	ExceptionsChecked   []ExceptionCheck `json:"exceptions_checked"`
	// CurrentEvidenceRefs is the evidence behind the matched conditions: the state fields and open
	// signals that make the knowledge apply now (decision_guidance.v1.json, ADR-0013). Set only on
	// APPLIES entries: for knowledge that does not apply it would cite evidence for a conclusion not drawn.
	CurrentEvidenceRefs []EvidenceRef `json:"current_evidence_refs,omitempty"`
	// Similarity is set only on learned knowledge offered by closest match (ADR-0013 amendment 2).
	Similarity *EntrySimilarity `json:"similarity,omitempty"`
}

// ExceptionCheck records one exception and whether it fired, with the evidence when it did.
type ExceptionCheck struct {
	Exception    string        `json:"exception"`
	Triggered    bool          `json:"triggered"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// Result is the matcher's verdict on one knowledge object in one situation.
type Result struct {
	Label string // LabelApplies, LabelDoesNotApply or LabelExceptionTriggered
	Entry Entry
	// Similarity is the closest-match record of knowledge matched by similarity (RetrieveAll); nil otherwise.
	Similarity *Candidate
}

// ErrInvalidKnowledge is returned for knowledge that cannot be evaluated (no id, empty signature).
var ErrInvalidKnowledge = errors.New("invalid knowledge")

// Match evaluates one knowledge object against one situation (signature first, ADR-0011/0013).
func Match(k Knowledge, s Situation) (Result, error) {
	return newEnv(s).match(k)
}

// MatchAll evaluates every knowledge object against the same situation (open signals computed once).
// It stops at the first invalid knowledge object.
func MatchAll(ks []Knowledge, s Situation) ([]Result, error) {
	e := newEnv(s)
	out := make([]Result, 0, len(ks))
	for _, k := range ks {
		r, err := e.match(k)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Validate checks every condition of a knowledge object (signature, applicability conditions and
// exceptions) without evaluating it, so a malformed exception is reported even when the signature fails.
func Validate(k Knowledge) error {
	if k.ID == "" || len(k.SituationSignature) == 0 {
		return fmt.Errorf("%w: knowledge %q needs an id and a non-empty situation_signature", ErrInvalidKnowledge, k.ID)
	}
	fields := stateFieldNames()
	conds := append(append([]Condition(nil), k.SituationSignature...), k.ApplicabilityConditions...)
	for _, x := range k.Exceptions {
		if len(x.Conditions) == 0 {
			return fmt.Errorf("%w: knowledge %s exception %q has no conditions", ErrInvalidKnowledge, k.ID, x.Description)
		}
		conds = append(conds, x.Conditions...)
	}
	for _, c := range conds {
		ns, _, err := namespaceOf(c.Field, fields)
		if err == nil {
			err = validate(c, ns)
		}
		if err != nil {
			return fmt.Errorf("knowledge %s: %w", k.ID, err)
		}
	}
	return nil
}

func (e env) match(k Knowledge) (Result, error) {
	if err := Validate(k); err != nil {
		return Result{}, err
	}
	entry := Entry{KnowledgeID: k.ID, MatchedConditions: []string{}, UnmatchedConditions: []string{}, ExceptionsChecked: []ExceptionCheck{}}
	conds := append(append([]Condition(nil), k.SituationSignature...), k.ApplicabilityConditions...)
	var current []EvidenceRef
	for _, c := range conds {
		ok, refs, err := e.holds(c)
		if err != nil {
			return Result{}, fmt.Errorf("knowledge %s: %w", k.ID, err)
		}
		if ok {
			entry.MatchedConditions = append(entry.MatchedConditions, render(c))
			current = append(current, refs...)
		} else {
			entry.UnmatchedConditions = append(entry.UnmatchedConditions, render(c))
		}
	}
	if len(entry.UnmatchedConditions) > 0 {
		return Result{Label: LabelDoesNotApply, Entry: entry}, nil
	}
	triggered := false
	for _, x := range k.Exceptions {
		check, err := e.checkException(x)
		if err != nil {
			return Result{}, fmt.Errorf("knowledge %s exception %q: %w", k.ID, x.Description, err)
		}
		triggered = triggered || check.Triggered
		entry.ExceptionsChecked = append(entry.ExceptionsChecked, check)
	}
	if triggered {
		return Result{Label: LabelExceptionTriggered, Entry: entry}, nil
	}
	entry.Applies = true
	entry.CurrentEvidenceRefs = dedupeRefs(current)
	return Result{Label: LabelApplies, Entry: entry}, nil
}

// checkException: an exception fires when all of its conditions hold (conditions already validated).
func (e env) checkException(x Exception) (ExceptionCheck, error) {
	all := true
	var refs []EvidenceRef
	for _, c := range x.Conditions {
		ok, r, err := e.holds(c)
		if err != nil {
			return ExceptionCheck{}, err
		}
		all = all && ok
		refs = append(refs, r...)
	}
	check := ExceptionCheck{Exception: x.Description, Triggered: all}
	if all {
		check.EvidenceRefs = dedupeRefs(refs)
	}
	return check, nil
}

func dedupeRefs(in []EvidenceRef) []EvidenceRef {
	seen := map[EvidenceRef]bool{}
	var out []EvidenceRef
	for _, r := range in {
		if r.ActivityID != "" && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
