package deterministic

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// HAR-97 §4 "Duplicate-action detection" sub-checks.
const (
	CheckEmailNotSent        Check = "duplicate.email_not_already_sent"
	CheckMeetingNotScheduled Check = "duplicate.meeting_not_already_scheduled"
	CheckCRMNotCompleted     Check = "duplicate.crm_action_not_completed"
)

var duplicateChecks = []Check{CheckEmailNotSent, CheckMeetingNotScheduled, CheckCRMNotCompleted}

// sameContentThreshold is the share of content words two bodies must share, in both directions.
const (
	sameContentThreshold = 0.8
	minAskTokens         = 3
	askContainment       = 0.6
	stemMatch            = 0.5
)

var yearRE = regexp.MustCompile(`(?i)\b20\d\d(q[1-4])?\b`)

var questionRE = regexp.MustCompile(`[^.!?\n]*\?`)

// DuplicateAction is the HAR-97 §4 "Duplicate-action detection" eval.
func DuplicateAction(in Input) Judgment {
	var f []Finding
	f = append(f, EmailNotAlreadySent(in)...)
	f = append(f, MeetingNotAlreadyScheduled(in)...)
	f = append(f, CRMActionNotAlreadyCompleted(in)...)
	return judge(in, TypeDuplicate, duplicateChecks, []string{"next_meeting", "stage", "next_milestone"}, f)
}

// canonicalIDs maps recipients to their surviving person ids (a merged person is the same person).
func canonicalIDs(in Input, ids []string) map[string]bool {
	people := personIndex(in)
	out := map[string]bool{}
	for _, id := range ids {
		if p, ok := people[id]; ok && p.MergedInto != nil {
			id = *p.MergedInto
		}
		out[id] = true
	}
	return out
}

func draftRecipientIDs(in Input) []string {
	ids := make([]string, len(in.Draft.Recipients))
	for i, r := range in.Draft.Recipients {
		ids[i] = r.PersonID
	}
	return ids
}

func overlapsPeople(in Input, prior []string) bool {
	a, b := canonicalIDs(in, draftRecipientIDs(in)), canonicalIDs(in, prior)
	for id := range a {
		if b[id] {
			return true
		}
	}
	return false
}

func withPrior(f Finding, p PriorAction) Finding {
	if p.RefKind == "activity" {
		return f.withActivities(p.RefID)
	}
	return f
}

// EmailNotAlreadySent: within 7 days, the draft does not repeat material or an ask already sent to
// an overlapping set of recipients. Within 24 hours the repeat blocks; later it is a plain failure
// the revision planner can rework.
func EmailNotAlreadySent(in Input) []Finding {
	if in.Draft.ProposedActionType != ActionSendEmail && in.Draft.ProposedActionType != ActionShareDocument {
		return nil
	}
	var out []Finding
	for _, p := range in.PriorActions {
		age := in.EvaluatedAt.Sub(p.OccurredAt)
		if p.Action != ActionSendEmail && p.Action != ActionShareDocument || p.Status != "completed" ||
			age < 0 || age > DuplicateWindow || !overlapsPeople(in, p.RecipientPersonIDs) {
			continue
		}
		what, same := repeatedContent(in.Draft.FinishedArtifact, p)
		if !same {
			continue
		}
		f := withPrior(failure(CheckEmailNotSent,
			fmt.Sprintf("repeats %s already sent %s ago (%s)", what, ago(age), p.OccurredAt.Format(day)),
			"Do not resend; reference the earlier message or add something new."), p)
		if age <= DuplicateBlockWindow {
			f = f.blocking()
		}
		out = append(out, f)
	}
	return out
}

// repeatedContent says what the draft repeats from a prior message: the same attachment, the same
// body, or the same question.
func repeatedContent(art Artifact, p PriorAction) (string, bool) {
	if sharedAttachment(art.Attachments, p.Attachments) {
		return "the same attachment", true
	}
	prior := ""
	if p.BodyText != nil {
		prior = *p.BodyText
	}
	if name, ok := attachmentDeliveredInText(art.Attachments, prior); ok {
		return fmt.Sprintf("the same material (%s)", name), true
	}
	if mutualContainment(contentTokens(art.Body, deliveryWords), contentTokens(prior, deliveryWords), minAskTokens) {
		return "the same message", true
	}
	for _, q := range questionRE.FindAllString(art.Body, -1) {
		for _, pq := range questionRE.FindAllString(prior, -1) {
			if sameAsk(contentTokens(q, nudgeWords), contentTokens(pq, nudgeWords)) {
				return "the same question", true
			}
		}
	}
	return "", false
}

func mutualContainment(a, b []string, minTokens int) bool {
	return len(toSet(a)) >= minTokens && len(toSet(b)) >= minTokens &&
		containment(a, b) >= sameContentThreshold && containment(b, a) >= sameContentThreshold
}

func sharedAttachment(a, b []string) bool {
	set := map[string]bool{}
	for _, x := range b {
		set[normalize(x)] = true
	}
	for _, x := range a {
		if set[normalize(x)] {
			return true
		}
	}
	return false
}

