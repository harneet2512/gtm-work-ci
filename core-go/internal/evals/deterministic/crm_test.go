package deterministic

import (
	"testing"
	"time"
)

func crmInput(stage string) Input {
	in := baseInput()
	in.Draft.CRMNextStepIntent = CRMIntent{NextStep: "Pat reviews the rollout summary", StageChange: ptr(stage)}
	return in
}

func TestStageUpdateLegal(t *testing.T) {
	t.Run("one step forward passes", func(t *testing.T) {
		expectNone(t, StageUpdateLegal(crmInput("commercial-review")))
	})
	t.Run("same stage passes", func(t *testing.T) {
		expectNone(t, StageUpdateLegal(crmInput("Technical evaluation")))
	})
	t.Run("no stage change passes", func(t *testing.T) {
		expectNone(t, StageUpdateLegal(baseInput()))
	})
	t.Run("backward blocks", func(t *testing.T) {
		expectFinding(t, StageUpdateLegal(crmInput("Discovery")), CheckStageLegal, true, "backward")
	})
	t.Run("skipping stages blocks", func(t *testing.T) {
		expectFinding(t, StageUpdateLegal(crmInput("Negotiation")), CheckStageLegal, true, "jumps 2")
	})
	t.Run("unknown stage blocks", func(t *testing.T) {
		expectFinding(t, StageUpdateLegal(crmInput("Vibes")), CheckStageLegal, true, "not one of")
	})
	t.Run("closing blocks", func(t *testing.T) {
		expectFinding(t, StageUpdateLegal(crmInput("Closed won")), CheckStageLegal, true, "terminal")
	})
	t.Run("reopening a closed deal blocks", func(t *testing.T) {
		in := crmInput("Discovery")
		in.State.Fields.Stage = known("Closed lost")
		expectFinding(t, StageUpdateLegal(in), CheckStageLegal, true, "out of terminal")
	})
}

func TestOwnerExists(t *testing.T) {
	t.Run("employee owner passes", func(t *testing.T) { expectNone(t, OwnerExists(crmInput("Technical evaluation"))) })
	t.Run("no owner blocks", func(t *testing.T) {
		in := crmInput("Technical evaluation")
		in.Opportunities[0].OwnerPersonID = nil
		in.State.Fields.Owner = unknownField()
		expectFinding(t, OwnerExists(in), CheckOwnerExists, true, "no owner")
	})
	t.Run("state owner is used without an opportunity row", func(t *testing.T) {
		in := crmInput("Technical evaluation")
		in.Opportunities = nil
		expectNone(t, OwnerExists(in))
	})
	t.Run("unknown person blocks", func(t *testing.T) {
		in := crmInput("Technical evaluation")
		in.Opportunities[0].OwnerPersonID = ptr("emp-404")
		expectFinding(t, OwnerExists(in), CheckOwnerExists, true, "not a known person")
	})
	t.Run("merged owner blocks", func(t *testing.T) {
		in := crmInput("Technical evaluation")
		in.People[0].MergedInto = ptr("emp-2")
		expectFinding(t, OwnerExists(in), CheckOwnerExists, true, "merged")
	})
	t.Run("a customer cannot own the deal", func(t *testing.T) {
		in := crmInput("Technical evaluation")
		in.Opportunities[0].OwnerPersonID = ptr(pat)
		expectFinding(t, OwnerExists(in), CheckOwnerExists, true, "not an employee")
	})
}

func TestNextStepDoesNotOverwrite(t *testing.T) {
	t.Run("no newer state passes", func(t *testing.T) { expectNone(t, NextStepDoesNotOverwrite(crmInput("commercial review"))) })
	t.Run("human-approved stage newer than the evidence blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		in.State.Fields.Stage.Standing = ptr("human_approved")
		in.State.Fields.Stage.AsOf = ptr(now)
		expectFinding(t, NextStepDoesNotOverwrite(in), CheckNoOverwrite, true, "human_approved")
	})
	t.Run("human-approved stage older than the evidence passes", func(t *testing.T) {
		in := crmInput("commercial review")
		in.State.Fields.Stage.Standing = ptr("human_approved")
		in.State.Fields.Stage.AsOf = ptr(now.Add(-72 * time.Hour))
		expectNone(t, NextStepDoesNotOverwrite(in))
	})
	t.Run("next step over a crm-explicit milestone blocks", func(t *testing.T) {
		in := baseInput()
		in.Draft.CRMNextStepIntent.DueAt = ptr(now.Add(72 * time.Hour))
		in.State.Fields.NextMilestone = known("Security review on the 12th")
		in.State.Fields.NextMilestone.Standing = ptr("crm_explicit")
		in.State.Fields.NextMilestone.AsOf = ptr(now)
		expectFinding(t, NextStepDoesNotOverwrite(in), CheckNoOverwrite, true, "next step")
	})
	t.Run("field changed in the latest state blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		latest := in.State
		latest.Version = 4
		latest.Fields.Stage = known("Negotiation")
		in.LatestState = &latest
		expectFinding(t, NextStepDoesNotOverwrite(in), CheckNoOverwrite, true, "changed after")
	})
	t.Run("unchanged latest state passes", func(t *testing.T) {
		in := crmInput("commercial review")
		latest := in.State
		latest.Version = 4
		in.LatestState = &latest
		expectNone(t, NextStepDoesNotOverwrite(in))
	})
}

