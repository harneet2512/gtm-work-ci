// Package signals turns a material StateDiff, the activities behind it and the field contradictions
// of a recompute into Signals (contracts/schemas/signal.v1.json): decision-relevant candidates for
// trigger evaluation, never actions. The rule table is data (Table); every rule is deterministic
// and pure, so the same inputs always emit the same signals with the same idempotency keys.
//
// Not derivable today (documented gaps, HAR-106): product_usage_increased (a ProductUsageChanged
// activity carries no direction), and a clock-driven commitment_overdue (the reducer marks a commitment
// overdue only when a recompute runs after its due date, so a quiet account never flips; sig.commitment_overdue@1
// reports the transition when a recompute does flip it). support_risk_spike reports relationship_risk turning high.
package signals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

// EventWindow is how long an EVENT signal stays open (signal.v1.json eventWindowDays, ADR-0011).
const EventWindow = 14 * 24 * time.Hour

// EventTypes mirrors signal.v1.json#/$defs/eventSignalType (parity-tested): these signals have a window.
var EventTypes = map[string]bool{
	"new_stakeholder_entered": true, "champion_weakened": true, "champion_reactivated": true, "champion_delegated": true,
	"blocker_resolved": true, "pricing_interest": true, "expansion_interest": true, "customer_replied": true,
	"meeting_accepted": true, "product_usage_increased": true, "stage_regressed": true, "stage_advanced": true,
}

// Signal is one emitted signal (signal.v1.json without id, account, opportunity and created_at,
// which the store assigns). Key discriminates signals of one rule within one diff.
type Signal struct {
	Type            string
	Rule            string
	SubjectPersonID string
	SubjectClaimID  string
	// OpportunityID tags a signal that is about one particular deal other than the batch's scope (a deal that
	// closed is no longer the primary the batch is scoped to). Empty = the batch's scope.
	OpportunityID string
	Details       map[string]any
	EvidenceRefs  []reducer.EvidenceRef
	OccurredAt    time.Time
	ExpiresAt     *time.Time
	DedupeKey     string
	Key           string
	// PerActivity keys the signal on its activity rather than on the state version, so an activity folded
	// into two recomputes still yields one signal.
	PerActivity bool
}

// ActivityFact is the slice of an activity the activity rules read. FromCustomer and FromRep say who
// the activity came from (the caller resolves people; see IsCustomerIdentity).
type ActivityFact struct {
	ID           string
	Type         string
	OccurredAt   time.Time
	FromCustomer bool
	FromRep      bool
}

// Input is everything the rules read; nothing else (no clock, no database) influences the output.
type Input struct {
	Prev       *reducer.AccountState // nil for an account's first state
	Next       reducer.AccountState
	Diff       statediff.Diff
	Activities []ActivityFact // the activities folded into Next since Prev
	Conflicts  []claims.Conflict
}

// Rule is one row of the rule table.
type Rule struct {
	ID    string   // "sig.<name>@<version>", stored in signals.rule
	Emits []string // the signal types the rule can emit
	Apply func(Input) []Signal
	// FromState marks a rule that reads the state change: it runs only when the diff is material.
	// The others read activities or contradictions and run on every recompute.
	FromState bool
}

