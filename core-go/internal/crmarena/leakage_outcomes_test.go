package crmarena

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Outcome leakage (HAR-130 review M3). The source holds some values only as of the snapshot, after
// every outcome: the final stage, the amount, the close date, a quote's final status. The loader dates
// them at the deal's last event. These tables say which fields are which, so that a value that carries an
// outcome cannot reach an event earlier than the deal's last one by accident.

// snapshotOnlyFields may appear on a non-creation (change) event of a deal, with the reason. Any other
// field on a change event is a failure: a new field there must be classified here first.
var snapshotOnlyFields = map[string]string{
	"Opportunity.StageName": "the source has no stage history: the final stage is emitted once, dated at the deal's last event",
	"Opportunity.Amount":    "the source holds the final amount only; emitted with the final stage",
	"Opportunity.CloseDate": "the source holds the final close date only; emitted with the final stage",
	"Quote.Status":          "the source has no status history: the final status is emitted once, dated at the deal's last event",
}

// outcomeBearingFields are the fields whose value says how the deal or its quote ended.
var outcomeBearingFields = map[string]string{
	"Opportunity.StageName": "final stage",
	"Opportunity.Amount":    "final amount",
	"Opportunity.CloseDate": "final close date",
	"Quote.Status":          "final quote status (Accepted, Rejected, ...)",
	"Contract.Status":       "contract status",
	"Order.Status":          "order status",
	"support.closed_at":     "when the case was closed",
	"Quote.GrandTotal":      "equals the final Opportunity.Amount on every pre-cutoff quote of the sample's current deals",
	"Quote.Discount":        "fixes the final amount together with GrandTotal",
}

// outcomeAllowed lists the outcome-bearing fields that may sit on an event dated before the deal's
// last event, with the reason. Everything else outcome-bearing there is a leak.
var outcomeAllowed = map[string]string{
	"Contract.Status": "the contract's creation event is dated at its signature, when its status is true; a later email does not make the status early",
	"Order.Status":    "orders are account-level and name no deal; their status is dated at the order's effective date",
	"Quote.GrandTotal": "a quote's total is known when the quote is created, but it equals the final Opportunity.Amount on all 181 pre-cutoff " +
		"quotes of current deals, so it discloses the final amount by construction: a property of the data, not a loader leak",
	"support.closed_at": "on the case's closing event only (outcomeAllowedOnlyOn): that event is dated at ClosedDate, so the value is not early",
	"Quote.Discount":    "known when the quote is created; with GrandTotal it fixes the final amount",
}

// outcomeAllowedOnlyOn restricts an allowed field to one event key: the close date of a case is
// allowed on the event that closes it, which is dated at that close, and nowhere else.
var outcomeAllowedOnlyOn = map[string]string{"support.closed_at": "closed"}

// staticFields are record attributes the source holds only as of the snapshot and the loader stamps on the
// creation event: they describe the record, not how a deal ended, and are not outcome labels.
var staticFields = map[string]string{
	"Account.Description": "static account attribute", "Account.Domain": "derived from the contacts' addresses",
	"Account.Industry": "static account attribute", "Account.Name": "static account attribute", "Account.NumberOfEmployees": "static account attribute",
	"Contact.Department": "static contact attribute", "Contact.Email": "static contact attribute", "Contact.FirstName": "static contact attribute",
	"Contact.LastName": "static contact attribute", "Contact.Title": "static contact attribute",
	"Contract.ContractNumber": "contract identity", "Contract.ContractTerm": "term agreed at signing", "Contract.EndDate": "term agreed at signing; redacted when after the cutoff",
	"Contract.StartDate":      "term agreed at signing; redacted when after the cutoff",
	"Opportunity.Description": "static deal attribute", "Opportunity.Name": "static deal attribute", "Opportunity.OwnerEmail": "static deal attribute",
	"Order.EffectiveDate": "the order's own date", "Order.OrderNumber": "order identity",
	"Quote.ExpirationDate": "quote term; redacted when after the cutoff", "Quote.Name": "quote identity", "Quote.QuoteNumber": "quote identity",
	"Task.ActivityDate": "the task's own date", "Task.Priority": "task attribute", "Task.Subject": "task attribute", "Task.Type": "task attribute",
	"email.kind": "payload discriminator", "support.kind": "payload discriminator",
	"email.body_text": "message text", "email.cc": "message envelope", "email.crm_opportunity_ref": "deal link", "email.date": "the message's own date",
	"email.direction": "derived from the sender", "email.from": "message envelope", "email.in_reply_to": "derived reply link", "email.message_id": "identity",
	"email.subject": "message text", "email.thread_id": "deal link", "email.to": "message envelope",
	"support.account_record_id": "link", "support.body_text": "chat text", "support.case_id": "identity", "support.contact_record_id": "link",
	"support.description": "case text", "support.ended_at": "the chat's own date", "support.opened_at": "the case's own date", "support.origin": "case attribute",
	"support.owner_email": "case attribute", "support.priority": "case attribute", "support.subject": "case text", "support.transcript_id": "identity",
}