func ago(d time.Duration) string {
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// MeetingNotAlreadyScheduled: no future meeting with an overlapping set of attendees is already booked.
func MeetingNotAlreadyScheduled(in Input) []Finding {
	if in.Draft.ProposedActionType != ActionScheduleMeeting {
		return nil
	}
	var out []Finding
	for _, p := range in.PriorActions {
		if p.Action != ActionScheduleMeeting || p.Status != "scheduled" || p.MeetingStart == nil ||
			p.MeetingStart.Before(in.EvaluatedAt) || !overlapsPeople(in, p.RecipientPersonIDs) {
			continue
		}
		out = append(out, withPrior(failure(CheckMeetingNotScheduled,
			fmt.Sprintf("a meeting with these attendees is already scheduled for %s", p.MeetingStart.Format(day)),
			"Do not book another meeting; move or reference the scheduled one.").blocking().withState("next_meeting"), p))
	}
	return out
}

// CRMActionNotAlreadyCompleted: the stage is not already the target, and neither the stage change
// nor the next step was already written within the duplicate window.
func CRMActionNotAlreadyCompleted(in Input) []Finding {
	c := in.Draft.CRMNextStepIntent
	var out []Finding
	if c.StageChange != nil && strings.TrimSpace(*c.StageChange) != "" {
		out = append(out, stageAlreadyDone(in, *c.StageChange)...)
	}
	if hasCRMWrite(in.Draft) && strings.TrimSpace(c.NextStep) != "" {
		out = append(out, nextStepAlreadyDone(in, c.NextStep)...)
	}
	return out
}

func stageAlreadyDone(in Input, target string) []Finding {
	var out []Finding
	cur, err := currentStage(in)
	if err != nil {
		return []Finding{failure(CheckCRMNotCompleted, fmt.Sprintf("the stage state is unreadable: %v", err),
			"Repair the account state before writing the stage.").blocking().withState("stage")}
	}
	if cur != "" && stageKey(cur) == stageKey(target) {
		out = append(out, failure(CheckCRMNotCompleted, fmt.Sprintf("the deal is already in stage %q", cur),
			"Drop the stage change; it is already in effect.").blocking().withState("stage"))
	}
	for _, p := range recentCRMWrites(in) {
		if p.StageChange != nil && stageKey(*p.StageChange) == stageKey(target) {
			out = append(out, withPrior(failure(CheckCRMNotCompleted,
				fmt.Sprintf("the stage change to %q was already written on %s", target, p.OccurredAt.Format(day)),
				"Drop the stage change; it was already written.").blocking(), p))
		}
	}
	return out
}

func nextStepAlreadyDone(in Input, step string) []Finding {
	var out []Finding
	for _, p := range recentCRMWrites(in) {
		if p.NextStep != nil && normalize(*p.NextStep) == normalize(step) {
			out = append(out, withPrior(failure(CheckCRMNotCompleted,
				fmt.Sprintf("the next step %q was already written on %s", step, p.OccurredAt.Format(day)),
				"Drop the next step; it was already written.").blocking(), p))
		}
	}
	return out
}

func recentCRMWrites(in Input) []PriorAction {
	var out []PriorAction
	for _, p := range in.PriorActions {
		age := in.EvaluatedAt.Sub(p.OccurredAt)
		if p.Action == ActionCRMUpdate && p.Status == "completed" && age >= 0 && age <= DuplicateWindow {
			out = append(out, p)
		}
	}
	return out
}

// nudgeWords are filler of a follow-up ("just making sure this didn't get buried") that does not
// change what is asked.
var nudgeWords = toSet(strings.Fields(`just making sure didn't get buried bump circling checking following again
quick gentle reminder wanted touching base`))

// sameAsk: one question's content words are mostly contained in the other's.
func sameAsk(a, b []string) bool {
	return len(toSet(a)) >= minAskTokens && len(toSet(b)) >= minAskTokens &&
		math.Max(containment(a, b), containment(b, a)) >= askContainment
}

// attachmentDeliveredInText finds a draft attachment that the prior message already handed over
// in its text ("our BAA template ... are attached"), when the prior record has no attachment list.
// File stems are compared with letters and digits only, years and quarters ignored.
func attachmentDeliveredInText(attachments []string, prior string) (string, bool) {
	if prior == "" {
		return "", false
	}
	squashed := map[string]bool{}
	for _, t := range tokens(prior) {
		squashed[stageKey(t)] = true
	}
	delivered := false
	for _, c := range clauses(prior) {
		delivered = delivered || isDelivery(c)
	}
	if !delivered {
		return "", false
	}
	for _, a := range attachments {
		stem := yearRE.ReplaceAllString(strings.TrimSuffix(a, filepath.Ext(a)), " ")
		words := tokens(strings.NewReplacer("-", " ", "_", " ").Replace(stem))
		hit := 0
		for _, w := range words {
			if squashed[stageKey(w)] {
				hit++
			}
		}
		if len(words) > 0 && hit > 0 && float64(hit)/float64(len(words)) >= stemMatch {
			return a, true
		}
	}
	return "", false
}
