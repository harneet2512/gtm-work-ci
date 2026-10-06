package deterministic

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func withBody(in Input, body string) Input {
	in.Draft.FinishedArtifact.Body = body
	return in
}

func TestDatesMatchState(t *testing.T) {
	booked := "Hi Pat,\n\nPlenty of time before our security review on Thursday, October 1. See you then!"
	t.Run("pass: base draft", func(t *testing.T) { expectNone(t, DatesMatchState(baseInput())) })
	t.Run("fail: asserts a meeting the state does not have", func(t *testing.T) {
		got := DatesMatchState(withBody(baseInput(), booked))
		expectFinding(t, got, CheckDatesMatchState, true, "2026-10-01")
	})
	t.Run("pass: asserted meeting is the state's next meeting", func(t *testing.T) {
		in := withBody(baseInput(), booked)
		in.State.Fields.NextMeeting = known(map[string]any{"event_id": "e1", "title": "Security review", "start": "2026-10-01T15:00:00Z"})
		expectNone(t, DatesMatchState(in))
	})
	t.Run("pass: asserted meeting is on the calendar", func(t *testing.T) {
		in := withBody(baseInput(), booked)
		in.PriorActions = []PriorAction{{RefKind: "activity", RefID: "act-9", Action: ActionScheduleMeeting, Status: "scheduled",
			OccurredAt: now.Add(-48 * time.Hour), RecipientPersonIDs: []string{pat}, MeetingStart: ptr(at("2026-10-01T15:00:00Z"))}}
		expectNone(t, DatesMatchState(in))
	})
	t.Run("fail: next-step due date already passed", func(t *testing.T) {
		in := baseInput()
		in.Draft.CRMNextStepIntent.DueAt = ptr(at("2026-09-20T09:00:00Z"))
		expectFinding(t, DatesMatchState(in), CheckDatesMatchState, true, "due_at")
	})
	t.Run("fail: wait_until already passed", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(at("2026-09-28T09:00:00Z"))
		expectFinding(t, DatesMatchState(in), CheckDatesMatchState, true, "wait_until")
	})
	t.Run("fail: promises delivery on a past date", func(t *testing.T) {
		in := withBody(baseInput(), "Hi Pat,\n\nI'll get the revised order form over to you by Monday the 14th.")
		expectFinding(t, DatesMatchState(in), CheckDatesMatchState, true, "2026-09-14")
	})
	t.Run("pass: refers to a past date in the past tense", func(t *testing.T) {
		expectNone(t, DatesMatchState(withBody(baseInput(), "Thanks for the time on September 23.")))
	})
}

func TestMeetingTimeNotStale(t *testing.T) {
	meeting := func(due string, body string) Input {
		in := baseInput()
		in.Draft.ProposedActionType = ActionScheduleMeeting
		in.Draft.CRMNextStepIntent.DueAt = ptr(at(due))
		return withBody(in, body)
	}
	t.Run("pass: invite for a future slot", func(t *testing.T) {
		expectNone(t, MeetingTimeNotStale(meeting("2026-10-01T14:00:00Z", "Sending an invite for Thursday October 1 at 10:00.")))
	})
	t.Run("fail: invite for a slot that has passed", func(t *testing.T) {
		got := MeetingTimeNotStale(meeting("2026-09-28T14:00:00Z", "Sending an invite."))
		expectFinding(t, got, CheckMeetingNotStale, true, "2026-09-28")
	})
	t.Run("fail: email proposes a past meeting date", func(t *testing.T) {
		got := MeetingTimeNotStale(withBody(baseInput(), "Could we meet on September 28 to review?"))
		expectFinding(t, got, CheckMeetingNotStale, true, "2026-09-28")
	})
	t.Run("pass: mentions a past meeting", func(t *testing.T) {
		expectNone(t, MeetingTimeNotStale(withBody(baseInput(), "Great call on September 28.")))
	})
}

