// Package crmarena maps a read-only export of the CRMArena-Pro B2B Salesforce org (Salesforce AI
// Research, CC BY-NC 4.0, non-commercial use only) onto SourceEvents (HAR-130 / WP31), and splits its
// deals by time into previous deals (knowledge) and current deals (replayed change by change).
//
// The export is produced by bench/data/crmarena_export.py: one <Object>.json array per sObject with
// Salesforce API field names. Only the fields listed on the record types below are read.
package crmarena

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Account is a Salesforce Account (a customer company).
type Account struct {
	ID                string `json:"Id"`
	Name              string `json:"Name"`
	Industry          string `json:"Industry"`
	NumberOfEmployees *int   `json:"NumberOfEmployees"`
	Description       string `json:"Description"`
}

// Contact is a customer-side person of an account.
type Contact struct {
	ID         string `json:"Id"`
	AccountID  string `json:"AccountId"`
	FirstName  string `json:"FirstName"`
	LastName   string `json:"LastName"`
	Email      string `json:"Email"`
	Title      string `json:"Title"`
	Department string `json:"Department"`
}

// User is a Salesforce user: the seller's reps, plus admin and system users.
type User struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	Email    string `json:"Email"`
	UserType string `json:"UserType"`
}

// Opportunity is a deal.
type Opportunity struct {
	ID          string   `json:"Id"`
	AccountID   string   `json:"AccountId"`
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	StageName   string   `json:"StageName"`
	Amount      *float64 `json:"Amount"`
	CloseDate   string   `json:"CloseDate"`
	OwnerID     string   `json:"OwnerId"`
	CreatedDate string   `json:"CreatedDate"`
	ContractID  string   `json:"ContractId__c"`
}

// EmailMessage is an email logged against an opportunity (RelatedToId).
type EmailMessage struct {
	ID          string `json:"Id"`
	RelatedToID string `json:"RelatedToId"`
	FromAddress string `json:"FromAddress"`
	FromName    string `json:"FromName"`
	ToAddress   string `json:"ToAddress"`
	CcAddress   string `json:"CcAddress"`
	Subject     string `json:"Subject"`
	TextBody    string `json:"TextBody"`
	MessageDate string `json:"MessageDate"`
}

// Task is a rep's task on an opportunity (WhatId); ActivityDate is a day.
type Task struct {
	ID           string `json:"Id"`
	WhatID       string `json:"WhatId"`
	WhoID        string `json:"WhoId"` // the contact a task is about (empty in the snapshot)
	AccountID    string `json:"AccountId"`
	OwnerID      string `json:"OwnerId"`
	Subject      string `json:"Subject"`
	Description  string `json:"Description"`
	ActivityDate string `json:"ActivityDate"`
	Priority     string `json:"Priority"`
	Type         string `json:"Type"`
}

// Quote is a quote on an opportunity. Status is the final status; the source has no status history.
type Quote struct {
	ID             string   `json:"Id"`
	OpportunityID  string   `json:"OpportunityId"`
	AccountID      string   `json:"AccountId"`
	Name           string   `json:"Name"`
	QuoteNumber    string   `json:"QuoteNumber"`
	Status         string   `json:"Status"`
	CreatedDate    string   `json:"CreatedDate"`
	ExpirationDate string   `json:"ExpirationDate"`
	GrandTotal     *float64 `json:"GrandTotal"`
	Discount       *float64 `json:"Discount"`
	Description    string   `json:"Description"`
	ContactID      string   `json:"ContactId"` // the quote's contact
}

// Order is an order of an account (no opportunity link in the source).
type Order struct {
	ID            string `json:"Id"`
	AccountID     string `json:"AccountId"`
	OwnerID       string `json:"OwnerId"`
	EffectiveDate string `json:"EffectiveDate"`
	Status        string `json:"Status"`
	OrderNumber   string `json:"OrderNumber"`
	BillToContact string `json:"BillToContactId"` // empty in the snapshot
	ShipToContact string `json:"ShipToContactId"` // empty in the snapshot
}

// Contract is a signed contract; its opportunity is named by Opportunity.ContractId__c.
type Contract struct {
	ID                 string `json:"Id"`
	AccountID          string `json:"AccountId"`
	ContractNumber     string `json:"ContractNumber"`
	StartDate          string `json:"StartDate"`
	EndDate            string `json:"EndDate"`
	ContractTerm       *int   `json:"ContractTerm"`
	CustomerSignedDate string `json:"CustomerSignedDate"`
	CompanySignedDate  string `json:"CompanySignedDate"`
	Status             string `json:"Status"`
	Description        string `json:"Description"`
}

// Case is a support case.
type Case struct {
	ID          string `json:"Id"`
	AccountID   string `json:"AccountId"`
	ContactID   string `json:"ContactId"`
	OwnerID     string `json:"OwnerId"`
	CaseNumber  string `json:"CaseNumber"`
	Subject     string `json:"Subject"`
	Description string `json:"Description"`
	Priority    string `json:"Priority"`
	Origin      string `json:"Origin"`
	CreatedDate string `json:"CreatedDate"`
	ClosedDate  string `json:"ClosedDate"`
}

// ChatTranscript is a LiveChatTranscript attached to a case.
type ChatTranscript struct {
	ID        string `json:"Id"`
	CaseID    string `json:"CaseId"`
	AccountID string `json:"AccountId"`
	OwnerID   string `json:"OwnerId"`
	Body      string `json:"Body"`
	EndTime   string `json:"EndTime"`
}

// ContactRole links a contact to a deal. Its CreatedDate is the org's load date, so it carries no
// real time; it is only used to place a contact that appears nowhere else.
type ContactRole struct {
	ContactID     string `json:"ContactId"`
	OpportunityID string `json:"OpportunityId"`
}

// Snapshot is one export of the org.
type Snapshot struct {
	Accounts      []Account
	Contacts      []Contact
	Users         []User
	Opportunities []Opportunity
	Emails        []EmailMessage
	Tasks         []Task
	Quotes        []Quote
	Orders        []Order
	Contracts     []Contract
	Cases         []Case
	Chats         []ChatTranscript
	Roles         []ContactRole // optional file (OpportunityContactRole.json)
}

// Load reads every <Object>.json the loader needs from dir. A missing or malformed file is an error.
func Load(dir string) (Snapshot, error) {
	var s Snapshot
	files := []struct {
		name string
		into any
	}{
		{"Account", &s.Accounts}, {"Contact", &s.Contacts}, {"User", &s.Users}, {"Opportunity", &s.Opportunities},
		{"EmailMessage", &s.Emails}, {"Task", &s.Tasks}, {"Quote", &s.Quotes}, {"Order", &s.Orders},
		{"Contract", &s.Contracts}, {"Case", &s.Cases}, {"LiveChatTranscript", &s.Chats},
	}
	for _, f := range files {
		if err := readArray(filepath.Join(dir, f.name+".json"), f.into); err != nil {
			return Snapshot{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "OpportunityContactRole.json")); err == nil {
		if err := readArray(filepath.Join(dir, "OpportunityContactRole.json"), &s.Roles); err != nil {
			return Snapshot{}, err
		}
	}
	if len(s.Accounts) == 0 {
		return Snapshot{}, errors.New("crmarena: snapshot has no accounts")
	}
	return s, nil
}

func readArray(path string, into any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("crmarena: read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("crmarena: decode %s: %w", path, err)
	}
	return nil
}
