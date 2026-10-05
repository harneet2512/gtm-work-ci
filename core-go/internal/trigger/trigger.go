// Package trigger decides whether the post-interaction follow-up workflow is eligible to run after a
// recompute (contracts/schemas/trigger_evaluation.v1.json). HAR-96 decides eligibility only; HAR-97
// judges whether the proposed action is good. Every outcome carries reason codes, so "state changed
// but no action needed" is a recorded, inspectable decision (eligible=false plus a reason), never silence.
//
// Precedence, first match wins: account_unresolved, no_material_change, permission_denied,
// open_run_exists, no_relevant_signal, rep_already_replied, cooldown, champion_unknown; otherwise the
// evaluation is eligible with every eligible_* reason that holds. The function is pure.
package trigger

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

// Workflow is the one workflow HAR-96 builds (trigger_evaluation.v1.json).
const Workflow = "post_interaction_followup"

// DefaultCooldown is the minimum gap between two eligible evaluations of one account.
const DefaultCooldown = 30 * time.Minute

// Reason codes (trigger_evaluation.v1.json reason_codes; parity-tested against the schema and SQL).
const (
	EligibleCustomerReplied   = "eligible_customer_replied"
	EligibleMeetingCompleted  = "eligible_meeting_completed"
	EligibleStakeholderChange = "eligible_stakeholder_change"
	EligibleBlockerChange     = "eligible_blocker_change"
	NoMaterialChange          = "no_material_change"
	NoRelevantSignal          = "no_relevant_signal"
	OpenRunExists             = "open_run_exists"
	RepAlreadyReplied         = "rep_already_replied"
	Cooldown                  = "cooldown"
	ChampionUnknown           = "champion_unknown"
	PermissionDenied          = "permission_denied"
	AccountUnresolved         = "account_unresolved"
)

// Input is everything an evaluation reads.
type Input struct {
	AccountID        string
	Diff             statediff.Diff
	Signals          []signals.Signal
	Activities       []signals.ActivityFact // the activities folded into the diff
	OpenRun          bool                   // an unfinished run of this workflow exists for the account
	PermissionDenied bool                   // a folded activity is not visible to the workflow's audience
	ChampionKnown    bool                   // the state names a champion
	LastEligibleAt   *time.Time             // when this account last became eligible
	Now              time.Time
	Cooldown         time.Duration // zero selects DefaultCooldown
}

// Evaluation is the decision (trigger_evaluation.v1.json without id, signal ids and timestamps).
type Evaluation struct {
	Workflow    string
	Eligible    bool
	ReasonCodes []string
	Explanation string
}

var (
	stakeholderSignals = map[string]bool{
		"new_stakeholder_entered": true, "champion_delegated": true, "champion_weakened": true, "champion_reactivated": true,
	}
	blockerSignals    = map[string]bool{"security_blocker_appeared": true, "blocker_resolved": true}
	meetingActivities = map[string]bool{"MeetingCompleted": true, "TranscriptReady": true, "CallEnded": true}
)

// Evaluate decides eligibility.
func Evaluate(in Input) Evaluation {
	switch {
	case in.AccountID == "":
		return no(AccountUnresolved, "the activity is not attached to an account")
	case !in.Diff.IsMaterial:
		return no(NoMaterialChange, "state changed but no action needed: no material change in "+fieldList(in.Diff))
	case in.PermissionDenied:
		return no(PermissionDenied, "an activity behind this change is not visible to the workflow")
	case in.OpenRun:
		return no(OpenRunExists, "an unfinished run of this workflow already exists for the account")
	}
	reasons := eligibleReasons(in)
	if len(reasons) == 0 {
		return no(NoRelevantSignal, "the material change emitted no signal that calls for a follow-up")
	}
	if repLast(in.Activities) {
		return no(RepAlreadyReplied, "the rep's own email is the latest activity; nothing is waiting on a reply")
	}
	if in.LastEligibleAt != nil && in.Now.Sub(*in.LastEligibleAt) < cooldownOf(in) {
		return no(Cooldown, fmt.Sprintf("the account became eligible %s ago", in.Now.Sub(*in.LastEligibleAt).Round(time.Second)))
	}
	if !in.ChampionKnown && !contains(reasons, EligibleCustomerReplied) {
		return no(ChampionUnknown, "no champion is known and the customer did not write, so there is nobody to follow up with")
	}
	return Evaluation{Workflow: Workflow, Eligible: true, ReasonCodes: reasons,
		Explanation: "eligible: " + strings.ReplaceAll(strings.Join(reasons, ", "), "eligible_", "")}
}

func no(reason, why string) Evaluation {
	return Evaluation{Workflow: Workflow, ReasonCodes: []string{reason}, Explanation: why}
}

func cooldownOf(in Input) time.Duration {
	if in.Cooldown > 0 {
		return in.Cooldown
	}
	return DefaultCooldown
}

func eligibleReasons(in Input) []string {
	var out []string
	has := func(f func(signals.Signal) bool) bool {
		for _, s := range in.Signals {
			if f(s) {
				return true
			}
		}
		return false
	}
	if has(func(s signals.Signal) bool { return s.Type == "customer_replied" }) {
		out = append(out, EligibleCustomerReplied)
	}
	for _, a := range in.Activities {
		if meetingActivities[a.Type] {
			out = append(out, EligibleMeetingCompleted)
			break
		}
	}
	if buyingGroupChanged(in.Diff) || has(func(s signals.Signal) bool { return stakeholderSignals[s.Type] }) {
		out = append(out, EligibleStakeholderChange)
	}
	if has(func(s signals.Signal) bool { return blockerSignals[s.Type] }) {
		out = append(out, EligibleBlockerChange)
	}
	sort.Strings(out)
	return out
}

// repLast reports whether the newest folded activity is an email the rep sent.
func repLast(acts []signals.ActivityFact) bool {
	var newest *signals.ActivityFact
	for i := range acts {
		if newest == nil || !acts[i].OccurredAt.Before(newest.OccurredAt) {
			newest = &acts[i]
		}
	}
	return newest != nil && newest.FromRep && newest.Type == "EmailSent"
}

// buyingGroupChanged: the buying group changed (a member joined, left, changed role, status or delegation)
// in an account that already had one. The first state's group is a load, not a change.
func buyingGroupChanged(d statediff.Diff) bool {
	c, ok := d.Change(statediff.FieldBuyingGroup)
	return ok && c.Material && c.Op != statediff.OpSet
}

func fieldList(d statediff.Diff) string {
	if len(d.Changes) == 0 {
		return "no field"
	}
	names := make([]string, 0, len(d.Changes))
	for _, c := range d.Changes {
		names = append(names, c.Field)
	}
	return strings.Join(names, ", ")
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
