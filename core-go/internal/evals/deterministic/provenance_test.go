package deterministic

import (
	"testing"
	"time"
)

func bodyInput(body string) Input {
	in := baseInput()
	in.Draft.FinishedArtifact.Body = body
	return in
}

func TestCriticalStatementsSupported(t *testing.T) {
	t.Run("clean draft passes", func(t *testing.T) { expectNone(t, CriticalStatementsSupported(baseInput())) })
	t.Run("quantity in the cited evidence passes", func(t *testing.T) {
		expectNone(t, CriticalStatementsSupported(bodyInput("This covers your 120 seats.")))
	})
	t.Run("quantity absent from the cited evidence blocks", func(t *testing.T) {
		expectFinding(t, CriticalStatementsSupported(bodyInput("This covers your 90 seats.")),
			CheckCriticalStatementsSupported, false, "90 seats")
	})
	t.Run("uncited activity does not support a fact", func(t *testing.T) {
		in := bodyInput("This covers your 40 clinics.")
		in.Activities = append(in.Activities, Activity{ActivityID: "00000000-0000-4000-8000-0000000000a2", AccountID: ptr(acct),
			ActivityType: "Note", OccurredAt: now.Add(-time.Hour), Text: "They run 40 clinics."})
		expectFinding(t, CriticalStatementsSupported(in), CheckCriticalStatementsSupported, false, "40 clinics")
	})
	t.Run("amount from the quote on file passes", func(t *testing.T) {
		in := bodyInput("The total is $4,800.")
		in.Commercial.Quoted = []QuotedLine{{Product: "Alpha", Total: ptr(4800.0)}}
		expectNone(t, CriticalStatementsSupported(in))
	})
	t.Run("amount without a source blocks", func(t *testing.T) {
		expectFinding(t, CriticalStatementsSupported(bodyInput("The total is $4,800.")), CheckCriticalStatementsSupported, true, "4800")
	})
	t.Run("percentage without a source blocks", func(t *testing.T) {
		expectFinding(t, CriticalStatementsSupported(bodyInput("Adoption is at 80% today.")), CheckCriticalStatementsSupported, true, "80%")
	})
	t.Run("asserted date from state passes", func(t *testing.T) {
		in := bodyInput("Our review on 2026-10-06 stands.")
		in.State.Fields.NextMeeting = known(map[string]any{"start": "2026-10-06T15:00:00Z"})
		expectNone(t, CriticalStatementsSupported(in))
	})
	t.Run("asserted date without a source blocks", func(t *testing.T) {
		expectFinding(t, CriticalStatementsSupported(bodyInput("Our review is on 2026-10-06.")), CheckCriticalStatementsSupported, false, "2026-10-06")
	})
	t.Run("proposed date is not a fact", func(t *testing.T) {
		expectNone(t, CriticalStatementsSupported(bodyInput("How about 2026-10-06 for the call?")))
	})
	t.Run("title without a source blocks", func(t *testing.T) {
		expectFinding(t, CriticalStatementsSupported(bodyInput("Please loop in your CFO.")), CheckCriticalStatementsSupported, false, "title cfo")
	})
	t.Run("title in the evidence passes", func(t *testing.T) {
		in := bodyInput("Please loop in your CFO.")
		in.Activities[0].Text += " Our CFO needs the numbers."
		expectNone(t, CriticalStatementsSupported(in))
	})
	t.Run("passive actions state nothing", func(t *testing.T) {
		in := bodyInput("This covers your 90 seats.")
		in.Draft.ProposedActionType = ActionWait
		expectNone(t, CriticalStatementsSupported(in))
	})
}

func TestEvidenceRefsResolve(t *testing.T) {
	t.Run("missing activity blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.EvidenceRefs = []EvidenceRef{{ActivityID: "00000000-0000-4000-8000-0000000000ff"}}
		expectFinding(t, CriticalStatementsSupported(in), CheckCriticalStatementsSupported, true, "not an activity")
	})
	t.Run("another account's activity blocks", func(t *testing.T) {
		in := baseInput()
		in.Activities[0].AccountID = ptr(otherAcct)
		expectFinding(t, CriticalStatementsSupported(in), CheckCriticalStatementsSupported, true, "another account")
	})
	t.Run("quote not in the activity blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.EvidenceRefs = []EvidenceRef{{ActivityID: act1, Quote: "we agreed to a pilot"}}
		got := CriticalStatementsSupported(in)
		expectFinding(t, got, CheckCriticalStatementsSupported, true, "does not occur")
		if len(got[0].EvidenceRefs) != 1 {
			t.Fatalf("the offending ref must be attached: %+v", got[0])
		}
	})
}

func TestProvenanceCoverageResult(t *testing.T) {
	pass := ProvenanceCoverage(baseInput())
	if pass.Result.Verdict != "pass" || len(pass.Checks) != 1 {
		t.Fatalf("pass = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	fail := ProvenanceCoverage(bodyInput("This covers your 90 seats."))
	if fail.Result.Verdict != "fail" || fail.Result.Blocking {
		t.Fatalf("an unsupported quantity fails without blocking: %+v", fail.Result)
	}
	if r := ProvenanceCoverage(bodyInput("The total is $9,999.")).Result; !r.Blocking {
		t.Fatalf("an unsupported price blocks: %+v", r)
	}
	expectContractValid(t, fail.Result)
	in := baseInput()
	in.Draft.EvidenceRefs = []EvidenceRef{{ActivityID: "00000000-0000-4000-8000-0000000000ff"}}
	expectContractValid(t, ProvenanceCoverage(in).Result)
}
