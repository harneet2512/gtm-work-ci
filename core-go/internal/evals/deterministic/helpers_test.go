package deterministic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/schemacheck"
)

// Minimal neutral inputs: one account, one rep, two contacts, one cited activity.
const (
	acct      = "acct-1"
	otherAcct = "acct-2"
	opp       = "opp-1"
	rep       = "emp-1"
	pat       = "p-1"
	sam       = "p-2"
	act1      = "00000000-0000-4000-8000-0000000000a1"
	runID     = "00000000-0000-4000-8000-0000000000b1"
)

var now = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func known(v any) reducer.Field {
	return reducer.Field{Value: v, Known: true, WinningClaimID: ptr("claim-1"),
		EvidenceRefs: []reducer.EvidenceRef{{ActivityID: "act-0"}}}
}

func unknownField() reducer.Field {
	return reducer.Field{Value: "unknown", EvidenceRefs: []reducer.EvidenceRef{}}
}

func baseState() reducer.AccountState {
	var f reducer.Fields
	for _, name := range reducer.FieldNames() {
		*f.Field(name) = unknownField()
	}
	f.Stage = known("Technical evaluation")
	f.Owner = known(rep)
	return reducer.AccountState{
		AccountID: acct, OpportunityID: ptr(opp), Version: 3, AsOf: now.Add(-time.Hour), ComputedAt: now.Add(-time.Hour),
		Fields: f,
		BuyingGroup: []reducer.Member{
			{PersonID: pat, DisplayName: "Pat Lee", Roles: []string{"champion"}, Status: "active", EvidenceRefs: []reducer.EvidenceRef{}},
			{PersonID: sam, DisplayName: "Sam Roe", Roles: []string{"security"}, Status: "active", EvidenceRefs: []reducer.EvidenceRef{}},
		},
		CoverageGaps: []string{},
	}
}

// baseInput is a clean draft every eval passes; tests change one thing at a time.
func baseInput() Input {
	return Input{
		AgentRunID: runID, DraftIndex: 1, WorkspaceID: "ws-1", AccountID: acct, OpportunityID: ptr(opp),
		RunMode: "live", EvaluatedAt: now,
		Draft: Output{
			ProposedActionType: ActionSendEmail,
			Recipients:         []Recipient{{PersonID: pat, Role: "to"}},
			FinishedArtifact: Artifact{Channel: ChannelEmail, Subject: ptr("Next steps"),
				Body: "Hi Pat,\n\nThanks for the call. As you asked, here is the summary of the rollout options.\n\nBest,\nRep"},
			CRMNextStepIntent: CRMIntent{NextStep: "Pat reviews the rollout summary"},
			Reason:            "Pat asked for the summary.",
			EvidenceRefs:      []EvidenceRef{{ActivityID: act1, Quote: "send the summary of the rollout options"}},
		},
		State:    baseState(),
		RunSteps: []RunStep{{Seq: 1, Step: "draft", Status: "succeeded"}},
		People: []Person{
			{PersonID: rep, DisplayName: "Rep One", Kind: KindEmployee, HasEmail: true},
			{PersonID: pat, DisplayName: "Pat Lee", Kind: "contact", AccountID: ptr(acct), HasEmail: true},
			{PersonID: sam, DisplayName: "Sam Roe", Kind: "contact", AccountID: ptr(acct), HasEmail: true},
		},
		Opportunities: []Opportunity{{OpportunityID: opp, AccountID: acct, OwnerPersonID: ptr(rep)}},
		Activities: []Activity{{ActivityID: act1, AccountID: ptr(acct), ActivityType: "EmailReply",
			OccurredAt: now.Add(-time.Hour),
			Text:       "Thanks for the call. Could you send the summary of the rollout options for our 120 seats?"}},
		CRM: CRMRules{Stages: []string{"Discovery", "Technical evaluation", "Commercial review", "Negotiation"},
			TerminalStages: []string{"Closed won", "Closed lost"}, MaxForwardSteps: 1},
		Policy: Policy{WorkspaceID: "ws-1", AutonomyLevel: "customer_facing",
			AllowedTools: []string{ToolEmailSend, ToolCalendarInvite, ToolDocumentShare, ToolSlackPost, ToolCRMNote, ToolCRMUpdate}},
	}
}

// expectFinding asserts exactly one finding of check c with the given blocking flag and detail fragment.
func expectFinding(t *testing.T, got []Finding, c Check, blocking bool, fragment string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want 1 finding of %s, got %d: %+v", c, len(got), got)
	}
	f := got[0]
	if f.Check != c || f.Blocking != blocking || !strings.Contains(f.Detail, fragment) {
		t.Fatalf("finding = %+v; want check %s blocking %v detail containing %q", f, c, blocking, fragment)
	}
}

func expectNone(t *testing.T, got []Finding) {
	t.Helper()
	if len(got) != 0 {
		t.Fatalf("want no findings, got %+v", got)
	}
}

// expectContractValid asserts the result marshals to a document valid against eval_result.v1.json.
func expectContractValid(t *testing.T, r EvalResult) {
	t.Helper()
	v, err := schemacheck.New()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate("eval_result", raw); err != nil {
		t.Fatalf("result violates eval_result.v1.json: %v\n%s", err, raw)
	}
}
