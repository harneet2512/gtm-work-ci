package deterministic

import "testing"

func TestToolAllowed(t *testing.T) {
	t.Run("email with email_send passes", func(t *testing.T) { expectNone(t, ToolAllowed(baseInput())) })
	t.Run("tool missing from the policy stops", func(t *testing.T) {
		in := baseInput()
		in.Policy.AllowedTools = []string{ToolSlackPost, ToolCRMUpdate}
		got := ToolAllowed(in)
		expectFinding(t, got, CheckToolAllowed, true, "email_send")
		if got[0].Remedy != RemedyStop {
			t.Fatalf("remedy = %s", got[0].Remedy)
		}
	})
	t.Run("crm write needs crm_update", func(t *testing.T) {
		in := crmInput("commercial review")
		in.Policy.AllowedTools = []string{ToolEmailSend}
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "crm_update")
	})
	t.Run("internal note to a customer is the wrong tool", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionInternalNote
		in.Draft.FinishedArtifact.Channel = ChannelSlack
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "addresses a customer")
	})
	t.Run("email action over slack is the wrong tool", func(t *testing.T) {
		in := baseInput()
		in.Draft.FinishedArtifact.Channel = ChannelSlack
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "slack")
	})
	t.Run("internal note by email is the wrong tool", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionInternalNote
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "email channel")
	})
	t.Run("internal slack note to staff passes", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionInternalNote
		in.Draft.FinishedArtifact.Channel = ChannelSlack
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
		expectNone(t, ToolAllowed(in))
	})
	t.Run("meeting and document tools are checked", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionScheduleMeeting
		in.Policy.AllowedTools = []string{ToolEmailSend, ToolCRMUpdate}
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "calendar_invite")
		in.Draft.ProposedActionType = ActionShareDocument
		expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "document_share")
	})
	t.Run("wait needs no tool", func(t *testing.T) {
		in := baseInput()
		in.Draft.ProposedActionType = ActionWait
		in.Draft.CRMNextStepIntent = CRMIntent{}
		in.Policy.AllowedTools = nil
		expectNone(t, ToolAllowed(in))
	})
}

func TestAccountWorkspaceCorrect(t *testing.T) {
	t.Run("matching scope passes", func(t *testing.T) { expectNone(t, AccountWorkspaceCorrect(baseInput())) })
	t.Run("workspace mismatch stops", func(t *testing.T) {
		in := baseInput()
		in.Policy.WorkspaceID = "ws-2"
		expectFinding(t, AccountWorkspaceCorrect(in), CheckScopeCorrect, true, "workspace")
	})
	t.Run("account outside the allowed list stops", func(t *testing.T) {
		in := baseInput()
		in.Policy.AccountIDs = []string{otherAcct}
		expectFinding(t, AccountWorkspaceCorrect(in), CheckScopeCorrect, true, "may act on")
	})
	t.Run("listed account passes", func(t *testing.T) {
		in := baseInput()
		in.Policy.AccountIDs = []string{acct}
		expectNone(t, AccountWorkspaceCorrect(in))
	})
	t.Run("state of another account stops", func(t *testing.T) {
		in := baseInput()
		in.State.AccountID = otherAcct
		expectFinding(t, AccountWorkspaceCorrect(in), CheckScopeCorrect, true, "state of account")
	})
}

func TestWithinAutonomy(t *testing.T) {
	levels := []struct {
		level  string
		action string
		crm    bool
		ok     bool
	}{
		{"customer_facing", ActionSendEmail, false, true},
		{"crm_writeback", ActionSendEmail, false, false},
		{"crm_writeback", ActionNoAction, true, true},
		{"internal_actions", ActionNoAction, true, false},
		{"internal_actions", ActionInternalNote, false, true},
		{"suggest_only", ActionInternalNote, false, false},
		{"suggest_only", ActionWait, false, true},
		{"suggest_only", ActionNoAction, true, false}, // any CRM write needs crm_writeback
	}
	for _, c := range levels {
		in := baseInput()
		in.Draft.ProposedActionType = c.action
		in.Draft.CRMNextStepIntent = CRMIntent{}
		if c.crm {
			in.Draft.CRMNextStepIntent.StageChange = ptr("Negotiation")
		}
		in.Policy.AutonomyLevel = c.level
		got := WithinAutonomy(in)
		if c.ok {
			expectNone(t, got)
		} else {
			expectFinding(t, got, CheckAutonomyPermitted, true, "allows "+c.level)
		}
	}
	t.Run("unknown level stops", func(t *testing.T) {
		in := baseInput()
		in.Policy.AutonomyLevel = "root"
		expectFinding(t, WithinAutonomy(in), CheckAutonomyPermitted, true, "unknown autonomy")
	})
}