func TestWritebackTargetCorrect(t *testing.T) {
	t.Run("own opportunity passes", func(t *testing.T) { expectNone(t, WritebackTargetCorrect(crmInput("commercial review"))) })
	t.Run("no opportunity stops", func(t *testing.T) {
		in := crmInput("commercial review")
		in.OpportunityID = nil
		got := WritebackTargetCorrect(in)
		expectFinding(t, got, CheckWritebackTarget, true, "no target")
		if got[0].Remedy != RemedyStop {
			t.Fatalf("remedy = %s", got[0].Remedy)
		}
	})
	t.Run("opportunity missing from directory blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		in.Opportunities = nil
		expectFinding(t, WritebackTargetCorrect(in), CheckWritebackTarget, true, "not in the directory")
	})
	t.Run("another account's opportunity blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		in.Opportunities[0].AccountID = otherAcct
		expectFinding(t, WritebackTargetCorrect(in), CheckWritebackTarget, true, "belongs to account")
	})
	t.Run("state of another account blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		in.State.AccountID = otherAcct
		expectFinding(t, WritebackTargetCorrect(in), CheckWritebackTarget, true, "state is for account")
	})
	t.Run("state of another opportunity blocks", func(t *testing.T) {
		in := crmInput("commercial review")
		in.State.OpportunityID = ptr("opp-2")
		expectFinding(t, WritebackTargetCorrect(in), CheckWritebackTarget, true, "for opportunity")
	})
}

func TestCRMWritebackResult(t *testing.T) {
	pass := CRMWriteback(crmInput("commercial review"))
	if pass.Result.Verdict != "pass" || len(pass.Checks) != 4 {
		t.Fatalf("pass = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	noWrite := baseInput()
	noWrite.Draft.CRMNextStepIntent = CRMIntent{}
	noWrite.OpportunityID = nil
	if r := CRMWriteback(noWrite).Result; r.Verdict != "pass" {
		t.Fatalf("a draft without a CRM write must pass: %+v", r)
	}
	fail := CRMWriteback(crmInput("Closed won"))
	if fail.Result.Verdict != "fail" || !fail.Result.Blocking {
		t.Fatalf("fail = %+v", fail.Result)
	}
	expectContractValid(t, fail.Result)
}

// A required next_step text alone is not a CRM writeback: good emails must not be stopped for it.
func TestNextStepTextAloneIsNotACRMWrite(t *testing.T) {
	proceeds := func(t *testing.T, in Input) {
		t.Helper()
		js := Evaluate(in)
		if g := Decide(js); g != GateProceed {
			t.Fatalf("gate = %s: %+v", g, js)
		}
	}
	t.Run("email on an account with no opportunity", func(t *testing.T) {
		in := baseInput()
		in.OpportunityID, in.State.OpportunityID, in.Opportunities = nil, nil, nil
		proceeds(t, in)
	})
	t.Run("email-only workspace", func(t *testing.T) {
		in := baseInput()
		in.Policy.AllowedTools = []string{ToolEmailSend}
		proceeds(t, in)
	})
	t.Run("internal slack note at internal_actions autonomy", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionInternalNote
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
		in.Draft.FinishedArtifact = Artifact{Channel: ChannelSlack, Body: "Heads up: Pat asked for the summary of the rollout options."}
		in.Policy.AutonomyLevel = "internal_actions"
		proceeds(t, in)
	})
	t.Run("a stage change is a write and still needs its target", func(t *testing.T) {
		in := crmInput("commercial review")
		in.OpportunityID = nil
		if g := Decide(Evaluate(in)); g != GateStop {
			t.Fatalf("gate = %s, want stop", g)
		}
	})
	t.Run("a due date alone is a write", func(t *testing.T) {
		in := baseInput()
		in.Draft.CRMNextStepIntent.DueAt = ptr(now.Add(72 * time.Hour))
		if !hasCRMWrite(in.Draft) {
			t.Fatal("due_at is a writeback")
		}
	})
}