func withCommitment(in Input, owner, due string) Input {
	d := at(due)
	in.State.Fields.CurrentCommitments = known([]reducer.Item{{Text: "Send the security questionnaire answers",
		ClaimID: "c-1", Status: "open", DueAt: &d, OwnerPersonID: owner}})
	return in
}

func TestCommitmentNotContradicted(t *testing.T) {
	t.Run("pass: base draft", func(t *testing.T) {
		expectNone(t, CommitmentNotContradicted(withCommitment(baseInput(), rep, "2026-10-01T17:00:00Z")))
	})
	t.Run("fail: waits past our own commitment", func(t *testing.T) {
		in := withCommitment(baseInput(), rep, "2026-10-01T17:00:00Z")
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(at("2026-10-05T09:00:00Z"))
		expectFinding(t, CommitmentNotContradicted(in), CheckCommitmentHonored, true, "comes due")
	})
	t.Run("pass: waits past the customer's commitment", func(t *testing.T) {
		in := withCommitment(baseInput(), pat, "2026-10-01T17:00:00Z")
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(at("2026-10-05T09:00:00Z"))
		expectNone(t, CommitmentNotContradicted(in))
	})
	t.Run("fail: moves a committed date out", func(t *testing.T) {
		in := withCommitment(baseInput(), rep, "2026-10-01T17:00:00Z")
		in = withBody(in, "Hi Pat, I'll send the security questionnaire answers by October 6.")
		expectFinding(t, CommitmentNotContradicted(in), CheckCommitmentHonored, true, "2026-10-06")
	})
	t.Run("fail: CRM next step due after the commitment", func(t *testing.T) {
		in := withCommitment(baseInput(), rep, "2026-10-01T17:00:00Z")
		in.Draft.CRMNextStepIntent = CRMIntent{NextStep: "Send security questionnaire answers", DueAt: ptr(at("2026-10-08T17:00:00Z"))}
		expectFinding(t, CommitmentNotContradicted(in), CheckCommitmentHonored, true, "2026-10-08")
	})
}

func TestPromisedAssetExists(t *testing.T) {
	deck := Asset{Name: "pricing-deck.pdf", Aliases: []string{"pricing deck"}, Available: true}
	t.Run("pass: nothing promised", func(t *testing.T) { expectNone(t, PromisedAssetExists(baseInput())) })
	t.Run("fail: says attached, attaches nothing", func(t *testing.T) {
		got := PromisedAssetExists(withBody(baseInput(), "Attached is the proposal."))
		expectFinding(t, got, CheckPromisedAssetExists, true, "nothing is attached")
	})
	t.Run("fail: shares a document without one", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionShareDocument
		expectFinding(t, PromisedAssetExists(in), CheckPromisedAssetExists, true, "nothing is attached")
	})
	t.Run("pass: attachment is a library asset alias", func(t *testing.T) {
		in := withBody(baseInput(), "Attached is our pricing deck.")
		in.Assets, in.Draft.FinishedArtifact.Attachments = []Asset{deck}, []string{"Pricing Deck"}
		expectNone(t, PromisedAssetExists(in))
	})
	t.Run("fail: attachment is not in the library", func(t *testing.T) {
		in := withBody(baseInput(), "Attached is our roadmap.")
		in.Assets, in.Draft.FinishedArtifact.Attachments = []Asset{deck}, []string{"roadmap-2027.pdf"}
		expectFinding(t, PromisedAssetExists(in), CheckPromisedAssetExists, true, "roadmap-2027.pdf")
	})
	t.Run("fail: promised asset is not available", func(t *testing.T) {
		in := withBody(baseInput(), "I'll send our pricing deck tomorrow.")
		unavailable := deck
		unavailable.Available = false
		in.Assets = []Asset{unavailable}
		expectFinding(t, PromisedAssetExists(in), CheckPromisedAssetExists, true, "not available")
	})
}