func TestDryRunCannotWrite(t *testing.T) {
	dry := func(steps ...RunStep) Input {
		in := baseInput()
		in.RunMode, in.ExecuteMode = RunModeDryRun, ExecuteRecord
		in.RunSteps = steps
		return in
	}
	t.Run("recorded execute passes", func(t *testing.T) {
		expectNone(t, DryRunCannotWrite(dry(RunStep{Seq: 1, Step: "execute", Status: "recorded"})))
	})
	t.Run("external effect stops", func(t *testing.T) {
		expectFinding(t, DryRunCannotWrite(dry(RunStep{Seq: 1, Step: "execute", Status: "recorded", ExternalEffectID: ptr("msg-1")})),
			CheckDryRunInert, true, "external effect")
	})
	t.Run("executed step stops", func(t *testing.T) {
		expectFinding(t, DryRunCannotWrite(dry(RunStep{Seq: 2, Step: "execute", Status: "succeeded"})), CheckDryRunInert, true, "executed")
	})
	t.Run("live runs are not restricted", func(t *testing.T) {
		in := baseInput()
		in.RunSteps = []RunStep{{Seq: 1, Step: "execute", Status: "succeeded", ExternalEffectID: ptr("msg-1")}}
		expectNone(t, DryRunCannotWrite(in))
	})
	// The intended action is judged before anything ran.
	t.Run("a declared perform executor stops before execution", func(t *testing.T) {
		in := baseInput()
		in.RunMode, in.ExecuteMode = RunModeDryRun, ExecutePerform
		in.RunSteps = []RunStep{{Seq: 2, Step: "execute", Status: "pending"}}
		got := DryRunCannotWrite(in)
		expectFinding(t, got, CheckDryRunInert, true, "would perform")
		if got[0].Remedy != RemedyStop {
			t.Fatalf("remedy = %s", got[0].Remedy)
		}
	})
	t.Run("CRM write intent under a perform executor stops", func(t *testing.T) {
		in := crmInput("commercial review")
		in.Draft.ProposedActionType = ActionNoAction
		in.RunMode, in.ExecuteMode = RunModeDryRun, ExecutePerform
		expectFinding(t, DryRunCannotWrite(in), CheckDryRunInert, true, "no_action")
	})
	t.Run("an absent execute_mode under dry_run is record_only: the default config sends nothing and stops nothing", func(t *testing.T) {
		in := baseInput()
		in.RunMode = RunModeDryRun
		expectNone(t, DryRunCannotWrite(in))
		if g := Decide(Evaluate(in)); g != GateProceed {
			t.Fatalf("a default-mode (dry_run) email must proceed, got %s", g)
		}
	})
	t.Run("a dry run that only waits has nothing to record", func(t *testing.T) {
		in := baseInput()
		in.RunMode = RunModeDryRun
		in.Draft.ProposedActionType = ActionWait
		in.Draft.CRMNextStepIntent = CRMIntent{}
		expectNone(t, DryRunCannotWrite(in))
	})
	t.Run("record-only dry run of an email passes", func(t *testing.T) { expectNone(t, DryRunCannotWrite(dry())) })
}

