package deterministic

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// lineCase is an executable case pair for one HAR-97 line: Good must not trip the check and Bad must.
// Sub-check rows run Find and expect a finding of the row's Check on Bad; eval rows run Eval and
// expect verdict pass on Good and fail on Bad.
type lineCase struct {
	Good func() Input
	Bad  func() Input
	Find func(Input) []Finding
	Eval func(Input) Judgment
}

func edit(f func(*Input)) func() Input {
	return func() Input { in := baseInput(); f(&in); return in }
}

func priced(body string) func() Input { return func() Input { return pricedInput(body) } }

func dryRunSend() Input {
	in := baseInput()
	in.RunMode = RunModeDryRun
	return in
}

func internalSecret(asset Asset) func() Input {
	return edit(func(in *Input) {
		in.Assets = []Asset{asset}
		in.Draft.FinishedArtifact.Body = "Here is our SOC2 Type II report."
	})
}

var secretAsset = Asset{Name: "SOC2 Type II report", Available: true, Confidential: true, ShareCondition: ptr("the NDA is countersigned")}

var lineCases = map[string]lineCase{
	"Recipient correctness": {Eval: RecipientCorrectness, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}} })},
	"recipients exist": {Find: RecipientsExist, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.Recipients = []Recipient{{PersonID: "p-404", Role: "to"}} })},
	"recipient belongs to intended account": {Find: RecipientsBelongToAccount, Good: baseInput,
		Bad: edit(func(in *Input) { in.People[1].AccountID = ptr(otherAcct) })},
	"no duplicate recipients": {Find: RecipientsNotDuplicated, Good: baseInput,
		Bad: edit(func(in *Input) {
			in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: pat, Role: "cc"})
		})},
	"no internal-only contact accidentally externalized": {Find: InternalOnlyNotExternalized, Good: baseInput,
		Bad: edit(func(in *Input) {
			in.People = append(in.People, Person{PersonID: "emp-dd", DisplayName: "Deal Desk", Kind: KindEmployee, HasEmail: true, InternalOnly: true})
			in.Draft.Recipients = append(in.Draft.Recipients, Recipient{PersonID: "emp-dd", Role: "cc"})
		})},

	"Date / commitment consistency": {Eval: DateCommitmentConsistency, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.WaitUntil = ptr(now.Add(-48 * time.Hour)) })},
	"dates match current state": {Find: DatesMatchState, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.FinishedArtifact.Body = "See you on October 8." })},
	"meeting time is not stale": {Find: MeetingTimeNotStale, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.FinishedArtifact.Body = "Hi Pat, could we meet on September 22 to review?" })},
	"action does not contradict a commitment already made": {Find: CommitmentNotContradicted,
		Good: edit(func(in *Input) { in.State.Fields.CurrentCommitments = dueCommitment("2026-10-01T00:00:00Z") }),
		Bad: edit(func(in *Input) {
			in.State.Fields.CurrentCommitments = dueCommitment("2026-10-01T00:00:00Z")
			in.Draft.ProposedActionType, in.Draft.WaitUntil = ActionWait, ptr(now.Add(7*24*time.Hour))
		})},
	"promised asset exists if referenced": {Find: PromisedAssetExists, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.FinishedArtifact.Body = "Attached is the proposal." })},

	"Number / product / pricing integrity": {Eval: PricingIntegrity, Good: priced("Alpha is $40 per seat for 120 seats."),
		Bad: priced("Alpha is $55 per seat.")},
	"no unsupported price": {Find: PriceSupported, Good: priced("Alpha is $40 per seat."), Bad: priced("Alpha is $55 per seat.")},
	"no invented discount": {Find: DiscountNotInvented, Good: priced("We can do 10% off the list price."),
		Bad: priced("We can do 15% off the list price.")},
	"correct product / plan / region": {Find: ProductPlanRegionCorrect, Good: priced("Alpha Scale is available in the EU."),
		Bad: priced("Beta Scale suits you.")},
	"quantities match source evidence": {Find: QuantitiesMatchEvidence, Good: priced("That covers 120 seats."),
		Bad: priced("That covers 150 seats.")},

	"CRM/writeback consistency": {Eval: CRMWriteback, Good: func() Input { return crmInput("commercial review") },
		Bad: func() Input { return crmInput("Closed won") }},
	"stage update is legal": {Find: StageUpdateLegal, Good: func() Input { return crmInput("commercial review") },
		Bad: func() Input { return crmInput("Negotiation") }},
	"owner exists": {Find: OwnerExists, Good: func() Input { return crmInput("commercial review") },
		Bad: func() Input {
			in := crmInput("commercial review")
			in.Opportunities[0].OwnerPersonID = ptr("emp-404")
			return in
		}},
	"next step does not overwrite newer state": {Find: NextStepDoesNotOverwrite, Good: func() Input { return crmInput("commercial review") },
		Bad: func() Input {
			in := crmInput("commercial review")
			in.State.Fields.Stage.Standing, in.State.Fields.Stage.AsOf = ptr("human_approved"), ptr(now)
			return in
		}},
	"writeback references correct opportunity/account": {Find: WritebackTargetCorrect, Good: func() Input { return crmInput("commercial review") },
		Bad: func() Input {
			in := crmInput("commercial review")
			in.Opportunities[0].AccountID = otherAcct
			return in
		}},

	"Duplicate-action detection": {Eval: DuplicateAction, Good: baseInput,
		Bad: edit(func(in *Input) {
			in.Draft.FinishedArtifact.Body = priorEmailBody
			in.PriorActions = []PriorAction{priorEmail(time.Hour, priorEmailBody)}
		})},
	"same email was not already sent": {Find: EmailNotAlreadySent, Good: baseInput,
		Bad: edit(func(in *Input) {
			in.Draft.FinishedArtifact.Body = priorEmailBody
			in.PriorActions = []PriorAction{priorEmail(time.Hour, priorEmailBody)}
		})},
	"meeting was not already scheduled": {Find: MeetingNotAlreadyScheduled,
		Good: func() Input { in := meetingInput(now.Add(-48 * time.Hour)); return in },
		Bad:  func() Input { return meetingInput(now.Add(48 * time.Hour)) }},
	"CRM action was not already completed": {Find: CRMActionNotAlreadyCompleted, Good: func() Input { return crmInput("Commercial review") },
		Bad: func() Input { return crmInput("technical evaluation") }},

	"Provenance / source coverage": {Find: CriticalStatementsSupported, Good: baseInput,
		Bad: edit(func(in *Input) { in.Draft.FinishedArtifact.Body = "The total is $4,800." })},

	"Permission / tool policy": {Eval: PermissionPolicy, Good: baseInput,
		Bad: edit(func(in *Input) { in.Policy.AutonomyLevel = "suggest_only" })},
	"correct allowed tool": {Find: ToolAllowed, Good: baseInput,
		Bad: edit(func(in *Input) { in.Policy.AllowedTools = []string{ToolSlackPost} })},
	"correct account/workspace": {Find: AccountWorkspaceCorrect, Good: baseInput,
		Bad: edit(func(in *Input) { in.Policy.WorkspaceID = "ws-2" })},
	"action is within permitted autonomy level": {Find: WithinAutonomy, Good: baseInput,
		Bad: edit(func(in *Input) { in.Policy.AutonomyLevel = "crm_writeback" })},
	"test-mode runs cannot send or write": {Find: DryRunCannotWrite,
		Good: dryRunSend,
		Bad:  func() Input { in := dryRunSend(); in.ExecuteMode = ExecutePerform; return in }},
}

