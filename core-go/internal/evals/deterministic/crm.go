package deterministic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// HAR-97 §4 "CRM/writeback consistency" sub-checks.
const (
	CheckStageLegal      Check = "crm.stage_update_legal"
	CheckOwnerExists     Check = "crm.owner_exists"
	CheckNoOverwrite     Check = "crm.next_step_no_overwrite"
	CheckWritebackTarget Check = "crm.writeback_target"
)

var crmChecks = []Check{CheckStageLegal, CheckOwnerExists, CheckNoOverwrite, CheckWritebackTarget}

// CRMWriteback is the HAR-97 §4 "CRM/writeback consistency" eval. A draft that writes nothing to
// the CRM passes every check.
func CRMWriteback(in Input) Judgment {
	var f []Finding
	if hasCRMWrite(in.Draft) {
		f = append(f, StageUpdateLegal(in)...)
		f = append(f, OwnerExists(in)...)
		f = append(f, NextStepDoesNotOverwrite(in)...)
		f = append(f, WritebackTargetCorrect(in)...)
	}
	return judge(in, TypeCRM, crmChecks, []string{"stage", "owner", "next_milestone"}, f)
}

// stageKey compares stage names ignoring case, spaces and separators.
func stageKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stageIndex is the position of stage in the ordered open stages, or -1.
func stageIndex(stages []string, stage string) int {
	for i, s := range stages {
		if stageKey(s) == stageKey(stage) {
			return i
		}
	}
	return -1
}

func isTerminal(rules CRMRules, stage string) bool {
	return stageIndex(rules.TerminalStages, stage) >= 0
}

// currentStage is the deal's stage in the newest state; "" when unknown. A known stage that is not
// text is an error, not an unknown stage.
func currentStage(in Input) (string, error) {
	f := latestState(in).Fields.Stage
	if !f.Known {
		return "", nil
	}
	s, ok := f.Value.(string)
	if !ok {
		return "", fmt.Errorf("stage is a %T, not text", f.Value)
	}
	return s, nil
}

// StageUpdateLegal: the stage the draft sets is a workspace stage, moves forward by at most
// max_forward_steps, and never moves into or out of a terminal stage (human-only, ADR-0014).
func StageUpdateLegal(in Input) []Finding {
	if in.Draft.CRMNextStepIntent.StageChange == nil || strings.TrimSpace(*in.Draft.CRMNextStepIntent.StageChange) == "" {
		return nil
	}
	target := strings.TrimSpace(*in.Draft.CRMNextStepIntent.StageChange)
	from, err := currentStage(in)
	if err != nil {
		return []Finding{failure(CheckStageLegal, fmt.Sprintf("the stage state is unreadable: %v", err),
			"Repair the account state before writing the stage.").blocking().withState("stage")}
	}
	bad := func(detail, fix string) []Finding {
		return []Finding{failure(CheckStageLegal, detail, fix).blocking().withState("stage")}
	}
	ti := stageIndex(in.CRM.Stages, target)
	switch {
	case isTerminal(in.CRM, target):
		return bad(fmt.Sprintf("moves the deal to terminal stage %q, which only a human may do", target),
			"Leave the stage unchanged and let a human close the deal.")
	case from != "" && isTerminal(in.CRM, from):
		return bad(fmt.Sprintf("moves the deal out of terminal stage %q, which only a human may do", from),
			"Leave the stage unchanged; reopening a closed deal is a human decision.")
	case ti < 0:
		return bad(fmt.Sprintf("stage %q is not one of the workspace's stages", target),
			fmt.Sprintf("Use one of: %s.", strings.Join(in.CRM.Stages, ", ")))
	}
	fi := stageIndex(in.CRM.Stages, from)
	switch {
	case fi < 0 || ti == fi:
		return nil
	case ti < fi:
		return bad(fmt.Sprintf("moves the stage backward from %q to %q", from, target),
			"Do not move a deal backward; a human decides regressions.")
	case ti-fi > in.CRM.MaxForwardSteps:
		return bad(fmt.Sprintf("jumps %d stages from %q to %q; at most %d are allowed", ti-fi, from, target, in.CRM.MaxForwardSteps),
			fmt.Sprintf("Move to %q at most, or leave the stage for a human.", in.CRM.Stages[min(fi+in.CRM.MaxForwardSteps, len(in.CRM.Stages)-1)]))
	}
	return nil
}

func matchingOpportunity(in Input) (Opportunity, bool) {
	if in.OpportunityID == nil {
		return Opportunity{}, false
	}
	for _, o := range in.Opportunities {
		if o.OpportunityID == *in.OpportunityID {
			return o, true
		}
	}
	return Opportunity{}, false
}

