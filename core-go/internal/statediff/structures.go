package statediff

import (
	"reflect"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// MemberView is a buying-group member as a diff reports it: identity, roles, status and delegation.
// Titles, engagement times and evidence are not state a rep acts on, so they never make a change.
type MemberView struct {
	PersonID    string   `json:"person_id"`
	Roles       []string `json:"roles"`
	Status      string   `json:"status"`
	DelegatedTo string   `json:"delegated_to_person_id,omitempty"`
}

func viewOf(m reducer.Member) MemberView {
	roles := append([]string{}, m.Roles...)
	sort.Strings(roles)
	v := MemberView{PersonID: m.PersonID, Roles: roles, Status: m.Status}
	if m.DelegatedToPerson != nil {
		v.DelegatedTo = *m.DelegatedToPerson
	}
	return v
}

func memberViews(members []reducer.Member) []MemberView {
	out := make([]MemberView, 0, len(members))
	for _, m := range members {
		out = append(out, viewOf(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PersonID < out[j].PersonID })
	return out
}

func buyingGroupChange(before, after []reducer.Member) (Change, bool) {
	b, a := memberViews(before), memberViews(after)
	if reflect.DeepEqual(b, a) {
		return Change{}, false
	}
	c := Change{Field: FieldBuyingGroup, Material: true, Op: structureOp(len(b), len(a))}
	if len(b) > 0 {
		c.Before = b
	}
	if len(a) > 0 {
		c.After = a
	}
	byID := map[string]MemberView{}
	for _, v := range b {
		byID[v.PersonID] = v
	}
	for _, m := range after {
		if old, seen := byID[m.PersonID]; !seen || !reflect.DeepEqual(old, viewOf(m)) {
			c.EvidenceRefs = append(c.EvidenceRefs, m.EvidenceRefs...)
		}
	}
	return c, true
}

func gapChange(before, after []string) (Change, bool) {
	b, a := sortedKeys(before), sortedKeys(after)
	if reflect.DeepEqual(b, a) {
		return Change{}, false
	}
	c := Change{Field: FieldCoverageGaps, Material: true, Op: structureOp(len(b), len(a))}
	if len(b) > 0 {
		c.Before = b
	}
	if len(a) > 0 {
		c.After = a
	}
	return c, true
}

func structureOp(before, after int) string {
	switch {
	case before == 0:
		return OpSet
	case after == 0:
		return OpRemoved
	}
	return OpChanged
}

func sortedKeys(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(strings.TrimSpace(s)))
	}
	sort.Strings(out)
	return out
}