func dueCommitment(due string) reducer.Field {
	return known([]reducer.Item{{Text: "Send pricing proposal", Status: "open", OwnerPersonID: rep, DueAt: ptr(at(due))}})
}

// Every one of the 30 lines has an executed case that passes and one that fails, and the failing
// case is reported under the row's own check (or a failing verdict for an eval heading).
func TestEveryLineHasAPassingAndAFailingExecutedCase(t *testing.T) {
	if len(lineCases) != len(traceRows) {
		t.Fatalf("%d executable cases for %d lines", len(lineCases), len(traceRows))
	}
	for _, r := range traceRows {
		c, ok := lineCases[r.Line]
		if !ok {
			t.Errorf("no executable case for line %q", r.Line)
			continue
		}
		if c.Eval != nil {
			if v := c.Eval(c.Good()).Result.Verdict; v != "pass" {
				t.Errorf("%q: good case verdict = %s", r.Line, v)
			}
			if v := c.Eval(c.Bad()).Result.Verdict; v != "fail" {
				t.Errorf("%q: bad case verdict = %s", r.Line, v)
			}
			continue
		}
		if got := c.Find(c.Good()); len(got) != 0 {
			t.Errorf("%q: good case flagged %+v", r.Line, got)
		}
		bad := c.Find(c.Bad())
		if len(bad) == 0 {
			t.Errorf("%q: bad case was not flagged", r.Line)
		}
		for _, f := range bad {
			if f.Check != r.Check {
				t.Errorf("%q: finding is for %s, want %s", r.Line, f.Check, r.Check)
			}
		}
	}
}

func TestConfidentialSharingHasExecutedPassAndFailCases(t *testing.T) {
	if got := ConfidentialSharingConditionMet(internalSecret(secretAsset)()); len(got) != 1 {
		t.Fatalf("unmet condition must be flagged: %+v", got)
	}
	met := secretAsset
	met.ShareConditionMet = true
	expectNone(t, ConfidentialSharingConditionMet(internalSecret(met)()))
}
