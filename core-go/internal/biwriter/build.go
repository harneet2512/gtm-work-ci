package biwriter

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// maxClaimEvidence bounds the evidence refs one claim carries (the surfaces show a few and link the rest).
const maxClaimEvidence = 20

// Build restates the facts as an AccountChange and, when the diff is material, a BusinessIntelligenceUpdate.
// The result is a pure function of its arguments: the same facts, ids and time always give the same documents.
// Build fails instead of returning anything Validate would reject, so an uncited claim cannot be written.
func Build(f Facts, ids IDs, now time.Time) (Result, error) {
	if err := checkInputs(f, ids); err != nil {
		return Result{}, err
	}
	claims, err := buildClaims(f)
	if err != nil {
		return Result{}, err
	}
	switch {
	case f.Diff.IsMaterial && len(claims) == 0:
		return Result{}, errors.New("biwriter: the state diff is material but lists no material change")
	case !f.Diff.IsMaterial && len(claims) > 0:
		return Result{}, errors.New("biwriter: the state diff is not material but lists a material change")
	}
	change := buildChange(f, ids, now, claims)
	res := Result{Change: change}
	if f.Diff.IsMaterial {
		res.BI = buildBI(f, ids, now, claims, change)
	}
	if err := Validate(f, res); err != nil {
		return Result{}, fmt.Errorf("biwriter: built an invalid result: %w", err)
	}
	return res, nil
}

func checkInputs(f Facts, ids IDs) error {
	missing := func(name string) error { return fmt.Errorf("biwriter: %s is required", name) }
	switch {
	case ids.Change == "" || ids.BI == "":
		return missing("the ids of the change and the update")
	case f.AccountID == "":
		return missing("the account id")
	case f.HeldOutEventID == "":
		return missing("the held-out event id")
	case f.Activity.ID == "":
		return missing("the trigger activity (a change without one cannot be cited)")
	case f.Diff.ID == "":
		return missing("the state diff (the recompute has not written it)")
	case f.Diff.ToVersion <= f.Diff.FromVersion:
		return fmt.Errorf("biwriter: state diff versions %d -> %d do not increase", f.Diff.FromVersion, f.Diff.ToVersion)
	case f.Graph.SourceEventID == "" || len(f.Graph.JobIDs) == 0:
		return errors.New("biwriter: the graph diff of the event is not projected yet")
	}
	return nil
}

// buildClaims is one claim per material diff entry, in diff order.
func buildClaims(f Facts) ([]Claim, error) {
	var out []Claim
	for _, e := range f.Diff.Entries {
		if !e.Material {
			continue
		}
		dim := DimensionOf(e.Field)
		if dim == "" {
			return nil, fmt.Errorf("biwriter: material diff field %q has no change dimension; classify it in statements.go", e.Field)
		}
		name := e.Field
		refs, own := evidenceFor(f, e)
		stmt := statement(e, f.People)
		if !own {
			stmt = labelSelfCited(stmt)
		}
		out = append(out, Claim{
			Statement: stmt, Dimension: dim, StateDiffField: &name,
			EvidenceRefs: refs, GraphDiffItems: graphItemsFor(dim, activitySet(refs), f.Graph.Items),
		})
	}
	return out, nil
}

// evidenceFor cites a diff entry: the evidence the diff carries; for a transition entry (which carries none)
// the supporting evidence of the transition itself; and, failing both, the activity that caused the diff,
// without an invented quote. The second result is false for that last case: the claim has no evidence of its own
// and rests on the triggering event itself, which its statement must say (labelSelfCited).
func evidenceFor(f Facts, e DiffEntry) (refs []EvidenceRef, own bool) {
	refs = dedupe(e.EvidenceRefs)
	if len(refs) == 0 && (e.Field == fieldRelationshipState || e.Field == fieldOpenTransition) && f.Transition != nil {
		for _, fact := range f.Transition.Supporting {
			refs = append(refs, fact.Evidence...)
		}
		refs = dedupe(refs)
	}
	if len(refs) == 0 {
		return []EvidenceRef{triggerRef(f)}, false
	}
	if len(refs) > maxClaimEvidence {
		refs = refs[:maxClaimEvidence]
	}
	return refs, true
}