func TestDateCommitmentConsistencyResult(t *testing.T) {
	pass := DateCommitmentConsistency(baseInput())
	if pass.Result.Verdict != "pass" || pass.Result.Blocking || len(pass.Checks) != 4 {
		t.Fatalf("pass result = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	in := baseInput()
	in.Draft.WaitUntil = ptr(now.Add(-48 * time.Hour))
	fail := DateCommitmentConsistency(in)
	if fail.Result.Verdict != "fail" || !fail.Result.Blocking || fail.Result.SuggestedCorrection == nil {
		t.Fatalf("fail result = %+v", fail.Result)
	}
	if fail.Result.EvalVersion != "date_commitment_consistency:v1" {
		t.Fatalf("version = %s", fail.Result.EvalVersion)
	}
	expectContractValid(t, fail.Result)
}

// Past-tense context reports history; only a date presented as upcoming can be stale.
func TestPastDatesInPastContextAreNotStale(t *testing.T) {
	good := []string{
		"Thanks for the call on September 28, I'll send the summary of the rollout options today.",
		"As discussed on September 25, we will share the summary of the rollout options.",
		"Following our call on September 26, here is the summary of the rollout options.",
		"We spoke on September 24 about the rollout options.",
	}
	for _, body := range good {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = body
		expectNone(t, DatesMatchState(in))
		expectNone(t, MeetingTimeNotStale(in))
		expectNone(t, CriticalStatementsSupported(in))
		if g := Decide(Evaluate(in)); g != GateProceed {
			t.Errorf("%q: gate = %s", body, g)
		}
	}
	bad := map[string]Check{
		"I'll send the proposal by September 22.":             CheckDatesMatchState,
		"Thanks for the call. Let's meet on September 22.":    CheckMeetingNotStale,
		"Looking forward to our call on September 22 at 3pm.": CheckMeetingNotStale,
		"September 22 works for me to review the rollout.":    CheckDatesMatchState,
		"Following our call, could we meet on September 22?":  CheckMeetingNotStale,
	}
	for body, check := range bad {
		in := baseInput()
		in.Draft.FinishedArtifact.Body = body
		f := append(DatesMatchState(in), MeetingTimeNotStale(in)...)
		if len(f) == 0 {
			t.Errorf("%q must still be flagged (%s)", body, check)
		}
	}
}

func TestCommitmentContradictionIsLooselyMatchedButAcknowledgedMovesPass(t *testing.T) {
	due := "2026-10-01T00:00:00Z"
	mk := func(body string) Input {
		in := baseInput()
		in.State.Fields.CurrentCommitments = known([]reducer.Item{{Text: "Send pricing proposal", Status: "open", OwnerPersonID: rep, DueAt: ptr(at(due))}})
		in.Draft.FinishedArtifact.Body = body
		return in
	}
	t.Run("one shared object word is a weak match: it fails without blocking", func(t *testing.T) {
		expectFinding(t, CommitmentNotContradicted(mk("I'll get the proposal over by October 10.")), CheckCommitmentHonored, false, "2026-10-10")
	})
	t.Run("the whole object is a strong match: it blocks", func(t *testing.T) {
		expectFinding(t, CommitmentNotContradicted(mk("I'll send the pricing proposal by October 10.")), CheckCommitmentHonored, true, "2026-10-10")
	})
	t.Run("an openly moved date passes", func(t *testing.T) {
		expectNone(t, CommitmentNotContradicted(mk("Sorry, I need more time: the proposal will be with you by October 10.")))
		expectNone(t, CommitmentNotContradicted(mk("I'm pushing back the proposal to October 10.")))
	})
	t.Run("an unrelated later date passes", func(t *testing.T) {
		expectNone(t, CommitmentNotContradicted(mk("Let's meet by October 10 to talk about the rollout.")))
	})
	t.Run("a contradicted commitment gates the draft to revise", func(t *testing.T) {
		in := mk("")
		in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(now.Add(7*24*time.Hour))
		in.Draft.CRMNextStepIntent = CRMIntent{}
		if g := Decide(Evaluate(in)); g != GateRevise {
			t.Fatalf("gate = %s, want revise", g)
		}
	})
}