// outcomeValues lists every outcome-bearing field an event carries, whatever the type of its value
// (string, number, boolean, object, array). A JSON null is no value.
func outcomeValues(t testing.TB, e Event) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.Source.Payload, &p); err != nil {
		t.Fatal(err)
	}
	object, _ := p["object_type"].(string)
	if object == "" {
		object = e.Source.SourceSystem
	}
	found := map[string]any{}
	if fields, ok := p["fields"].(map[string]any); ok {
		for name, change := range fields {
			if m, ok := change.(map[string]any); ok && m["new"] != nil {
				found[object+"."+name] = m["new"]
			}
		}
	}
	for name, v := range p {
		if name == "fields" || v == nil {
			continue
		}
		if e.Source.SourceSystem == "crm" { // the envelope of a crm_change: never confused with a record field of the same name
			found[envelopePrefix+name] = v
		} else {
			found[object+"."+name] = v
		}
	}
	return found
}

// lastEvents is the time of every deal's last event of any kind.
func lastEvents(events []Event) map[string]Event {
	last := map[string]Event{}
	for _, e := range events {
		if e.DealID != "" && (last[e.DealID].Source.OccurredAt == nil || e.OccurredAt().After(last[e.DealID].OccurredAt())) {
			last[e.DealID] = e
		}
	}
	return last
}

// outcomeLeaks reports every outcome-bearing value on an event dated before its deal's last event
// that is not allow-listed, and every non-snapshot-only field on a change event.
func outcomeLeaks(t testing.TB, events []Event) []string {
	t.Helper()
	last := lastEvents(events)
	var leaks []string
	for _, e := range events {
		var p struct {
			Created bool `json:"created"`
		}
		if err := json.Unmarshal(e.Source.Payload, &p); err != nil {
			t.Fatal(err)
		}
		isChange := e.DealID != "" && e.Source.SourceSystem == "crm" && !p.Created
		for name, v := range outcomeValues(t, e) {
			where := fmt.Sprintf("deal %s %s/%s at %s: %s = %v (%T)", e.DealID, e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt().Format(sfDate), name, v, v)
			if isChange && !isStructural(name) && snapshotOnlyFields[name] == "" {
				leaks = append(leaks, where+": not an allow-listed snapshot-only field on a change event")
			}
			if _, outcome := outcomeBearingFields[name]; !outcome {
				continue
			}
			allowed := outcomeAllowed[name] != "" && (outcomeAllowedOnlyOn[name] == "" || outcomeAllowedOnlyOn[name] == e.Source.SourceEventKey)
			switch {
			case allowed:
			case e.DealID == "":
				leaks = append(leaks, where+": outcome-bearing value on a deal-less event that is not allow-listed")
			case e.OccurredAt().Before(last[e.DealID].OccurredAt()):
				leaks = append(leaks, where+": outcome-bearing value before the deal's last event")
			}
		}
	}
	return leaks
}

// envelopePrefix names the top-level keys of a crm_change payload (kind, record_id, changed_at,
// description, ...). A key of the same name inside "fields" is a record field and is classified on its own:
// "description" at the top level is the record's text, "fields.description" is not structural.
const envelopePrefix = "envelope."

// structuralEnvelopeKeys are the top-level keys of a crm_change payload that carry no record value.
var structuralEnvelopeKeys = map[string]bool{"kind": true, "object_type": true, "record_id": true, "account_record_id": true,
	"opportunity_record_id": true, "changed_at": true, "changed_by": true, "created": true, "description": true}

// isStructural is a top-level structural key of a crm_change payload; only those, never a field.
func isStructural(qualified string) bool {
	key, ok := strings.CutPrefix(qualified, envelopePrefix)
	return ok && structuralEnvelopeKeys[key]
}

