package deterministic

import (
	"testing"
	"time"
)

const priorEmailBody = "Hi Pat, as you asked, here is the summary of the rollout options. Could you confirm the security review owner?"

func priorEmail(ago time.Duration, body string) PriorAction {
	return PriorAction{RefKind: "activity", RefID: act1, Action: ActionSendEmail, Status: "completed",
		OccurredAt: now.Add(-ago), RecipientPersonIDs: []string{pat}, BodyText: ptr(body), Attachments: []string{}}
}

func TestEmailNotAlreadySent(t *testing.T) {
	t.Run("no prior action passes", func(t *testing.T) { expectNone(t, EmailNotAlreadySent(baseInput())) })
	t.Run("same message within 24h blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		in.PriorActions = []PriorAction{priorEmail(2*time.Hour, priorEmailBody)}
		got := EmailNotAlreadySent(in)
		expectFinding(t, got, CheckEmailNotSent, true, "the same message")
		if len(got[0].ActivityRefs) != 1 {
			t.Fatalf("want the prior activity cited, got %+v", got[0])
		}
	})
	t.Run("same message after 24h fails without blocking", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		in.PriorActions = []PriorAction{priorEmail(3*24*time.Hour, priorEmailBody)}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, false, "3d ago")
	})
	t.Run("older than the window passes", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		in.PriorActions = []PriorAction{priorEmail(8*24*time.Hour, priorEmailBody)}
		expectNone(t, EmailNotAlreadySent(in))
	})
	t.Run("different recipients pass", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		p := priorEmail(time.Hour, priorEmailBody)
		p.RecipientPersonIDs = []string{sam}
		in.PriorActions = []PriorAction{p}
		expectNone(t, EmailNotAlreadySent(in))
	})
	t.Run("merged recipient counts as the same person", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		in.People[1].MergedInto = ptr("p-9")
		p := priorEmail(time.Hour, priorEmailBody)
		p.RecipientPersonIDs = []string{"p-9"}
		in.PriorActions = []PriorAction{p}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, true, "same message")
	})
	t.Run("different content passes", func(t *testing.T) {
		in := baseInput()
		in.PriorActions = []PriorAction{priorEmail(time.Hour, "Pricing proposal for the pilot is ready; legal terms follow tomorrow.")}
		expectNone(t, EmailNotAlreadySent(in))
	})
	t.Run("same attachment blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Attachments = []string{"Security Whitepaper.pdf"}
		p := priorEmail(time.Hour, "Different words entirely.")
		p.Attachments = []string{"security whitepaper.pdf"}
		in.PriorActions = []PriorAction{p}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, true, "attachment")
	})
	t.Run("same question blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = "Quick note. Could you confirm the security review owner?"
		in.PriorActions = []PriorAction{priorEmail(time.Hour, priorEmailBody)}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, true, "question")
	})
	t.Run("cancelled prior is ignored", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = priorEmailBody
		p := priorEmail(time.Hour, priorEmailBody)
		p.Status = "cancelled"
		in.PriorActions = []PriorAction{p}
		expectNone(t, EmailNotAlreadySent(in))
	})
	t.Run("non-email drafts are not checked", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionWait
		in.Draft.FinishedArtifact.Body = priorEmailBody
		in.PriorActions = []PriorAction{priorEmail(time.Hour, priorEmailBody)}
		expectNone(t, EmailNotAlreadySent(in))
	})
}

func meetingInput(start time.Time) Input {
	in := baseInput()
	in.Draft.ProposedActionType = ActionScheduleMeeting
	in.PriorActions = []PriorAction{{RefKind: "agent_run", RefID: runID, Action: ActionScheduleMeeting, Status: "scheduled",
		OccurredAt: now.Add(-time.Hour), RecipientPersonIDs: []string{pat}, MeetingStart: &start, Attachments: []string{}}}
	return in
}

func TestMeetingNotAlreadyScheduled(t *testing.T) {
	t.Run("no meeting passes", func(t *testing.T) { expectNone(t, MeetingNotAlreadyScheduled(baseInput())) })
	t.Run("future meeting with the same attendee blocks", func(t *testing.T) {
		expectFinding(t, MeetingNotAlreadyScheduled(meetingInput(now.Add(48*time.Hour))), CheckMeetingNotScheduled, true, "already scheduled")
	})
	t.Run("past meeting passes", func(t *testing.T) {
		expectNone(t, MeetingNotAlreadyScheduled(meetingInput(now.Add(-48*time.Hour))))
	})
	t.Run("other attendees pass", func(t *testing.T) {
		in := meetingInput(now.Add(48 * time.Hour))
		in.PriorActions[0].RecipientPersonIDs = []string{sam}
		expectNone(t, MeetingNotAlreadyScheduled(in))
	})
	t.Run("cancelled meeting passes", func(t *testing.T) {
		in := meetingInput(now.Add(48 * time.Hour))
		in.PriorActions[0].Status = "cancelled"
		expectNone(t, MeetingNotAlreadyScheduled(in))
	})
	t.Run("emails are not checked", func(t *testing.T) {
		in := meetingInput(now.Add(48 * time.Hour))
		in.Draft.ProposedActionType = ActionSendEmail
		expectNone(t, MeetingNotAlreadyScheduled(in))
	})
}