// OwnerExists: the opportunity has an owner who is a live employee in the directory.
func OwnerExists(in Input) []Finding {
	owner := ""
	if o, ok := matchingOpportunity(in); ok && o.OwnerPersonID != nil {
		owner = *o.OwnerPersonID
	} else if f := latestState(in).Fields.Owner; f.Known {
		s, isText := f.Value.(string)
		if !isText {
			return []Finding{failure(CheckOwnerExists, fmt.Sprintf("the owner state is unreadable: owner is a %T, not text", f.Value),
				"Repair the account state before writing to the CRM.").blocking().withState("owner")}
		}
		owner = s
	}
	bad := func(detail string) []Finding {
		return []Finding{failure(CheckOwnerExists, detail, "Assign an existing owner before writing to the CRM.").blocking().withState("owner")}
	}
	if owner == "" {
		return bad("the opportunity has no owner")
	}
	p, ok := personIndex(in)[owner]
	switch {
	case !ok:
		return bad(fmt.Sprintf("owner %s is not a known person", owner))
	case p.MergedInto != nil:
		return bad(fmt.Sprintf("owner %s was merged into %s", owner, *p.MergedInto))
	case p.Kind != KindEmployee:
		return bad(fmt.Sprintf("owner %s is not an employee", p.DisplayName))
	}
	return nil
}

// draftEvidenceTime is the newest time the draft's evidence refers to, or the state's as-of time.
func draftEvidenceTime(in Input) time.Time {
	acts := activityIndex(in)
	var newest time.Time
	for _, r := range in.Draft.EvidenceRefs {
		t := time.Time{}
		if r.OccurredAt != nil {
			t = *r.OccurredAt
		} else if a, ok := acts[r.ActivityID]; ok {
			t = a.OccurredAt
		}
		if t.After(newest) {
			newest = t
		}
	}
	if newest.IsZero() {
		return in.State.AsOf
	}
	return newest
}

func sameField(a, b reducer.Field) bool {
	ja, _ := json.Marshal(a.Value)
	jb, _ := json.Marshal(b.Value)
	return string(ja) == string(jb) && a.Known == b.Known
}

// NextStepDoesNotOverwrite: a CRM write (stage or next step) must not replace a field that a
// human or the CRM set at or after the draft's evidence, nor one that changed since the draft's state.
func NextStepDoesNotOverwrite(in Input) []Finding {
	c := in.Draft.CRMNextStepIntent
	var out []Finding
	if c.StageChange != nil && strings.TrimSpace(*c.StageChange) != "" {
		out = append(out, overwrite(in, "stage", "stage")...)
	}
	if hasCRMWrite(in.Draft) && (strings.TrimSpace(c.NextStep) != "" || c.DueAt != nil) {
		out = append(out, overwrite(in, "next_milestone", "next step")...)
	}
	return out
}

func overwrite(in Input, name, what string) []Finding {
	latest, base := latestState(in), in.State
	lf, bf := *latest.Fields.Field(name), *base.Fields.Field(name)
	evidence := draftEvidenceTime(in)
	switch {
	case lf.Known && lf.Standing != nil && standingsThatOutrankAgents[*lf.Standing] && lf.AsOf != nil && !lf.AsOf.Before(evidence):
		return []Finding{failure(CheckNoOverwrite,
			fmt.Sprintf("the %s was set (%s) at %s, not before the draft's newest evidence", what, *lf.Standing, lf.AsOf.Format(day)),
			fmt.Sprintf("Keep the existing %s; the draft does not know anything newer.", what)).blocking().withState(name)}
	case in.LatestState != nil && !sameField(lf, bf):
		return []Finding{failure(CheckNoOverwrite,
			fmt.Sprintf("the %s changed after the state the draft was written from (state v%d to v%d)", what, base.Version, latest.Version),
			fmt.Sprintf("Re-run the draft against the latest state before writing the %s.", what)).blocking().withState(name)}
	}
	return nil
}

// WritebackTargetCorrect: the write targets an opportunity of this run's account, and the state
// it was based on belongs to the same account and opportunity.
func WritebackTargetCorrect(in Input) []Finding {
	bad := func(detail string) []Finding {
		return []Finding{failure(CheckWritebackTarget, detail, "Write only to this account's own opportunity.").blocking().stopping()}
	}
	if in.OpportunityID == nil {
		return bad("the CRM write has no target opportunity")
	}
	o, ok := matchingOpportunity(in)
	switch {
	case !ok:
		return bad(fmt.Sprintf("opportunity %s is not in the directory", *in.OpportunityID))
	case o.AccountID != in.AccountID:
		return bad(fmt.Sprintf("opportunity %s belongs to account %s, not the run's account %s", o.OpportunityID, o.AccountID, in.AccountID))
	case in.State.AccountID != in.AccountID:
		return bad(fmt.Sprintf("the draft's state is for account %s, not the run's account %s", in.State.AccountID, in.AccountID))
	case in.State.OpportunityID != nil && *in.State.OpportunityID != *in.OpportunityID:
		return bad(fmt.Sprintf("the draft's state is for opportunity %s, not %s", *in.State.OpportunityID, *in.OpportunityID))
	}
	return nil
}