// unclassifiedFields makes the leakage check closed-world: every field of every event, including the
// creation events and the deal-less ones, must be structural, static, snapshot-only or outcome-bearing
// (with a reason in one of the tables). A field the tables do not know fails, so a new mapping cannot
// carry an outcome by default.
func unclassifiedFields(t testing.TB, events []Event) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, e := range events {
		for name := range outcomeValues(t, e) {
			_, outcome := outcomeBearingFields[name]
			_, outcomeOK := outcomeAllowed[name]
			if !isStructural(name) && staticFields[name] == "" && snapshotOnlyFields[name] == "" && !outcome && !outcomeOK {
				seen[name] = true
			}
		}
	}
	var out []string
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestAllowListsCarryAReasonForEveryEntry(t *testing.T) {
	for _, table := range []map[string]string{snapshotOnlyFields, outcomeBearingFields, outcomeAllowed, staticFields} {
		for name, reason := range table {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("allow-list entry %s has no reason", name)
			}
		}
	}
	for name := range outcomeAllowed {
		if _, ok := outcomeBearingFields[name]; !ok {
			t.Errorf("%s is allowed but is not outcome-bearing", name)
		}
	}
	for name := range snapshotOnlyFields {
		if _, ok := outcomeBearingFields[name]; !ok {
			t.Errorf("snapshot-only field %s is not listed as outcome-bearing", name)
		}
	}
}

func TestNoOutcomeBearingValueBeforeTheDealsLastEvent(t *testing.T) {
	for name, s := range map[string]Snapshot{"tiny": tiny(), "sample": sample(t)} {
		events := build(t, s).Events
		if leaks := outcomeLeaks(t, events); len(leaks) > 0 {
			t.Errorf("%s: %d outcome leaks, first: %s", name, len(leaks), leaks[0])
		}
		seen := 0
		for _, e := range events {
			seen += len(outcomeValues(t, e))
		}
		if seen == 0 {
			t.Fatalf("%s: no outcome-bearing value found; the check would pass vacuously", name)
		}
	}
}

// TestOutcomeLeakCheckCatchesAValueOfAnyType plants an outcome-bearing value on the deal's creation
// event (dated before its last event) as a string, a number, a boolean, an object and a list.
func TestOutcomeLeakCheckCatchesAValueOfAnyType(t *testing.T) {
	values := map[string]any{"string": "Closed", "number": 125000.5, "bool": true, "object": map[string]any{"x": 1}, "list": []any{"Closed"}}
	for kind, v := range values {
		t.Run(kind, func(t *testing.T) {
			events := build(t, tiny()).Events
			planted := false
			for i, e := range events {
				if e.DealID != "C" || e.Source.SourceObjectID != "opp:C" || e.Source.SourceEventKey != "created" {
					continue
				}
				var p map[string]any
				if err := json.Unmarshal(e.Source.Payload, &p); err != nil {
					t.Fatal(err)
				}
				p["fields"].(map[string]any)["Amount"] = map[string]any{"new": v}
				raw, _ := json.Marshal(p)
				events[i].Source.Payload = raw
				planted = true
			}
			if !planted {
				t.Fatal("creation event of deal C not found")
			}
			leaks := outcomeLeaks(t, events)
			if len(leaks) == 0 || !strings.Contains(strings.Join(leaks, "\n"), "Opportunity.Amount") {
				t.Fatalf("a %s Amount on the creation event was not caught: %v", kind, leaks)
			}
		})
	}
}

func TestSnapshotOnlyFieldOnAnUnlistedChangeEventIsCaught(t *testing.T) {
	events := build(t, tiny()).Events
	for i, e := range events {
		if e.Source.SourceObjectID == "opp:C" && strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:") {
			var p map[string]any
			_ = json.Unmarshal(e.Source.Payload, &p)
			p["fields"].(map[string]any)["Description"] = map[string]any{"new": "unclassified"}
			raw, _ := json.Marshal(p)
			events[i].Source.Payload = raw
		}
	}
	leaks := outcomeLeaks(t, events)
	if len(leaks) != 1 || !strings.Contains(leaks[0], "Opportunity.Description") {
		t.Fatalf("an unclassified field on a change event: %v", leaks)
	}
}

