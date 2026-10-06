package knowledge

import (
	"sync/atomic"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// EventWindow is how long an EVENT signal stays open (signal.v1.json#/$defs/eventWindowDays, ADR-0011).
const EventWindow = 14 * 24 * time.Hour

// eventSignalTypes and standingSignalTypes mirror signal.v1.json#/$defs (parity-tested).
var eventSignalTypes = map[string]bool{
	"new_stakeholder_entered": true, "champion_weakened": true, "champion_reactivated": true, "champion_delegated": true,
	"blocker_resolved": true, "pricing_interest": true, "expansion_interest": true, "customer_replied": true,
	"meeting_accepted": true, "product_usage_increased": true, "stage_regressed": true, "stage_advanced": true,
}

var standingSignalTypes = map[string]bool{
	"security_blocker_appeared": true, "commitment_overdue": true, "support_risk_spike": true, "stakeholder_gap": true,
	"next_meeting_missing": true, "customer_went_silent": true, "field_contradicted": true,
}

// buyingGroupRoles mirrors account_state.v1.json buying_group roles (parity-tested).
var buyingGroupRoles = map[string]bool{
	"champion": true, "economic_buyer": true, "technical_evaluator": true, "security": true, "legal": true,
	"executive_sponsor": true, "user": true, "influencer": true, "blocker": true, "procurement": true, "unknown": true,
}

// disengagedStatuses are buying-group statuses that do not count as holding a role for "exists".
var disengagedStatuses = map[string]bool{"departed": true, "inactive": true, "disengaged": true}

func isSignalType(t string) bool { return eventSignalTypes[t] || standingSignalTypes[t] }

// openSignals returns, per signal type, the signals open for the situation's opportunity at Now.
func openSignals(s Situation) map[string][]Signal {
	open := map[string][]Signal{}
	for _, sig := range s.Signals {
		if signalOpen(sig, s) {
			open[sig.Type] = append(open[sig.Type], sig)
		}
	}
	return open
}

// SignalOpen reports whether sig is open in situation s at s.Now (ADR-0011): the transition detector
// reads open signals under exactly the rule the knowledge matcher uses.
func SignalOpen(sig Signal, s Situation) bool { return signalOpen(sig, s) }

func signalOpen(sig Signal, s Situation) bool {
	if sig.CreatedAt.After(s.Now) {
		return false
	}
	if sig.OpportunityID != nil && s.OpportunityID != "" && *sig.OpportunityID != s.OpportunityID {
		return false
	}
	if eventSignalTypes[sig.Type] {
		return !s.Now.After(sig.CreatedAt.Add(EventWindow))
	}
	return standingOpen(sig, s)
}

// standingOpen implements signal.v1.json#/$defs/standingOpenWhile, with the ADR-0013 fallbacks
// when the signal's subject cannot be resolved.
func standingOpen(sig Signal, s Situation) bool {
	switch sig.Type {
	case "security_blocker_appeared":
		return itemOpen(s.Fields["blockers"], sig, "open", "overdue")
	case "commitment_overdue":
		return itemOpen(s.Fields["current_commitments"], sig, "overdue")
	case "support_risk_spike":
		v := s.Fields["relationship_risk"]
		return v.Known && sameScalar(v.Scalar, "high")
	case "stakeholder_gap":
		return gapOpen(s.Fields["coverage_gaps"], sig)
	case "next_meeting_missing":
		return !exists(s.Fields["next_meeting"])
	case "customer_went_silent":
		return silenceOpen(s.Fields["last_customer_interaction"], sig.CreatedAt)
	case "field_contradicted":
		return contradictionOpen(s.Conflicts, sig)
	}
	return false
}

// itemOpen: the reported item (by item key, else by claim id, else any item) has one of statuses.
func itemOpen(v Value, sig Signal, statuses ...string) bool {
	for _, it := range v.Items {
		if !hasString(statuses, it.Status) {
			continue
		}
		switch {
		case sig.SubjectItemKey != "":
			if claims.ItemKey(it.Text) == sig.SubjectItemKey {
				return true
			}
		case sig.SubjectClaimID != nil:
			if it.ClaimID == *sig.SubjectClaimID {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func gapOpen(v Value, sig Signal) bool {
	gap, _ := sig.Details["gap"].(string)
	for _, it := range v.Items {
		if gap == "" || claims.ItemKey(it.Text) == claims.ItemKey(gap) {
			return true
		}
	}
	return false
}

// silenceFallbacks counts silence signals kept open only because last_customer_interaction was unknown
// or unparseable (ADR-0013 fallback), so the fallback is observable rather than silent.
var silenceFallbacks atomic.Int64

// SilenceFallbacks reports how many times the silence fallback has been used in this process.
func SilenceFallbacks() int64 { return silenceFallbacks.Load() }

func silenceOpen(v Value, createdAt time.Time) bool {
	text, ok := v.Scalar.(string)
	if !v.Known || !ok {
		silenceFallbacks.Add(1)
		return true
	}
	last, err := time.Parse(time.RFC3339, text)
	if err != nil {
		silenceFallbacks.Add(1)
		return true
	}
	return !last.After(createdAt)
}

func contradictionOpen(conflicts map[string][]string, sig Signal) bool {
	field, _ := sig.Details["field"].(string)
	claimID, _ := sig.Details["contradicting_claim_id"].(string)
	if field == "" || claimID == "" {
		for _, ids := range conflicts {
			if len(ids) > 0 {
				return true
			}
		}
		return false
	}
	return hasString(conflicts[field], claimID)
}

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
