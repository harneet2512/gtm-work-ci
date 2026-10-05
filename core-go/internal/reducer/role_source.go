package reducer

import (
	"fmt"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// Where a buying-group member's roles come from (account_state.v1.json member.role_source).
const (
	RoleRecorded = "recorded" // a human or a system of record said so
	RoleInferred = "inferred" // a model read it from text
)

// RoleSources lists the legal values of Member.RoleSource.
func RoleSources() []string { return []string{RoleRecorded, RoleInferred} }

const (
	maxRoleBasis = 200
	maxBasisQuot = 80
)

// RoleProvenance is where one role of a member comes from (account_state.v1.json member.role_provenance).
type RoleProvenance struct {
	Role   string `json:"role"`
	Source string `json:"source"`
	Basis  string `json:"basis"`
}

// strongestClaim picks the claim that decides provenance: standing, then latest, then lowest id.
func strongestClaim(cs []claims.Claim) claims.Claim {
	best := cs[0]
	for _, c := range cs[1:] {
		if ra, rb := c.Standing.Rank(), best.Standing.Rank(); ra != rb {
			if ra > rb {
				best = c
			}
			continue
		}
		if !c.OccurredAt.Equal(best.OccurredAt) {
			if c.OccurredAt.After(best.OccurredAt) {
				best = c
			}
			continue
		}
		if c.ID < best.ID {
			best = c
		}
	}
	return best
}

// provenanceOf names the source and basis a claim gives a role: "recorded" when it is a human's, the CRM's or a
// first-party record, else "inferred" with a clipped quote.
func provenanceOf(c claims.Claim) (source, basis string) {
	source = RoleInferred
	switch c.Standing {
	case claims.HumanApproved, claims.CRMExplicit, claims.FirstPartyRecord:
		source = RoleRecorded
	}
	text := fmt.Sprintf("%s %s", c.Standing, c.FieldPath)
	if source == RoleInferred && c.EvidenceQuote != "" {
		text += fmt.Sprintf(": %q", truncate(c.EvidenceQuote, maxBasisQuot))
	}
	return source, truncate(text, maxRoleBasis)
}

// roleProvenance gives the provenance of each role (in role order) from the winning claims behind it, and the
// member-level summary: the provenance of the strongest claim among all of them. A role with no claim has no
// entry, and a member with no role claim has no provenance at all (nil, nil, empty list).
func roleProvenance(byRole map[string][]claims.Claim) (source, basis *string, perRole []RoleProvenance) {
	perRole = []RoleProvenance{}
	var all []claims.Claim
	for _, role := range roleOrder {
		cs := byRole[role]
		if len(cs) == 0 {
			continue
		}
		all = append(all, cs...)
		src, b := provenanceOf(strongestClaim(cs))
		perRole = append(perRole, RoleProvenance{Role: role, Source: src, Basis: b})
	}
	if len(all) == 0 {
		return nil, nil, perRole
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID }) // a stable input for the tie-break
	src, b := provenanceOf(strongestClaim(all))
	return ptr(src), ptr(b), perRole
}