// TestEveryFieldOfEveryEventIsClassified: the creation events are closed-world too (HAR-130 review: Quote
// GrandTotal equals the final Amount and was on a creation event, unclassified).
func TestEveryFieldOfEveryEventIsClassified(t *testing.T) {
	for name, s := range map[string]Snapshot{"tiny": tiny(), "sample": sample(t), "late contacts": withLateContacts()} {
		if got := unclassifiedFields(t, build(t, s).Events); len(got) > 0 {
			t.Errorf("%s: fields with no classification: %v", name, got)
		}
	}
	for _, name := range []string{"Quote.GrandTotal", "Quote.Discount"} {
		if outcomeAllowed[name] == "" || outcomeBearingFields[name] == "" {
			t.Errorf("%s must be classified outcome-bearing with a reason", name)
		}
	}
}

func TestAnUnclassifiedFieldOnACreationEventFails(t *testing.T) {
	events := build(t, tiny()).Events
	for i, e := range events {
		if e.Source.SourceObjectID == "contact:K" {
			var p map[string]any
			_ = json.Unmarshal(e.Source.Payload, &p)
			p["fields"].(map[string]any)["LifetimeValue"] = map[string]any{"new": 9000}
			raw, _ := json.Marshal(p)
			events[i].Source.Payload = raw
		}
	}
	if got := unclassifiedFields(t, events); len(got) != 1 || got[0] != "Contact.LifetimeValue" {
		t.Fatalf("an unclassified creation-event field: %v", got)
	}
}

// TestDealLessEventsCarryNoUnlistedOutcome: orders, cases and chats name no deal; a closed_at on a
// case that is only opened would be an outcome the check must catch.
func TestDealLessEventsCarryNoUnlistedOutcome(t *testing.T) {
	events := build(t, sample(t)).Events
	deallessOutcomes := 0
	for _, e := range events {
		if e.DealID == "" {
			deallessOutcomes += len(outcomeValues(t, e))
		}
	}
	if deallessOutcomes == 0 {
		t.Fatal("the sample has no deal-less event with a value")
	}
	for i, e := range events {
		if e.DealID == "" && e.Source.SourceSystem == "support" && e.Source.SourceEventKey == "opened" {
			var p map[string]any
			_ = json.Unmarshal(e.Source.Payload, &p)
			p["closed_at"] = "2024-01-01T00:00:00Z"
			raw, _ := json.Marshal(p)
			events[i].Source.Payload = raw
			if leaks := outcomeLeaks(t, events); len(leaks) != 1 || !strings.Contains(leaks[0], "support.closed_at") {
				t.Fatalf("a closed_at on an opened case: %v", leaks)
			}
			return
		}
	}
	t.Fatal("no opened case in the sample")
}

// TestAFieldNamedLikeAnEnvelopeKeyIsStillAField: `description` is structural at the top level only. Planted
// inside fields, on a creation event and on the final-stage event, it is unclassified and a change-event
// field that is not snapshot-only, so both checks catch it (HAR-130 re-review).
func TestAFieldNamedLikeAnEnvelopeKeyIsStillAField(t *testing.T) {
	plant := func(match func(Event) bool) []Event {
		events := build(t, tiny()).Events
		planted := false
		for i, e := range events {
			if !match(e) {
				continue
			}
			var p map[string]any
			_ = json.Unmarshal(e.Source.Payload, &p)
			p["fields"].(map[string]any)["description"] = map[string]any{"new": "Closed Won"}
			raw, _ := json.Marshal(p)
			events[i].Source.Payload = raw
			planted = true
		}
		if !planted {
			t.Fatal("nothing planted")
		}
		return events
	}
	creation := plant(func(e Event) bool { return e.Source.SourceObjectID == "opp:C" && e.Source.SourceEventKey == "created" })
	if got := unclassifiedFields(t, creation); len(got) != 1 || got[0] != "Opportunity.description" {
		t.Errorf("fields.description on a creation event: %v", got)
	}
	change := plant(func(e Event) bool {
		return e.Source.SourceObjectID == "opp:C" && strings.HasPrefix(e.Source.SourceEventKey, "field:StageName:")
	})
	if leaks := outcomeLeaks(t, change); len(leaks) != 1 || !strings.Contains(leaks[0], "Opportunity.description") {
		t.Errorf("fields.description on a change event: %v", leaks)
	}
	if !isStructural(envelopePrefix+"description") || isStructural("Opportunity.description") {
		t.Error("description must be structural at the top level only")
	}
}