// Table is the signal rule table. Add a row to add a signal; bump the @version when a rule changes meaning.
var Table = []Rule{
	{"sig.new_stakeholder@1", []string{"new_stakeholder_entered"}, newStakeholder, true},
	{"sig.champion_status@1", []string{"champion_weakened", "champion_reactivated", "champion_delegated"}, championStatus, true},
	{"sig.security_blocker@1", []string{"security_blocker_appeared"}, securityBlocker, true},
	{"sig.blocker_resolved@1", []string{"blocker_resolved"}, blockerResolved, true},
	{"sig.expansion_interest@1", []string{"expansion_interest"}, expansionInterest, true},
	{"sig.pricing_interest@1", []string{"pricing_interest"}, pricingInterest, true},
	{"sig.stakeholder_gap@1", []string{"stakeholder_gap"}, stakeholderGap, true},
	{"sig.next_meeting_missing@1", []string{"next_meeting_missing"}, nextMeetingMissing, true},
	{"sig.commitment_overdue@1", []string{"commitment_overdue"}, commitmentOverdue, true},
	{"sig.stage_moved@1", []string{"stage_advanced", "stage_regressed"}, stageMoved, true},
	{"sig.deal_closed@1", []string{"stage_advanced", "stage_regressed"}, dealClosed, false},
	{"sig.support_risk@1", []string{"support_risk_spike"}, supportRisk, true},
	{"sig.field_contradicted@1", []string{"field_contradicted"}, fieldContradicted, false},
	{"sig.customer_replied@1", []string{"customer_replied"}, customerReplied, false},
	{"sig.meeting_accepted@1", []string{"meeting_accepted"}, meetingAccepted, false},
	{"sig.customer_silence@1", []string{"customer_went_silent"}, customerSilence, false},
}

// Evaluate runs every rule and returns the signals in a stable order, each with its occurrence time,
// window and idempotency key filled in.
//
// When the primary opportunity changed between Prev and Next, the deal-scoped fields describe two different
// deals; the state rules then see them as unchanged (otherwise an email on another open deal reads as a stage or
// champion change, ADR-0016). A deal that closed is reported by sig.deal_closed instead, from the per-deal summaries.
func Evaluate(in Input) []Signal {
	var out []Signal
	state := sameDealView(in)
	for _, r := range Table {
		if r.FromState && !in.Diff.IsMaterial {
			continue
		}
		ri := in
		if r.FromState {
			ri = state
		}
		for _, s := range r.Apply(ri) {
			s.Rule = r.ID
			out = append(out, finalize(in, s))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].DedupeKey < out[j].DedupeKey
	})
	return out
}

func finalize(in Input, s Signal) Signal {
	if s.OccurredAt.IsZero() {
		s.OccurredAt = latest(s.EvidenceRefs, in)
	}
	s.OccurredAt = s.OccurredAt.UTC()
	s.ExpiresAt = expiry(s.Type, s.OccurredAt)
	if s.DedupeKey == "" {
		if s.PerActivity {
			s.DedupeKey = fmt.Sprintf("activity:%s:%s:%s", in.Next.AccountID, s.Rule, keyHash(s.Key))
		} else {
			s.DedupeKey = fmt.Sprintf("diff:%s:v%d:%s:%s", in.Next.AccountID, in.Next.Version, s.Rule, keyHash(s.Key))
		}
	}
	return s
}

// keyHash bounds a free-text discriminator (a blocker's text has no length limit) so a dedupe key always
// fits the 300-character column; the readable text stays in the signal's details.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// expiry is the end of the EVENT window; STANDING signals have none (ADR-0011, ADR-0015).
func expiry(signalType string, occurredAt time.Time) *time.Time {
	if !EventTypes[signalType] {
		return nil
	}
	end := occurredAt.Add(EventWindow)
	return &end
}

// latest is the newest evidence time, else the newest folded activity, else the state's as-of.
func latest(refs []reducer.EvidenceRef, in Input) time.Time {
	var t time.Time
	for _, r := range refs {
		if r.OccurredAt.After(t) {
			t = r.OccurredAt
		}
	}
	if !t.IsZero() {
		return t
	}
	for _, a := range in.Activities {
		if a.OccurredAt.After(t) {
			t = a.OccurredAt
		}
	}
	if t.IsZero() {
		t = in.Next.AsOf
	}
	return t
}

// IsCustomerIdentity reports whether a raw identity (an email address) belongs to a customer: any
// address outside ownDomain. Unresolved call speakers and non-addresses are not customers.
func IsCustomerIdentity(raw, ownDomain string) bool {
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(raw[at+1:])
	own := strings.ToLower(ownDomain)
	return domain != "" && domain != own && !strings.HasSuffix(domain, "."+own)
}
