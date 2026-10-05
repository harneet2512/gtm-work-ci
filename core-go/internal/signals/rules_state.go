package signals

import (
	"regexp"
	"slices"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// securityText marks a blocker as a security / compliance blocker.
var securityText = regexp.MustCompile(`(?i)\b(security|soc ?2|iso ?27001|pen-?tests?|pen-?testing|penetration|infosec|vulnerab\w*|gdpr|hipaa|baa|dpa|data residency|residency|compliance|encryption|sso|questionnaires?)\b`)

// pricingText marks a commercial issue as pricing interest.
var pricingText = regexp.MustCompile(`(?i)\b(pric(e|es|ing)|discount|quote|rebate|per[- ]seat|cost)\b`)

var (
	weakStatuses = map[string]bool{"weakening": true, "inactive": true, "departed": true}
	openStatuses = map[string]bool{"open": true, "overdue": true}
	doneStatuses = map[string]bool{"resolved": true, "fulfilled": true}
)

func prevOrEmpty(in Input) reducer.AccountState {
	if in.Prev == nil {
		return reducer.AccountState{}
	}
	return *in.Prev
}

func scalar(f reducer.Field) (string, bool) {
	if !f.Known {
		return "", false
	}
	s, ok := f.Value.(string)
	if !ok || s == claims.Unknown {
		return "", false
	}
	return s, true
}

func itemsOf(f reducer.Field) []reducer.Item { return f.ListItems() }

func itemByKey(items []reducer.Item) map[string]reducer.Item {
	m := make(map[string]reducer.Item, len(items))
	for _, it := range items {
		m[claims.ItemKey(it.Text)] = it
	}
	return m
}

func itemEvidence(it reducer.Item, field reducer.Field) []reducer.EvidenceRef {
	if len(it.EvidenceRefs) > 0 {
		return it.EvidenceRefs
	}
	return field.EvidenceRefs
}

// newStakeholder: a person is in the buying group now who was not before (every member of a first state).
func newStakeholder(in Input) []Signal {
	known := map[string]bool{}
	for _, m := range prevOrEmpty(in).BuyingGroup {
		known[m.PersonID] = true
	}
	var out []Signal
	for _, m := range in.Next.BuyingGroup {
		if known[m.PersonID] {
			continue
		}
		out = append(out, Signal{Type: "new_stakeholder_entered", SubjectPersonID: m.PersonID, Key: m.PersonID,
			Details: map[string]any{"roles": m.Roles, "status": m.Status}, EvidenceRefs: m.EvidenceRefs})
	}
	return out
}

// championStatus: the champion weakened, was reactivated, or delegated ownership.
func championStatus(in Input) []Signal {
	before, _ := scalar(prevOrEmpty(in).Fields.ChampionStatus)
	after, ok := scalar(in.Next.Fields.ChampionStatus)
	if !ok || before == after {
		return nil
	}
	f := in.Next.Fields.ChampionStatus
	typ := ""
	switch {
	case weakStatuses[after]:
		typ = "champion_weakened"
	case after == "delegated":
		typ = "champion_delegated"
	case after == "active" && weakStatuses[before]:
		typ = "champion_reactivated"
	default:
		return nil
	}
	return []Signal{{Type: typ, SubjectPersonID: championID(in.Next), Key: after, EvidenceRefs: f.EvidenceRefs,
		Details: map[string]any{"from": before, "to": after}}}
}

func championID(st reducer.AccountState) string {
	for _, m := range st.BuyingGroup {
		for _, r := range m.Roles {
			if r == "champion" {
				return m.PersonID
			}
		}
	}
	return ""
}

// securityBlocker: a security-related blocker is open now that was not open before.
func securityBlocker(in Input) []Signal {
	prev := itemByKey(itemsOf(prevOrEmpty(in).Fields.Blockers))
	var out []Signal
	for _, it := range itemsOf(in.Next.Fields.Blockers) {
		old, seen := prev[claims.ItemKey(it.Text)]
		if !openStatuses[it.Status] || !securityText.MatchString(it.Text) || (seen && openStatuses[old.Status]) {
			continue
		}
		out = append(out, Signal{Type: "security_blocker_appeared", SubjectClaimID: it.ClaimID, Key: claims.ItemKey(it.Text),
			Details: map[string]any{"blocker": it.Text}, EvidenceRefs: itemEvidence(it, in.Next.Fields.Blockers)})
	}
	return out
}

// blockerResolved: a blocker that was open is resolved now.
func blockerResolved(in Input) []Signal {
	prev := itemByKey(itemsOf(prevOrEmpty(in).Fields.Blockers))
	var out []Signal
	for _, it := range itemsOf(in.Next.Fields.Blockers) {
		old, seen := prev[claims.ItemKey(it.Text)]
		if !seen || !openStatuses[old.Status] || !doneStatuses[it.Status] {
			continue
		}
		out = append(out, Signal{Type: "blocker_resolved", SubjectClaimID: it.ClaimID, Key: claims.ItemKey(it.Text),
			Details: map[string]any{"blocker": it.Text}, EvidenceRefs: itemEvidence(it, in.Next.Fields.Blockers)})
	}
	return out
}

// expansionInterest: the motion became expansion.
func expansionInterest(in Input) []Signal {
	before, _ := scalar(prevOrEmpty(in).Fields.Motion)
	after, ok := scalar(in.Next.Fields.Motion)
	if !ok || before == after || !strings.EqualFold(after, "expansion") {
		return nil
	}
	return []Signal{{Type: "expansion_interest", Key: "motion", EvidenceRefs: in.Next.Fields.Motion.EvidenceRefs,
		Details: map[string]any{"from": before}}}
}

// pricingInterest: the commercial issue changed to one about price.
func pricingInterest(in Input) []Signal {
	before, _ := scalar(prevOrEmpty(in).Fields.CommercialIssue)
	after, ok := scalar(in.Next.Fields.CommercialIssue)
	if !ok || before == after || !pricingText.MatchString(after) {
		return nil
	}
	return []Signal{{Type: "pricing_interest", Key: "commercial_issue", EvidenceRefs: in.Next.Fields.CommercialIssue.EvidenceRefs,
		Details: map[string]any{"commercial_issue": after}}}
}

// stakeholderGap: a coverage gap is listed now that was not before.
func stakeholderGap(in Input) []Signal {
	had := map[string]bool{}
	for _, g := range prevOrEmpty(in).CoverageGaps {
		had[claims.ItemKey(g)] = true
	}
	var out []Signal
	for _, g := range in.Next.CoverageGaps {
		if had[claims.ItemKey(g)] {
			continue
		}
		out = append(out, Signal{Type: "stakeholder_gap", Key: claims.ItemKey(g), Details: map[string]any{"gap": g}})
	}
	return out
}

// nextMeetingMissing: a booked next meeting is gone (held, cancelled or moved to nothing).
func nextMeetingMissing(in Input) []Signal {
	before := prevOrEmpty(in).Fields.NextMeeting
	after := in.Next.Fields.NextMeeting
	if _, had := scalar(before); !had {
		return nil
	}
	if _, has := scalar(after); has {
		return nil
	}
	return []Signal{{Type: "next_meeting_missing", Key: "next_meeting", EvidenceRefs: slices.Concat(after.EvidenceRefs, before.EvidenceRefs)}}
}

// commitmentOverdue: a commitment is overdue now that was not before.
func commitmentOverdue(in Input) []Signal {
	prev := itemByKey(itemsOf(prevOrEmpty(in).Fields.CurrentCommitments))
	var out []Signal
	for _, it := range itemsOf(in.Next.Fields.CurrentCommitments) {
		old, seen := prev[claims.ItemKey(it.Text)]
		if it.Status != claims.ItemOverdue || (seen && old.Status == claims.ItemOverdue) {
			continue
		}
		out = append(out, Signal{Type: "commitment_overdue", SubjectClaimID: it.ClaimID, SubjectPersonID: it.OwnerPersonID,
			Key: claims.ItemKey(it.Text), Details: map[string]any{"commitment": it.Text}, EvidenceRefs: itemEvidence(it, in.Next.Fields.CurrentCommitments)})
	}
	return out
}

// stageMoved: the stage moved along the stage order.
func stageMoved(in Input) []Signal {
	before, hadBefore := scalar(prevOrEmpty(in).Fields.Stage)
	after, ok := scalar(in.Next.Fields.Stage)
	if !ok || !hadBefore {
		return nil
	}
	b, bok := reducer.StageRank(before)
	a, aok := reducer.StageRank(after)
	if !bok || !aok || a == b {
		return nil
	}
	typ := "stage_advanced"
	if a < b {
		typ = "stage_regressed"
	}
	return []Signal{{Type: typ, Key: after, EvidenceRefs: in.Next.Fields.Stage.EvidenceRefs,
		Details: map[string]any{"from": before, "to": after}}}
}

// sameDealView returns the input the state rules read. Normally the input itself; when the primary opportunity
// changed, a Prev whose deal-scoped fields equal Next's, so that only account-scoped changes (buying group,
// coverage gaps) remain.
func sameDealView(in Input) Input {
	if !reducer.PrimaryChanged(in.Prev, in.Next) {
		return in
	}
	prev := *in.Prev
	for _, name := range reducer.DealFieldNames() {
		*prev.Fields.Field(name) = *in.Next.Fields.Field(name)
	}
	in.Prev = &prev
	return in
}

// dealClosed: a deal reached a closed stage (Closed Won, Closed Lost, Churned ...) since the previous state. A
// closed deal is never the primary, so its stage change cannot show in the headline; this rule reads the
// per-deal summaries and tags the signal with the deal. A win is stage_advanced, anything else stage_regressed.
func dealClosed(in Input) []Signal {
	if in.Prev == nil {
		return nil
	}
	before := map[string]reducer.OpportunitySummary{}
	for _, s := range in.Prev.Opportunities {
		before[s.OpportunityID] = s
	}
	var out []Signal
	for _, s := range in.Next.Opportunities {
		b, seen := before[s.OpportunityID]
		if !seen || reducer.StageClosed(b.Stage) || !reducer.StageClosed(s.Stage) {
			continue
		}
		typ, outcome := "stage_regressed", "lost"
		if reducer.StageWon(s.Stage) {
			typ, outcome = "stage_advanced", "won"
		}
		var refs []reducer.EvidenceRef
		if s.LastActivityID != nil {
			refs = []reducer.EvidenceRef{{ActivityID: *s.LastActivityID, OccurredAt: s.AsOf}}
		}
		out = append(out, Signal{Type: typ, OpportunityID: s.OpportunityID, Key: s.OpportunityID + ":" + s.Stage, EvidenceRefs: refs,
			Details: map[string]any{"from": b.Stage, "to": s.Stage, "closed": true, "outcome": outcome}})
	}
	return out
}

// supportRisk: the relationship risk turned high.
func supportRisk(in Input) []Signal {
	before, _ := scalar(prevOrEmpty(in).Fields.RelationshipRisk)
	after, ok := scalar(in.Next.Fields.RelationshipRisk)
	if !ok || after != "high" || before == "high" {
		return nil
	}
	return []Signal{{Type: "support_risk_spike", Key: "relationship_risk", EvidenceRefs: in.Next.Fields.RelationshipRisk.EvidenceRefs,
		Details: map[string]any{"from": before}}}
}
