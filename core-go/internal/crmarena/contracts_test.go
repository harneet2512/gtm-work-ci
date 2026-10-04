package crmarena

import (
	"strings"
	"testing"
)

func TestContractNamedByTwoDealsIsReportedNotOverwritten(t *testing.T) {
	s := tiny()
	s.Opportunities[2].ContractID = "K1" // deal C names P's contract too
	_, err := Build(s)
	if err == nil || !strings.Contains(err.Error(), "K1") || !strings.Contains(err.Error(), "named by") {
		t.Fatalf("a contract named by two deals was accepted: %v", err)
	}
}

func TestContractOfAnotherAccountIsReported(t *testing.T) {
	s := tiny()
	s.Accounts = append(s.Accounts, Account{ID: "B", Name: "Other"})
	s.Contracts[0].AccountID = "B"
	_, err := Build(s)
	if err == nil || !strings.Contains(err.Error(), "K1") || !strings.Contains(err.Error(), "account") {
		t.Fatalf("a contract of another account than its deal was accepted: %v", err)
	}
}

func TestSplitRecordsThePreviousDealsByStage(t *testing.T) {
	w, _ := Windows(tiny())
	s, err := NewSplit(w, "2023-10-01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PreviousByStage) != 1 || s.PreviousByStage["Closed"] != 1 {
		t.Fatalf("previous deals by stage = %v", s.PreviousByStage)
	}
}
