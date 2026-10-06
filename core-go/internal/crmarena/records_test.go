package crmarena

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSample(t *testing.T) {
	s := sample(t)
	if len(s.Accounts) != 3 || len(s.Contacts) == 0 || len(s.Emails) == 0 || len(s.Chats) == 0 {
		t.Fatalf("sample counts: %d accounts %d contacts %d emails %d chats", len(s.Accounts), len(s.Contacts), len(s.Emails), len(s.Chats))
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory accepted")
	}
	dir := t.TempDir()
	for _, name := range []string{"Account", "Contact", "User", "Opportunity", "EmailMessage", "Task", "Quote", "Order",
		"Contract", "Case", "LiveChatTranscript"} {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte("[]"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "no accounts") {
		t.Errorf("empty snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Account.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("malformed file accepted")
	}
}

func TestRepsAreReferencedUsersGroupedByAddress(t *testing.T) {
	s := tiny()
	s.Users = append(s.Users, User{ID: "U2", Name: "Rep One", Email: "Rep@techagents.com"}) // duplicate record
	reps, err := buildReps(s)
	if err != nil {
		t.Fatal(err)
	}
	if reps.Len() != 1 {
		t.Fatalf("reps = %d, want 1 (admin is not referenced, duplicates are one person)", reps.Len())
	}
	if a, ok := reps.ByUser("U2"); !ok || a != "rep@vendor.example" {
		t.Errorf("duplicate user record = %q, %v", a, ok)
	}
	if _, ok := reps.ByUser("U0"); ok {
		t.Error("admin became a rep")
	}
	if a, isRep := reps.Address(" REP@techagents.com "); !isRep || a != "rep@vendor.example" {
		t.Errorf("rep address = %q, %v", a, isRep)
	}
	if a, isRep := reps.Address("Ann@Acme.example"); isRep || a != "ann@acme.example" {
		t.Errorf("customer address = %q, %v", a, isRep)
	}
	c := reps.Company("src")
	if len(c.People) != 1 || c.People[0].Kind != "employee" || c.Organization.Domain != "vendor.example" || c.Source != "src" {
		t.Errorf("company = %+v", c)
	}
}

func TestRepsThatWouldShareATenantAddressAreAnError(t *testing.T) {
	s := tiny()
	s.Users = append(s.Users, User{ID: "U3", Name: "Other Rep", Email: "rep@techdomain.com"})
	s.Tasks = append(s.Tasks, Task{ID: "T3", WhatID: "C", OwnerID: "U3", ActivityDate: "2023-09-02"})
	if _, err := buildReps(s); err == nil {
		t.Fatal("two reps bound to one address")
	}
}

func TestBuildErrorsNameTheBadRecord(t *testing.T) {
	cases := map[string]func(s *Snapshot){
		"bad opportunity date": func(s *Snapshot) { s.Opportunities[0].CreatedDate = "yesterday" },
		"bad email date":       func(s *Snapshot) { s.Emails[0].MessageDate = "2023-02-01" },
		"bad task date":        func(s *Snapshot) { s.Tasks[0].ActivityDate = "soon" },
		"email without recipient": func(s *Snapshot) {
			s.Emails[0].ToAddress = " ; "
		},
		"bad contract date": func(s *Snapshot) { s.Contracts[0].CustomerSignedDate = "x" },
		"bad order date":    func(s *Snapshot) { s.Orders = []Order{{ID: "O", AccountID: "A", EffectiveDate: "x"}} },
		"bad quote date":    func(s *Snapshot) { s.Quotes = []Quote{{ID: "Q", OpportunityID: "C", CreatedDate: "x"}} },
		"bad case date":     func(s *Snapshot) { s.Cases = []Case{{ID: "S", AccountID: "A", CreatedDate: "x"}} },
		"bad case close": func(s *Snapshot) {
			s.Cases = []Case{{ID: "S", AccountID: "A", CreatedDate: "2023-01-01T00:00:00.000+0000", ClosedDate: "x"}}
		},
		"bad chat time": func(s *Snapshot) { s.Chats = []ChatTranscript{{ID: "H", AccountID: "A", Body: "x", EndTime: "x"}} },
		"rep collision": func(s *Snapshot) {
			s.Users = append(s.Users, User{ID: "U3", Email: "rep@techdomain.com"})
			s.Orders = []Order{{ID: "O", AccountID: "A", OwnerID: "U3", EffectiveDate: "2023-01-01"}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := tiny()
			mutate(&s)
			if _, err := Build(s); err == nil {
				t.Fatal("bad record accepted")
			}
		})
	}
}

func TestContractWithoutSignaturesUsesItsStartDate(t *testing.T) {
	at, err := signedAt(Contract{StartDate: "2023-05-01"})
	if err != nil || at.Format(sfDate) != "2023-05-01" {
		t.Fatalf("signedAt = %v, %v", at, err)
	}
}
