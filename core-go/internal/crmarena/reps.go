package crmarena

import (
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// Reps are the seller's employees: the users who own a mapped record (opportunity, task, order, case,
// chat) or whose address appears on an email. Their addresses are bound to our domain, keeping the
// local part (contracts/normalization.md, "Internal identities"); admin and system users are not reps.
type Reps struct {
	byUserID map[string]string // Salesforce user id -> tenant address
	byEmail  map[string]string // original lower-case address -> tenant address
	people   []graph.CompanyPerson
}

// TenantAddress binds an address to our domain, keeping the local part.
func TenantAddress(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	return local + "@" + normalize.OurDomain
}

// ByUser returns the tenant address of a rep, or false when the user is not a rep.
func (r Reps) ByUser(userID string) (string, bool) {
	a, ok := r.byUserID[userID]
	return a, ok
}

// Address rebinds a rep's address and lower-cases anyone else's; isRep says which it was.
func (r Reps) Address(email string) (addr string, isRep bool) {
	e := strings.ToLower(strings.TrimSpace(email))
	if a, ok := r.byEmail[e]; ok {
		return a, true
	}
	return e, false
}

// Company is the vendor company file seeded before the events (employees only).
func (r Reps) Company(source string) graph.Company {
	people := append([]graph.CompanyPerson(nil), r.people...)
	return graph.Company{
		Organization: graph.CompanyInfo{Name: "CRMArena-Pro B2B seller (rebound)", Domain: normalize.OurDomain},
		People:       people,
		Source:       source,
	}
}

// Len is the number of distinct reps (people, not Salesforce user records).
func (r Reps) Len() int { return len(r.people) }

// buildReps finds the reps. Users that share an address are one person (the source has duplicate
// user records per person); two different addresses that would bind to the same tenant address are
// an error.
func buildReps(s Snapshot) (Reps, error) {
	referenced := referencedUsers(s)
	users := append([]User(nil), s.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	r := Reps{byUserID: map[string]string{}, byEmail: map[string]string{}}
	owner := map[string]string{} // tenant address -> original address
	for _, u := range users {
		email := strings.ToLower(strings.TrimSpace(u.Email))
		if !referenced.has(u.ID, email) || email == "" {
			continue
		}
		tenant := TenantAddress(email)
		if prev, ok := owner[tenant]; ok && prev != email {
			return Reps{}, fmt.Errorf("crmarena: reps %s and %s both bind to %s", prev, email, tenant)
		}
		r.byUserID[u.ID] = tenant
		if _, seen := owner[tenant]; !seen {
			owner[tenant] = email
			r.byEmail[email] = tenant
			r.people = append(r.people, graph.CompanyPerson{Key: "sf-user:" + u.ID, Kind: "employee",
				DisplayName: strings.TrimSpace(u.Name), Email: tenant})
		}
	}
	sort.Slice(r.people, func(i, j int) bool { return r.people[i].Email < r.people[j].Email })
	return r, nil
}

// refs are the user ids and addresses mapped records point at.
type refs struct {
	ids    map[string]bool
	emails map[string]bool
}

func (f refs) has(id, email string) bool { return f.ids[id] || f.emails[email] }

func referencedUsers(s Snapshot) refs {
	f := refs{ids: map[string]bool{}, emails: map[string]bool{}}
	for _, o := range s.Opportunities {
		f.ids[o.OwnerID] = true
	}
	for _, t := range s.Tasks {
		f.ids[t.OwnerID] = true
	}
	for _, o := range s.Orders {
		f.ids[o.OwnerID] = true
	}
	for _, c := range s.Cases {
		f.ids[c.OwnerID] = true
	}
	for _, c := range s.Chats {
		f.ids[c.OwnerID] = true
	}
	for _, e := range s.Emails {
		for _, a := range append(splitAddresses(e.ToAddress), splitAddresses(e.CcAddress)...) {
			f.emails[a] = true
		}
		f.emails[strings.ToLower(strings.TrimSpace(e.FromAddress))] = true
	}
	return f
}

// splitAddresses splits a Salesforce ';'-separated address list into lower-case addresses.
func splitAddresses(list string) []string {
	var out []string
	for _, a := range strings.Split(list, ";") {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			out = append(out, a)
		}
	}
	return out
}
