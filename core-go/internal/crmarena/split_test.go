package crmarena

import (
	"testing"
	"time"
)

// tiny is a hand-built snapshot: deal P ends before 2023-10-01 (its contract is signed after its last
// email, but before the cutoff), deal L has its last email before the cutoff yet signs its contract
// after it (so it has not ended), deal C runs across it, deal F starts after it, deal N has no activity.
func tiny() Snapshot {
	email := func(id, deal, at string) EmailMessage {
		return EmailMessage{ID: id, RelatedToID: deal, FromAddress: "rep@techagents.com", ToAddress: "ann@acme.example",
			Subject: "Hello", TextBody: "hi", MessageDate: at}
	}
	task := func(id, deal, day string) Task {
		return Task{ID: id, WhatID: deal, AccountID: "A", OwnerID: "U1", Subject: "Call", ActivityDate: day}
	}
	return Snapshot{
		Accounts: []Account{{ID: "A", Name: "Acme"}},
		Contacts: []Contact{{ID: "K", AccountID: "A", FirstName: "Ann", LastName: "Lee", Email: "ann@acme.example"}},
		Users:    []User{{ID: "U1", Name: "Rep One", Email: "rep@techagents.com"}, {ID: "U0", Name: "Admin", Email: "admin@salesforce.com"}},
		Opportunities: []Opportunity{
			{ID: "P", AccountID: "A", Name: "Prev", StageName: "Closed", CloseDate: "2023-12-15", OwnerID: "U1", CreatedDate: "2023-01-05T10:00:00.000+0000", ContractID: "K1"},
			{ID: "L", AccountID: "A", Name: "Late", StageName: "Closed", CloseDate: "2023-12-20", OwnerID: "U1", CreatedDate: "2023-07-05T10:00:00.000+0000", ContractID: "K2"},
			{ID: "C", AccountID: "A", Name: "Cur", StageName: "Quote", OwnerID: "U1", CreatedDate: "2023-08-01T10:00:00.000+0000"},
			{ID: "F", AccountID: "A", Name: "Fut", StageName: "Discovery", OwnerID: "U1", CreatedDate: "2023-11-01T10:00:00.000+0000"},
			{ID: "N", AccountID: "A", Name: "None", StageName: "Discovery", OwnerID: "U1", CreatedDate: "2023-02-01T10:00:00.000+0000"},
		},
		Emails: []EmailMessage{
			email("E1", "P", "2023-02-01T09:00:00.000+0000"), email("E2", "P", "2023-03-01T09:00:00.000+0000"),
			email("E3", "C", "2023-08-10T09:00:00.000+0000"), email("E4", "C", "2023-11-10T09:00:00.000+0000"),
			email("E5", "F", "2023-11-02T09:00:00.000+0000"),
			email("E6", "L", "2023-08-01T09:00:00.000+0000"), email("E7", "L", "2023-09-01T09:00:00.000+0000"),
		},
		Quotes: []Quote{{ID: "Q1", OpportunityID: "P", AccountID: "A", Name: "Quote", Status: "Accepted",
			CreatedDate: "2023-02-15T09:00:00.000+0000", ExpirationDate: "2023-12-31"}},
		Tasks: []Task{task("T1", "C", "2023-09-01"), task("T2", "C", "2023-12-01")},
		Contracts: []Contract{
			{ID: "K1", AccountID: "A", StartDate: "2023-04-01", EndDate: "2024-04-01", CustomerSignedDate: "2023-03-10", CompanySignedDate: "2023-03-15"},
			{ID: "K2", AccountID: "A", StartDate: "2023-11-15", EndDate: "2024-11-15", CustomerSignedDate: "2023-11-10", CompanySignedDate: "2023-11-12"},
		},
	}
}

