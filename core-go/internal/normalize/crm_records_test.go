package normalize

import (
	"fmt"
	"testing"
)

// recordJSON renders a created crm_change for a Task/Quote/Contract/Order record (HAR-130 rows).
func recordJSON(objectType, recordID, opp, description string) string {
	oppRef := "null"
	if opp != "" {
		oppRef = fmt.Sprintf("%q", opp)
	}
	desc := "null"
	if description != "" {
		desc = fmt.Sprintf("%q", description)
	}
	return crmJSON(objectType, recordID, "account:A1", "rep@ghostvendor.com", `{"Subject":{"new":"Hold negotiation meeting"}}`,
		fmt.Sprintf(`,"created":true,"opportunity_record_id":%s,"description":%s`, oppRef, desc))
}

func TestNormalizeCRMReplayRecords(t *testing.T) {
	rep := []Participant{part("rep@ghostvendor.com", "", "actor")}
	rec := func(typ, summary, body string) want {
		return want{typ: typ, occurred: crmChangedAt, participants: rep, accountKind: HintCRM, accountHint: "account:A1",
			oppHint: "opp:O1", summary: summary, body: body}
	}
	runCases(t, []normCase{
		{
			name: "Task created -> CRMTaskLogged with its description as body and its opportunity as hint",
			ev:   event(t, "crm", "task:T1", "created", "", recordJSON("Task", "task:T1", "opp:O1", "Finalize pricing.")),
			want: rec("CRMTaskLogged", "Task task:T1 logged: Hold negotiation meeting", "Finalize pricing."),
		},
		{
			name: "Quote created -> QuoteCreated",
			ev:   event(t, "crm", "quote:Q1", "created", "", recordJSON("Quote", "quote:Q1", "opp:O1", "Initial quote.")),
			want: rec("QuoteCreated", "Quote quote:Q1 created", "Initial quote."),
		},
		{
			name: "Contract created -> ContractSigned",
			ev:   event(t, "crm", "contract:C1", "created", "", recordJSON("Contract", "contract:C1", "opp:O1", "")),
			want: rec("ContractSigned", "Contract contract:C1 signed", ""),
		},
		{
			name: "Order created without an opportunity -> OrderPlaced on the account only",
			ev:   event(t, "crm", "order:R1", "created", "", recordJSON("Order", "order:R1", "", "")),
			want: want{typ: "OrderPlaced", occurred: crmChangedAt, participants: rep, accountKind: HintCRM,
				accountHint: "account:A1", summary: "Order order:R1 placed"},
		},
		{
			name: "Quote status change -> CRMFieldChanged hinted by the quote's opportunity",
			ev: event(t, "crm", "quote:Q1", "field:Status:Accepted", "", crmJSON("Quote", "quote:Q1", "account:A1", "integration:crmarena",
				`{"Status":{"new":"Accepted"}}`, `,"opportunity_record_id":"opp:O1"`)),
			want: want{typ: "CRMFieldChanged", occurred: crmChangedAt, participants: []Participant{part("integration:crmarena", "", "actor")},
				accountKind: HintCRM, accountHint: "account:A1", oppHint: "opp:O1", summary: "quote:Q1 Status changed to Accepted"},
		},
		{
			name: "Opportunity created without a stage -> CRMFieldChanged",
			ev: event(t, "crm", "opp:O1", "created", "", crmJSON("Opportunity", "opp:O1", "account:A1", "rep@ghostvendor.com",
				`{"Name":{"new":"EDA expansion"},"OwnerEmail":{"new":"rep@ghostvendor.com"}}`, `,"created":true`)),
			want: want{typ: "CRMFieldChanged", occurred: crmChangedAt, participants: rep, accountKind: HintCRM,
				accountHint: "account:A1", oppHint: "opp:O1", summary: "Opportunity opp:O1 created"},
		},
	})
}

func TestNormalizeCRMReplayRecordsRejectInvalidInput(t *testing.T) {
	withStage := crmJSON("Opportunity", "opp:O1", "account:A1", "rep@ghostvendor.com", `{"StageName":{"new":"Quote"}}`, `,"created":true`)
	oppOnOpp := crmJSON("Opportunity", "opp:O1", "account:A1", "rep@ghostvendor.com", `{"Amount":{"new":5}}`, `,"opportunity_record_id":"opp:O2"`)
	runCases(t, []normCase{
		{name: "Opportunity created with a stage must be keyed by it", ev: event(t, "crm", "opp:O1", "created", "", withStage), errCode: CodeInvalidEvent},
		{name: "an Opportunity record cannot name another opportunity", ev: event(t, "crm", "opp:O1", "field:Amount:5", "", oppOnOpp), errCode: CodeInvalidEvent},
		{name: "Task without created flag", ev: event(t, "crm", "task:T1", "created", "", crmJSON("Task", "task:T1", "account:A1", "rep@ghostvendor.com", "", "")), errCode: CodeInvalidEvent},
	})
}
