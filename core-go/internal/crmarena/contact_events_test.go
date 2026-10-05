package crmarena

import (
	"strings"
	"testing"
	"time"
)

// withLateContacts adds to tiny: bob, who first writes on deal C on 2023-11-20 (after the cutoff);
// carol, who is only a contact role on deal C; dave, who only opens a support case on 2023-12-05.
func withLateContacts() Snapshot {
	s := tiny()
	for _, c := range []Contact{{ID: "K2", FirstName: "Bob", Email: "bob@acme.example"}, {ID: "K3", FirstName: "Carol", Email: "carol@acme.example"},
		{ID: "K4", FirstName: "Dave", Email: "dave@acme.example"}} {
		c.AccountID, c.LastName, c.Title = "A", "Late", "VP"
		s.Contacts = append(s.Contacts, c)
	}
	s.Emails = append(s.Emails, EmailMessage{ID: "EB", RelatedToID: "C", FromAddress: "bob@acme.example", ToAddress: "rep@techagents.com",
		Subject: "Hi", TextBody: "x", MessageDate: "2023-11-20T09:00:00.000+0000"})
	s.Roles = []ContactRole{{ContactID: "K3", OpportunityID: "C"}, {ContactID: "K2", OpportunityID: "C"}}
	s.Cases = []Case{{ID: "CS1", AccountID: "A", ContactID: "K4", Subject: "Help", Origin: "Email", CreatedDate: "2023-12-05T09:00:00.000+0000"}}
	return s
}

func contactEvent(t *testing.T, events []Event, id string) (Event, bool) {
	t.Helper()
	for _, e := range events {
		if e.Source.SourceObjectID == "contact:"+id && e.Source.SourceEventKey == "created" {
			return e, true
		}
	}
	return Event{}, false
}

func TestContactIsCreatedAtItsFirstAppearanceNotItsAccountsFirstRecord(t *testing.T) {
	events := build(t, withLateContacts()).Events
	want := map[string]string{
		"K":  "2023-02-01", // writes on deal P first
		"K2": "2023-11-20", // first email, not its contact role on deal C (a load date) nor the account's first record
		"K3": "2023-08-01", // only a contact role: the start of deal C
		"K4": "2023-12-05", // only a support case
	}
	for id, day := range want {
		e, ok := contactEvent(t, events, id)
		if !ok {
			t.Fatalf("no creation event for contact %s", id)
		}
		if got := e.OccurredAt().Format(sfDate); got != day {
			t.Errorf("contact %s created %s, want %s", id, got, day)
		}
	}
}

func TestFutureContactsAreNotInTheAsOfTimelineAndArriveWithTheirFirstEmail(t *testing.T) {
	r := build(t, withLateContacts())
	cut := cutoff(t, "2023-10-01")
	before, _, err := AsOf(r.Events, cut)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"K2", "K4"} {
		if _, ok := contactEvent(t, before, id); ok {
			t.Errorf("contact %s, first seen after the cutoff, is in the as-of timeline", id)
		}
	}
	for _, e := range before { // deal-less records (cases, orders, chats) obey the same cutoff
		if e.DealID == "" && !e.OccurredAt().Before(cut) {
			t.Errorf("deal-less %s/%s at %s is in the as-of timeline", e.Source.SourceObjectID, e.Source.SourceEventKey, e.OccurredAt())
		}
	}
	laterCase := false
	for _, e := range r.Events {
		laterCase = laterCase || (e.Source.SourceSystem == "support" && !e.OccurredAt().Before(cut))
	}
	if !laterCase {
		t.Error("the snapshot has no case after the cutoff: the check above is vacuous")
	}
	if _, ok := contactEvent(t, before, "K"); !ok {
		t.Error("a contact first seen before the cutoff is missing")
	}
	w, _ := Windows(withLateContacts())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, err := ReplayRemaining(r.Events, split, "C")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range got {
		keys = append(keys, e.Source.SourceObjectID)
	}
	joined := strings.Join(keys, " ")
	if !strings.Contains(joined, "contact:K2 EB") {
		t.Fatalf("contact K2 must arrive immediately before the email that names them: %s", joined)
	}
	if strings.Contains(joined, "contact:K4") {
		t.Error("a case-only contact belongs to no deal's replay")
	}
	e, _ := contactEvent(t, r.Events, "K2")
	if !e.OccurredAt().Equal(time.Date(2023, 11, 20, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("K2 created at %s", e.OccurredAt())
	}
}

// TestQuoteOrderAndTaskContactsAreAppearanceEvidence: a contact named on a quote, an order or a task
// already exists from then; it is not a "new stakeholder" when it writes later (HAR-130 re-review).
func TestQuoteOrderAndTaskContactsAreAppearanceEvidence(t *testing.T) {
	s := withLateContacts()
	s.Quotes[0].ContactID = "K2"                                                                                                                           // bob: quote on 2023-02-15, email only on 2023-11-20
	s.Orders = []Order{{ID: "O1", AccountID: "A", OwnerID: "U1", EffectiveDate: "2023-03-01", Status: "Activated", OrderNumber: "1", BillToContact: "K4"}} // dave: case on 2023-12-05
	s.Tasks = append(s.Tasks, Task{ID: "T3", WhatID: "C", AccountID: "A", OwnerID: "U1", Subject: "Call", ActivityDate: "2023-09-01", WhoID: "K3"})        // carol: role only
	s.Contacts = append(s.Contacts, Contact{ID: "K6", AccountID: "A", FirstName: "Fay", LastName: "Role", Email: "fay@acme.example"})
	s.Roles = append(s.Roles, ContactRole{ContactID: "K6", OpportunityID: "C"}, ContactRole{ContactID: "K6", OpportunityID: "P"}) // C starts 2023-08-01, P 2023-01-05
	events := build(t, s).Events
	want := map[string]string{"K2": "2023-02-15", "K4": "2023-03-01", "K3": "2023-09-01", "K6": "2023-01-05"}
	for id, day := range want {
		e, ok := contactEvent(t, events, id)
		if !ok {
			t.Fatalf("no creation event for %s", id)
		}
		if got := e.OccurredAt().Format(sfDate); got != day {
			t.Errorf("contact %s created %s, want %s", id, got, day)
		}
	}
	w, _ := Windows(s)
	split, _ := NewSplit(w, "2023-10-01", 0)
	replay, err := ReplayRemaining(events, split, "C")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range replay {
		if strings.HasPrefix(e.Source.SourceObjectID, "contact:") {
			t.Errorf("%s is delivered as a new stakeholder although a quote, task or role named it before the cutoff", e.Source.SourceObjectID)
		}
	}
}
