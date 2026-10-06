package contracts

import (
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// HAR-130 payload kinds: CRM Task/Quote/Contract/Order records and support cases / chat transcripts.
var replayPayloads = map[string]string{
	"task": `{"kind":"crm_change","object_type":"Task","record_id":"task:00T1","account_record_id":"account:0011",
		"opportunity_record_id":"opp:0061","changed_at":"2023-02-20T00:00:00Z","changed_by":"rep@ghostvendor.com",
		"created":true,"fields":{"Subject":{"new":"Hold negotiation meeting"}},"description":"Finalize pricing."}`,
	"quote": `{"kind":"crm_change","object_type":"Quote","record_id":"quote:0Q01","account_record_id":"account:0011",
		"opportunity_record_id":"opp:0061","changed_at":"2021-01-05T09:15:00Z","changed_by":"integration:crmarena",
		"created":true,"fields":{"GrandTotal":{"new":10331.77}},"description":null}`,
	"support case": `{"kind":"support_case","case_id":"case:5001","account_record_id":"account:0011",
		"contact_record_id":"contact:0031","owner_email":"rep@ghostvendor.com","subject":"Alerts missing",
		"description":"No update notifications.","priority":"Medium","origin":"Email",
		"opened_at":"2020-12-29T08:36:00Z","closed_at":null}`,
	"chat transcript": `{"kind":"chat_transcript","transcript_id":"chat:5701","case_id":"case:5001",
		"account_record_id":"account:0011","contact_record_id":null,"owner_email":"rep@ghostvendor.com",
		"ended_at":"2023-03-08T07:07:30Z","body_text":"[2023-03-08T06:51:22] Jakob (Customer): Hi"}`,
}

func decode(t *testing.T, raw string) any {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestReplayPayloadKindsValidate(t *testing.T) {
	schema := schemaFor(t, compiler(t, contractsDir(t)), "source_payloads")
	for name, raw := range replayPayloads {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(decode(t, raw)); err != nil {
				t.Fatalf("%s payload invalid: %v", name, err)
			}
		})
	}
}

func TestReplayPayloadMutationsAreRejected(t *testing.T) {
	schema := schemaFor(t, compiler(t, contractsDir(t)), "source_payloads")
	bad := map[string]string{
		"unknown crm object type":       strings.Replace(replayPayloads["task"], `"Task"`, `"Invoice"`, 1),
		"case without account":          strings.Replace(replayPayloads["support case"], `"account_record_id":"account:0011",`, "", 1),
		"chat without a transcript":     strings.Replace(replayPayloads["chat transcript"], `"body_text":"[2023-03-08T06:51:22] Jakob (Customer): Hi"`, `"body_text":""`, 1),
		"case with an undeclared field": strings.Replace(replayPayloads["support case"], `"priority"`, `"status":"Closed","priority"`, 1),
	}
	for name, raw := range bad {
		t.Run(name, func(t *testing.T) {
			if err := schema.Validate(decode(t, raw)); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}
