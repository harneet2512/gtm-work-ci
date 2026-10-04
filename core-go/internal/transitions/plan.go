package transitions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CloseReasonStale is the only reason a transition is closed without being confirmed or rejected.
const CloseReasonStale = "stale"

// Record is one StateTransition (contracts/schemas/state_transition.v1.json; table state_transitions).
type Record struct {
	ID                 string       `json:"id"`
	AccountID          string       `json:"account_id"`
	OpportunityID      *string      `json:"opportunity_id"`
	FromState          string       `json:"from_state"`
	ToStateCandidate   *string      `json:"to_state_candidate"`
	Status             string       `json:"status"`
	TriggerActivityIDs []string     `json:"trigger_activity_ids"`
	SupportingFacts    []FactResult `json:"supporting_facts"`
	MissingFacts       []FactResult `json:"missing_facts"`
	ContradictingFacts []FactResult `json:"contradicting_facts"`
	Confidence         float64      `json:"confidence"`
	StateVersion       int          `json:"state_version"`
	RuleSetVersion     string       `json:"rule_set_version"`
	FirstObservedAt    time.Time    `json:"first_observed_at"`
	ConfirmedAt        *time.Time   `json:"confirmed_at"`
	RejectedAt         *time.Time   `json:"rejected_at"`
	ClosedAt           *time.Time   `json:"closed_at"`
	CloseReason        *string      `json:"close_reason"`
	LastUpdatedAt      time.Time    `json:"last_updated_at"`
}

// Open reports whether the record is the account's open transition (CANDIDATE or UNRESOLVED, not closed).
func (r Record) Open() bool {
	return (r.Status == StatusCandidate || r.Status == StatusUnresolved) && r.ClosedAt == nil
}

// AsOpen is the slice of an open record the evaluation steps need.
func (r Record) AsOpen() *Open {
	o := &Open{Status: r.Status, LastUpdatedAt: r.LastUpdatedAt}
	if r.ToStateCandidate != nil {
		o.ToStateCandidate = *r.ToStateCandidate
	}
	return o
}

// Meta is what a plan needs to know about the evaluated AccountState version.
type Meta struct {
	AccountID          string
	OpportunityID      *string
	CurrentState       string // the account's CONFIRMED relationship state ("unknown" before any)
	StateVersion       int
	RuleSetVersion     string
	AsOf               time.Time // the version's as_of: the evaluation time
	ComputedAt         time.Time
	TriggerActivityIDs []string
}

// ErrUnevidenced: a satisfied fact or contradiction cites no evidence, which the contract forbids.
var ErrUnevidenced = errors.New("transitions: a satisfied fact has no evidence")

// Plan turns an evaluation outcome into the record to write. prev is the account's open transition
// (nil when none). changed is false when nothing the record says is different, so an unchanged
// evaluation writes no history. Identity (id, account, from_state, first observation) is kept.
func Plan(prev *Record, out Outcome, m Meta) (next *Record, changed bool, err error) {
	if out.None() {
		return nil, false, nil
	}
	if err := checkEvidence(out); err != nil {
		return nil, false, err
	}
	rec := base(prev, m)
	rec.Status, rec.Confidence = out.Status, out.Confidence
	rec.SupportingFacts, rec.MissingFacts, rec.ContradictingFacts = nonNil(out.Supporting), nonNil(out.Missing), nonNil(out.Contradicting)
	if out.ToState != "" {
		to := out.ToState
		rec.ToStateCandidate = &to
	}
	at := later(m.AsOf, rec.FirstObservedAt)
	if prev == nil || prev.Status != out.Status || !ptrEqual(prev.ToStateCandidate, rec.ToStateCandidate) || out.Closed {
		rec.LastUpdatedAt = later(at, rec.LastUpdatedAt) // only a change of status, target or closure restarts the stale clock
	}
	switch out.Status {
	case StatusConfirmed:
		confirmed := later(m.AsOf, rec.FirstObservedAt)
		if out.ConfirmedAt != nil {
			confirmed = later(*out.ConfirmedAt, rec.FirstObservedAt)
		}
		rec.ConfirmedAt = &confirmed
	case StatusRejected:
		rec.RejectedAt = &at
	case StatusUnresolved:
		if out.Closed {
			reason := CloseReasonStale
			rec.ClosedAt, rec.CloseReason = &at, &reason
		}
	}
	if prev != nil && sameContent(*prev, rec) {
		return nil, false, nil
	}
	return &rec, true, nil
}

// base starts the next record from the open one (keeping identity) or a fresh observation.
func base(prev *Record, m Meta) Record {
	if prev != nil {
		r := *prev
		r.ToStateCandidate, r.ConfirmedAt, r.RejectedAt, r.ClosedAt, r.CloseReason = nil, nil, nil, nil, nil
		r.StateVersion, r.RuleSetVersion, r.TriggerActivityIDs = m.StateVersion, m.RuleSetVersion, m.TriggerActivityIDs
		return r
	}
	return Record{AccountID: m.AccountID, OpportunityID: m.OpportunityID, FromState: m.CurrentState, StateVersion: m.StateVersion,
		RuleSetVersion: m.RuleSetVersion, TriggerActivityIDs: m.TriggerActivityIDs, FirstObservedAt: m.AsOf}
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func nonNil(f []FactResult) []FactResult {
	if f == nil {
		return []FactResult{}
	}
	return f
}

func checkEvidence(out Outcome) error {
	for _, group := range [][]FactResult{out.Supporting, out.Contradicting} {
		for _, f := range group {
			if f.Satisfied && len(f.EvidenceRefs) == 0 {
				return fmt.Errorf("%w: %s", ErrUnevidenced, f.Key)
			}
		}
	}
	return nil
}

// sameContent compares what an evaluation decides: status, target, confidence, facts and closure.
func sameContent(a, b Record) bool {
	return a.Status == b.Status && ptrEqual(a.ToStateCandidate, b.ToStateCandidate) && a.Confidence == b.Confidence &&
		ptrEqual(a.CloseReason, b.CloseReason) && a.RuleSetVersion == b.RuleSetVersion &&
		sameJSON(a.SupportingFacts, b.SupportingFacts) && sameJSON(a.MissingFacts, b.MissingFacts) &&
		sameJSON(a.ContradictingFacts, b.ContradictingFacts)
}

func ptrEqual(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameJSON(a, b any) bool {
	x, errA := json.Marshal(a)
	y, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
