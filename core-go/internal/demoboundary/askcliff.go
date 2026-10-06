package demoboundary

// Ask Cliff's fixed surface (owner-approved HAR-129 contract change, 2026-10-06).
var (
	askActions    = []string{"play_next", "demo_status"}
	askTopLevel   = []string{"M1", "M2", "M3"}
	askDryRunOnly = []string{"draft_followup", "crm_update_preview"}
)

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkAskCliff returns every way the Ask Cliff section widens what the owner approved: it answers in a DM
// and in a mention thread, the channel's top level stays M1, M2 and M3, the only state-changing actions are the
// three demo actions (no email send, no CRM write), the drafts are dry runs, and the one
// extra trigger is Cliff's play_next on web Play's route behind a confirmation.
func (b *Boundary) checkAskCliff() []string {
	var problems []string
	a := b.AskCliff
	if !a.DM || !a.MentionInThread {
		problems = append(problems, "Ask Cliff must answer in a DM and in a thread under a mention")
	}
	if !sameList(a.ChannelTopLevelMessages, askTopLevel) {
		problems = append(problems, "the audience channel's top level must carry only M1, M2 and M3")
	}
	if !sameList(a.Actions, askActions) {
		problems = append(problems, "Ask Cliff's actions must be exactly play_next, demo_status: no send, no CRM write")
	}
	if !sameList(a.DryRunOnly, askDryRunOnly) {
		problems = append(problems, "draft_followup and crm_update_preview must be dry run only")
	}
	if len(b.VisibleTrigger.Also) != 1 {
		return append(problems, "the visible trigger lists web Play and exactly one Cliff trigger (play_next)")
	}
	t := b.VisibleTrigger.Also[0]
	if t.Surface != "cliff_in_slack" || t.Control != "play_next" || !t.RequiresConfirmation {
		problems = append(problems, "Cliff's play_next must be on cliff_in_slack and require a confirmation")
	}
	if t.CoreEndpoint != b.VisibleTrigger.CoreEndpoint {
		problems = append(problems, "Cliff's play_next must call the same core route as web Play")
	}
	return problems
}