func crmWrite(ago time.Duration, stage, step *string) PriorAction {
	return PriorAction{RefKind: "agent_run", RefID: runID, Action: ActionCRMUpdate, Status: "completed",
		OccurredAt: now.Add(-ago), RecipientPersonIDs: []string{}, StageChange: stage, NextStep: step, Attachments: []string{}}
}

func TestCRMActionNotAlreadyCompleted(t *testing.T) {
	t.Run("new stage and step pass", func(t *testing.T) {
		expectNone(t, CRMActionNotAlreadyCompleted(crmInput("Commercial review")))
	})
	t.Run("stage already in effect blocks", func(t *testing.T) {
		in := crmInput("technical evaluation")
		in.Draft.CRMNextStepIntent.NextStep = ""
		expectFinding(t, CRMActionNotAlreadyCompleted(in), CheckCRMNotCompleted, true, "already in stage")
	})
	t.Run("stage change already written blocks", func(t *testing.T) {
		in := crmInput("Commercial review")
		in.Draft.CRMNextStepIntent.NextStep = ""
		in.PriorActions = []PriorAction{crmWrite(time.Hour, ptr("commercial-review"), nil)}
		expectFinding(t, CRMActionNotAlreadyCompleted(in), CheckCRMNotCompleted, true, "already written")
	})
	t.Run("next step already written blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.CRMNextStepIntent.DueAt = ptr(now.Add(72 * time.Hour))
		in.PriorActions = []PriorAction{crmWrite(time.Hour, nil, ptr("pat reviews the rollout summary"))}
		expectFinding(t, CRMActionNotAlreadyCompleted(in), CheckCRMNotCompleted, true, "next step")
	})
	t.Run("old write outside the window passes", func(t *testing.T) {
		in := baseInput()
		in.PriorActions = []PriorAction{crmWrite(9*24*time.Hour, nil, ptr("Pat reviews the rollout summary"))}
		expectNone(t, CRMActionNotAlreadyCompleted(in))
	})
	t.Run("no CRM intent passes", func(t *testing.T) {
		in := baseInput()
		in.Draft.CRMNextStepIntent = CRMIntent{}
		in.PriorActions = []PriorAction{crmWrite(time.Hour, nil, ptr("anything"))}
		expectNone(t, CRMActionNotAlreadyCompleted(in))
	})
}

func TestDuplicateActionResult(t *testing.T) {
	pass := DuplicateAction(baseInput())
	if pass.Result.Verdict != "pass" || len(pass.Checks) != 3 {
		t.Fatalf("pass = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	in := baseInput()
	in.Draft.FinishedArtifact.Body = priorEmailBody
	in.PriorActions = []PriorAction{priorEmail(3*24*time.Hour, priorEmailBody)}
	soft := DuplicateAction(in)
	if soft.Result.Verdict != "fail" || soft.Result.Blocking || len(soft.Result.ActivityRefs) != 1 {
		t.Fatalf("a repeat after 24h fails without blocking: %+v", soft.Result)
	}
	expectContractValid(t, soft.Result)
	in.PriorActions = []PriorAction{priorEmail(time.Hour, priorEmailBody)}
	if r := DuplicateAction(in).Result; !r.Blocking {
		t.Fatalf("a repeat within 24h blocks: %+v", r)
	}
}

func TestRepeatsAreFoundWithoutAttachmentListsAndThroughNudgeWording(t *testing.T) {
	t.Run("attachment already handed over in the prior text", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = "Attached are the files."
		in.Draft.FinishedArtifact.Attachments = []string{"baa-template.pdf", "hipaa-controls-summary-2026Q3.pdf"}
		in.PriorActions = []PriorAction{priorEmail(2*time.Hour, "Our BAA template and HIPAA controls summary are attached.")}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, true, "same material")
	})
	t.Run("a prior text that only mentions the document is not a delivery", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Attachments = []string{"baa-template.pdf"}
		in.PriorActions = []PriorAction{priorEmail(2*time.Hour, "Do you still need the BAA template?")}
		expectNone(t, EmailNotAlreadySent(in))
	})
	t.Run("same ask with a nudge in front", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = "Just making sure this didn't get buried - could we book a 60-minute security review for Thursday, October 1?"
		in.PriorActions = []PriorAction{priorEmail(4*time.Hour,
			"To keep the EU timeline on track, could we book a 60-minute security review for Thursday, October 1?")}
		expectFinding(t, EmailNotAlreadySent(in), CheckEmailNotSent, true, "same question")
	})
	t.Run("a different ask passes", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = "Could you confirm who signs the order form?"
		in.PriorActions = []PriorAction{priorEmail(4*time.Hour,
			"Could we book a 60-minute security review for Thursday, October 1?")}
		expectNone(t, EmailNotAlreadySent(in))
	})
}
