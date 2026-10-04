package deterministic

import (
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Draft action types (agent_run_output.v1.json proposed_action_type).
const (
	ActionSendEmail       = "send_email"
	ActionScheduleMeeting = "schedule_meeting"
	ActionShareDocument   = "share_document"
	ActionInternalNote    = "internal_note"
	ActionWait            = "wait"
	ActionNoAction        = "no_action"
	ActionCRMUpdate       = "crm_update" // prior actions only
)

// Artifact channels.
const (
	ChannelEmail   = "email"
	ChannelSlack   = "slack"
	ChannelCRMNote = "crm_note"
	ChannelNone    = "none"
)

// Tools (ADR-0014).
const (
	ToolEmailSend      = "email_send"
	ToolCalendarInvite = "calendar_invite"
	ToolDocumentShare  = "document_share"
	ToolSlackPost      = "slack_post"
	ToolCRMNote        = "crm_note"
	ToolCRMUpdate      = "crm_update"
)

// Autonomy ladder (ADR-0014), lowest first.
var autonomyLadder = []string{"suggest_only", "internal_actions", "crm_writeback", "customer_facing"}

// Windows for duplicate detection (ADR-0014).
const (
	DuplicateWindow      = 7 * 24 * time.Hour
	DuplicateBlockWindow = 24 * time.Hour
)

// Run modes and person kinds.
const (
	RunModeDryRun  = "dry_run"
	ExecuteRecord  = "record_only" // the only execute mode a dry run may use (the default under dry_run)
	ExecutePerform = "perform"
	KindEmployee   = "employee"
	statusDeparted = "departed"
)

// standingsThatOutrankAgents are claim standings an agent write may not overwrite when newer
// than the draft's evidence (ADR-0008/0009).
var standingsThatOutrankAgents = map[string]bool{"human_approved": true, "crm_explicit": true}

func isExternalAction(action string) bool {
	return action == ActionSendEmail || action == ActionScheduleMeeting || action == ActionShareDocument
}

func isPassiveAction(action string) bool { return action == ActionWait || action == ActionNoAction }

// personIndex maps person ids to directory rows.
func personIndex(in Input) map[string]Person {
	idx := make(map[string]Person, len(in.People))
	for _, p := range in.People {
		idx[p.PersonID] = p
	}
	return idx
}

// activityIndex maps activity ids to activities.
func activityIndex(in Input) map[string]Activity {
	idx := make(map[string]Activity, len(in.Activities))
	for _, a := range in.Activities {
		idx[a.ActivityID] = a
	}
	return idx
}

// visibleRecipients are the to/cc recipients (bcc is hidden from the other addressees).
func visibleRecipients(out Output) []Recipient {
	var vis []Recipient
	for _, r := range out.Recipients {
		if r.Role != "bcc" {
			vis = append(vis, r)
		}
	}
	return vis
}

// reachesCustomer reports whether any recipient is not a known employee.
func reachesCustomer(in Input) bool {
	people := personIndex(in)
	for _, r := range in.Draft.Recipients {
		if p, ok := people[r.PersonID]; !ok || p.Kind != KindEmployee {
			return true
		}
	}
	return false
}

// hasCRMWrite reports whether the draft intends a CRM writeback: a stage change or a due date.
// next_step is required by the contract on every draft, so on its own it is a summary of the
// next step, not a write (an email-only workspace, an account without an opportunity and an
// internal note all carry one).
func hasCRMWrite(out Output) bool {
	c := out.CRMNextStepIntent
	return (c.StageChange != nil && strings.TrimSpace(*c.StageChange) != "") || c.DueAt != nil
}

// draftText is the draft's subject and body.
func draftText(out Output) string {
	if out.FinishedArtifact.Subject == nil {
		return out.FinishedArtifact.Body
	}
	return *out.FinishedArtifact.Subject + "\n" + out.FinishedArtifact.Body
}

// latestState is the newest state the evals know.
func latestState(in Input) reducer.AccountState {
	if in.LatestState != nil {
		return *in.LatestState
	}
	return in.State
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func civilDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
