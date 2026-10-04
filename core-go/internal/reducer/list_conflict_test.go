package reducer

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestListFieldCarriesItemConflictsAndTheirClaimsAreCompeting(t *testing.T) {
	human := claim(claims.FieldBlockers, `"resolved: EU budget line"`, claims.HumanApproved, 1, day(0))
	ai := claim(claims.FieldBlockers, `"open: EU budget line"`, claims.FirstPartyAI, 0.9, day(5))
	st, _ := Reduce(input(day(10), []claims.Claim{human, ai}, nil))
	mustValidate(t, st)
	f := st.Fields.Blockers
	if len(f.Conflicts) != 1 || f.Conflicts[0].ClaimID != ai.ID || f.Conflicts[0].Standing != "first_party_ai" {
		t.Fatalf("conflicts = %+v", f.Conflicts)
	}
	if got := ids(items(f)); len(got) != 1 || got[0] != "EU budget line=resolved" {
		t.Fatalf("the human-approved status stands: %v", got)
	}
	found := false
	for _, id := range f.CompetingClaimIDs {
		found = found || id == ai.ID
	}
	if !found {
		t.Fatalf("conflicting claim missing from competing_claim_ids %v", f.CompetingClaimIDs)
	}
}