// selfCitedLabel marks a claim that cites only the event that caused the diff: there is no quote behind it, and
// the statement must not read as if there were.
const selfCitedLabel = " (No quote of its own: this rests on the triggering event itself.)"

func labelSelfCited(statement string) string {
	return clip(statement, maxStatement-len([]rune(selfCitedLabel))) + selfCitedLabel
}

func triggerRef(f Facts) EvidenceRef {
	at := f.Activity.OccurredAt
	ref := EvidenceRef{ActivityID: f.Activity.ID}
	if !at.IsZero() {
		ref.OccurredAt = &at
	}
	return ref
}

// dedupe drops repeated refs (same activity, claim and quote), keeping the first of each.
func dedupe(in []EvidenceRef) []EvidenceRef {
	type key struct{ activity, claim, quote string }
	seen := map[key]bool{}
	out := make([]EvidenceRef, 0, len(in))
	for _, r := range in {
		k := key{r.ActivityID, r.ClaimID, r.Quote}
		if r.ActivityID == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

func activitySet(refs []EvidenceRef) map[string]bool {
	out := make(map[string]bool, len(refs))
	for _, r := range refs {
		out[r.ActivityID] = true
	}
	return out
}

func buildChange(f Facts, ids IDs, now time.Time, claims []Claim) AccountChange {
	var evidence []EvidenceRef
	for _, c := range claims {
		evidence = append(evidence, c.EvidenceRefs...)
	}
	evidence = dedupe(evidence)
	if len(evidence) == 0 {
		evidence = []EvidenceRef{triggerRef(f)}
	}
	held := f.HeldOutEventID
	return AccountChange{
		ID: ids.Change, AccountID: f.AccountID, OpportunityID: optional(f.OpportunityID), HeldOutEventID: &held,
		TriggerActivityIDs: []string{f.Activity.ID},
		PreviousStateRef:   StateRef{AccountID: f.AccountID, Version: f.Diff.FromVersion},
		CurrentStateRef:    StateRef{AccountID: f.AccountID, Version: f.Diff.ToVersion},
		StateDiffID:        f.Diff.ID,
		GraphDiffRef:       graphRef(f.Graph),
		MaterialChange:     f.Diff.IsMaterial,
		EvidenceRefs:       evidence,
		CreatedAt:          now.UTC(),
	}
}

// graphRef spans the account's projection jobs that touched the event: from the account's previous job (before the
// first one), to the last.
func graphRef(g GraphDiff) GraphDiffRef {
	return GraphDiffRef{ID: g.SourceEventID, FromProjectionSeq: g.PrevJobID, ToProjectionSeq: slices.Max(g.JobIDs)}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func buildBI(f Facts, ids IDs, now time.Time, claims []Claim, change AccountChange) *BI {
	return &BI{
		ID: ids.BI, AccountID: f.AccountID, OpportunityID: optional(f.OpportunityID), AccountChangeID: change.ID,
		Summary: summary(f, claims), Claims: claims, WhyItMatters: whyItMatters(f, claims),
		// TODO(HAR-117): knowledge_refs stays empty until the knowledge applicability check of the run orchestrator
		// can be called from the writer; nothing here may invent a reference (see docs/traceability/wp26.md).
		KnowledgeRefs: []string{},
		AccountMapRef: AccountMapRef{StateRef: change.CurrentStateRef, GraphDiffRef: change.GraphDiffRef},
		Transition:    transitionRef(f.Transition),
		CreatedAt:     now.UTC(),
	}
}

func transitionRef(t *Transition) *TransitionRef {
	if t == nil {
		return nil
	}
	missing := make([]MissingFact, 0, len(t.Missing))
	for _, m := range t.Missing {
		missing = append(missing, MissingFact{Key: m.Key, Description: m.Description, Required: m.Required})
	}
	return &TransitionRef{StateTransitionID: t.ID, Status: t.Status, FromState: t.FromState, ToStateCandidate: t.ToState,
		MissingFacts: missing, TouchedByEvent: t.Touched}
}
