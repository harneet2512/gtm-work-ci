package crmarena

import (
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// index holds the lookups the mappers share.
type index struct {
	opps        map[string]Opportunity
	contractOpp map[string]string // contract id -> opportunity id (Opportunity.ContractId__c)
	cases       map[string]Case
	names       map[string]string // lower-case original address -> display name
	domains     map[string]string // account id -> its contacts' one email domain, "" when they disagree
}

func newIndex(s Snapshot, reps Reps) index {
	ix := index{opps: map[string]Opportunity{}, contractOpp: map[string]string{}, cases: map[string]Case{},
		names: map[string]string{}, domains: map[string]string{}}
	for _, o := range s.Opportunities {
		ix.opps[o.ID] = o
	}
	for contract, deals := range contractOwners(s) {
		ix.contractOpp[contract] = deals[0] // checkContracts has rejected more than one
	}
	for _, c := range s.Cases {
		ix.cases[c.ID] = c
	}
	for _, u := range s.Users {
		if _, isRep := reps.Address(u.Email); isRep {
			ix.names[strings.ToLower(strings.TrimSpace(u.Email))] = strings.TrimSpace(u.Name)
		}
	}
	seen := map[string]map[string]bool{}
	for _, c := range s.Contacts {
		email := strings.ToLower(strings.TrimSpace(c.Email))
		ix.names[email] = strings.TrimSpace(c.FirstName + " " + c.LastName)
		if seen[c.AccountID] == nil {
			seen[c.AccountID] = map[string]bool{}
		}
		if d := normalize.ExternalDomain(email); d != "" {
			seen[c.AccountID][d] = true
		}
	}
	for acct, ds := range seen {
		if len(ds) == 1 {
			for d := range ds {
				ix.domains[acct] = d
			}
		}
	}
	return ix
}

// accountOf returns the account of an opportunity ("" when unknown).
func (ix index) accountOf(oppID string) string { return ix.opps[oppID].AccountID }

// contractOwners maps a contract id to the opportunities that name it (Opportunity.ContractId__c),
// in id order.
func contractOwners(s Snapshot) map[string][]string {
	out := map[string][]string{}
	for _, o := range s.Opportunities {
		if o.ContractID != "" {
			out[o.ContractID] = append(out[o.ContractID], o.ID)
		}
	}
	for _, deals := range out {
		sort.Strings(deals)
	}
	return out
}

// checkContracts rejects what the contract-to-deal link cannot carry: a contract named by two deals
// (one signature would be attributed to one of them silently) and a contract whose account is not
// its deal's account (the event would link two accounts).
func checkContracts(s Snapshot) error {
	owners := contractOwners(s)
	opps := map[string]Opportunity{}
	for _, o := range s.Opportunities {
		opps[o.ID] = o
	}
	for _, c := range s.Contracts {
		deals := owners[c.ID]
		if len(deals) > 1 {
			return fmt.Errorf("crmarena: contract %s is named by %d deals (%s)", c.ID, len(deals), strings.Join(deals, ", "))
		}
		if len(deals) == 1 && opps[deals[0]].AccountID != c.AccountID {
			return fmt.Errorf("crmarena: contract %s belongs to account %s but its deal %s to account %s",
				c.ID, c.AccountID, deals[0], opps[deals[0]].AccountID)
		}
	}
	return nil
}