func cutoff(t *testing.T, d string) time.Time {
	t.Helper()
	at, err := parseDate("test", d)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestWindowsAndRoles(t *testing.T) {
	w, err := Windows(tiny())
	if err != nil {
		t.Fatal(err)
	}
	at := cutoff(t, "2023-10-01")
	want := map[string]Role{"P": RolePrevious, "L": RoleCurrent, "C": RoleCurrent, "F": RoleFuture, "N": RoleNoActivity}
	for id, role := range want {
		if got := w[id].RoleAt(at); got != role {
			t.Errorf("deal %s role = %s, want %s", id, got, role)
		}
	}
	if c := w["C"]; c.Activities != 4 || c.Before(at) != 2 {
		t.Errorf("deal C window = %+v", c)
	}
}

func TestWindowsRejectActivityOnUnknownDeal(t *testing.T) {
	s := tiny()
	s.Tasks = append(s.Tasks, Task{ID: "T9", WhatID: "ghost", ActivityDate: "2023-01-01"})
	if _, err := Windows(s); err == nil {
		t.Fatal("activity on an unknown opportunity accepted")
	}
}

func TestCutoffRuleMaximizesTheSmallerSideAndPrefersEarlierTies(t *testing.T) {
	cands := []Candidate{
		{Cutoff: "2023-01-01", Counts: Counts{Previous: 1, CurrentReplayable: 9}},
		{Cutoff: "2023-02-01", Counts: Counts{Previous: 5, CurrentReplayable: 4}},
		{Cutoff: "2023-03-01", Counts: Counts{Previous: 9, CurrentReplayable: 4}},
	}
	best, err := ChooseCutoff(cands)
	if err != nil || best.Cutoff != "2023-02-01" {
		t.Fatalf("best = %+v, %v", best, err)
	}
	if _, err := ChooseCutoff(nil); err == nil {
		t.Error("empty candidates accepted")
	}
}

func TestNewSplitFreezesTheDealLists(t *testing.T) {
	w, _ := Windows(tiny())
	s, err := NewSplit(w, "2023-10-01", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Previous) != 1 || s.Previous[0] != "P" || len(s.Current) != 2 || s.Current[0] != "C" || s.Current[1] != "L" {
		t.Fatalf("split = %+v", s)
	}
	if s.Counts.Future != 1 || s.Counts.NoActivity != 1 || s.Counts.CurrentReplayable != 1 || s.Counts.AccountsWithBoth != 1 || s.Seed != 7 {
		t.Errorf("counts = %+v", s.Counts)
	}
	auto, err := NewSplit(w, "", 0)
	if err != nil || auto.Cutoff == "" || len(auto.Candidates) == 0 {
		t.Fatalf("automatic cutoff: %+v, %v", auto, err)
	}
	if _, err := NewSplit(w, "October", 0); err == nil {
		t.Error("malformed cutoff accepted")
	}
}

// TestKnowledgeInputsNeverSeeTheFuture: nothing of a current deal reaches previous-deal knowledge, and
// a deal whose contract is signed after the cutoff has not ended, so it is not previous at all.
func TestKnowledgeInputsNeverSeeTheFuture(t *testing.T) {
	r := build(t, tiny())
	w, _ := Windows(tiny())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, err := KnowledgeInputs(r.Events, split)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("no knowledge inputs")
	}
	sawContract := false
	for _, e := range got {
		if e.DealID != "P" {
			t.Errorf("%s of deal %q is not previous-deal knowledge", e.Source.SourceObjectID, e.DealID)
		}
		sawContract = sawContract || e.Source.SourceObjectID == "contract:K1"
	}
	if !sawContract {
		t.Error("the previous deal's own contract (signed before the cutoff) is missing from knowledge")
	}
}

func TestReplayIsOrderedByDateAndOnlyForCurrentDeals(t *testing.T) {
	r := build(t, tiny())
	w, _ := Windows(tiny())
	split, _ := NewSplit(w, "2023-10-01", 0)
	got, err := Replay(r.Events, split, "C")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 5 { // creation, 2 emails, 2 tasks, stage
		t.Fatalf("replay has %d events", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].OccurredAt().Before(got[i-1].OccurredAt()) {
			t.Fatalf("replay out of date order at %d", i)
		}
	}
	if _, err := Replay(r.Events, split, "P"); err == nil {
		t.Error("replay of a previous deal accepted")
	}
	if _, err := KnowledgeInputs(r.Events, Split{}); err == nil {
		t.Error("split without cutoff accepted")
	}
}