func TestConfidentialSharingConditionMet(t *testing.T) {
	asset := func(met bool) Input {
		in := baseInput()
		in.Draft.FinishedArtifact.Attachments = []string{"Security report"}
		in.Assets = []Asset{{Name: "Security report", Available: true, Confidential: true,
			ShareCondition: ptr("an NDA is signed"), ShareConditionMet: met}}
		return in
	}
	t.Run("condition met passes", func(t *testing.T) { expectNone(t, ConfidentialSharingConditionMet(asset(true))) })
	t.Run("condition unmet blocks", func(t *testing.T) {
		expectFinding(t, ConfidentialSharingConditionMet(asset(false)), CheckConfidentialShare, true, "NDA")
	})
	t.Run("internal recipients may receive it", func(t *testing.T) {
		in := asset(false)
		in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
		expectNone(t, ConfidentialSharingConditionMet(in))
	})
}

func TestPermissionPolicyResult(t *testing.T) {
	pass := PermissionPolicy(baseInput())
	if pass.Result.Verdict != "pass" || len(pass.Checks) != 5 {
		t.Fatalf("pass = %+v", pass.Result)
	}
	expectContractValid(t, pass.Result)
	in := baseInput()
	in.Policy.AutonomyLevel = "suggest_only"
	fail := PermissionPolicy(in)
	if fail.Result.Verdict != "fail" || !fail.Result.Blocking || fail.Findings[0].Remedy != RemedyStop {
		t.Fatalf("fail = %+v", fail.Result)
	}
	expectContractValid(t, fail.Result)
}

func TestConfidentialAssetsAreFoundWhereverTheyAreMentioned(t *testing.T) {
	soc2 := Asset{Name: "SOC2 Type II report", Aliases: []string{"pen-test summary"}, Available: true, Confidential: true,
		ShareCondition: ptr("the NDA is countersigned")}
	with := func(body string, attach ...string) Input {
		in := baseInput()
		in.Assets = []Asset{soc2}
		in.Draft.FinishedArtifact.Body = body
		in.Draft.FinishedArtifact.Attachments = attach
		return in
	}
	t.Run("named in the body", func(t *testing.T) {
		expectFinding(t, ConfidentialSharingConditionMet(with("Here is our SOC2 Type II report.")), CheckConfidentialShare, true, "SOC2")
	})
	t.Run("alias in the body", func(t *testing.T) {
		expectFinding(t, ConfidentialSharingConditionMet(with("Sharing the pen-test summary now.")), CheckConfidentialShare, true, "SOC2")
	})
	t.Run("link pasted in the body", func(t *testing.T) {
		in := with("Report: gdrive:soc2-type-ii-report-2026")
		in.Assets[0].Name = "SOC2 Type II report"
		expectFinding(t, ConfidentialSharingConditionMet(in), CheckConfidentialShare, true, "SOC2")
		in = with("Report: https://files.example.test/soc2-type-ii-report.pdf")
		expectFinding(t, ConfidentialSharingConditionMet(in), CheckConfidentialShare, true, "SOC2")
	})
	t.Run("an unrelated link passes", func(t *testing.T) {
		expectNone(t, ConfidentialSharingConditionMet(with("Agenda: https://files.example.test/agenda.pdf")))
	})
	t.Run("named once and attached is one finding", func(t *testing.T) {
		expectFinding(t, ConfidentialSharingConditionMet(with("Attached is the SOC2 Type II report.", "SOC2 Type II report")), CheckConfidentialShare, true, "SOC2")
	})
	t.Run("condition met passes everywhere", func(t *testing.T) {
		in := with("Here is our SOC2 Type II report: gdrive:soc2-type-ii-report")
		in.Assets[0].ShareConditionMet = true
		expectNone(t, ConfidentialSharingConditionMet(in))
	})
}

func TestShareDocumentChannelMustReachTheCustomer(t *testing.T) {
	in := baseInput()
	in.Draft.ProposedActionType = ActionShareDocument
	in.Draft.FinishedArtifact.Channel = ChannelSlack
	expectFinding(t, ToolAllowed(in), CheckToolAllowed, true, "internal")
	in.Draft.FinishedArtifact.Channel = ChannelEmail
	expectNone(t, ToolAllowed(in))
	in.Draft.FinishedArtifact.Channel = ChannelSlack
	in.Draft.Recipients = []Recipient{{PersonID: rep, Role: "to"}}
	expectNone(t, ToolAllowed(in))
}
