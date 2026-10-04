// Package signalhistory answers "which signals are open as of T for this account or opportunity", the
// question the knowledge matcher (HAR-118) asks. A signal fired in an earlier diff must still be
// visible later (K17 needs new_stakeholder_entered although the latest diff is another email), so
// signals are persisted with occurred_at and a window (ADR-0015) and queried by time, not read from
// the latest diff alone.
//
// OpenAsOf is pure: the Postgres store (internal/signalstore) and the benchmark's in-memory history
// both load Records and call it, so there is one definition of "open as of". It pre-filters by time and
// window only; whether a STANDING signal still holds is decided by the matcher against the AccountState
// as of the same T (ADR-0011).
package signalhistory

import (
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Record is one persisted signal as the query needs it.
type Record struct {
	ID             string
	Type           string
	OpportunityID  string // "" for an account-level signal
	SubjectClaimID string
	SubjectItemKey string // claims.ItemKey of the reported item's text, when resolved
	Details        map[string]any
	EvidenceRefs   []knowledge.EvidenceRef
	OccurredAt     time.Time
	ExpiresAt      *time.Time // nil for STANDING signals
}

// OpenAsOf returns the records that may be open at t for opportunityID ("" means no opportunity: only
// account-level records are returned). A record is a candidate when it had occurred by t, was not yet
// past its window at t, and belongs to the opportunity or to the account. Records are returned oldest
// first, as matcher signals timed at their occurred_at.
func OpenAsOf(records []Record, opportunityID string, t time.Time) []knowledge.Signal {
	var out []knowledge.Signal
	for _, r := range records {
		if r.OccurredAt.After(t) || (r.ExpiresAt != nil && t.After(*r.ExpiresAt)) {
			continue
		}
		if r.OpportunityID != "" && r.OpportunityID != opportunityID {
			continue
		}
		out = append(out, signalOf(r))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func signalOf(r Record) knowledge.Signal {
	s := knowledge.Signal{ID: r.ID, Type: r.Type, SubjectItemKey: r.SubjectItemKey, Details: r.Details,
		EvidenceRefs: append([]knowledge.EvidenceRef(nil), r.EvidenceRefs...), CreatedAt: r.OccurredAt}
	if r.OpportunityID != "" {
		opp := r.OpportunityID
		s.OpportunityID = &opp
	}
	if r.SubjectClaimID != "" {
		claim := r.SubjectClaimID
		s.SubjectClaimID = &claim
	}
	return s
}
