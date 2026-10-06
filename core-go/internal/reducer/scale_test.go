package reducer

import (
	"fmt"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// syntheticAccount builds an account with n claims spread over the scalar fields, list items and
// people, and n/5 activities: the shape of a very busy account.
func syntheticAccount(n int) ([]claims.Claim, []Activity) {
	var cs []claims.Claim
	scalarFields := []claims.FieldPath{claims.FieldStage, claims.FieldHealth, claims.FieldNextMilestone, claims.FieldChampionStatus, claims.FieldDecisionProcess}
	for i := 0; i < n; i++ {
		var c claims.Claim
		switch i % 4 {
		case 0, 1:
			c = claim(scalarFields[i%len(scalarFields)], fmt.Sprintf(`"value %d"`, i%7), claims.FirstPartyAI, 0.8, day(i%400))
		case 2:
			c = claim(claims.FieldBlockers, fmt.Sprintf(`"open: blocker %d"`, i%300), claims.FirstPartyAI, 0.8, day(i%400))
		default:
			c = about(claim(claims.FieldStakeholderRole, `"user"`, claims.FirstPartyAI, 0.8, day(i%400)), id(20+i%50))
		}
		cs = append(cs, c)
	}
	acts := make([]Activity, n/5)
	for i := range acts {
		acts[i] = inboundEmail(100000+i, priya, day(i%400))
	}
	return cs, acts
}

// TestFullReconstructionCostStaysBounded backs ADR-0010: the recompute rebuilds the state from every
// claim and activity of the account instead of folding a delta. The pure part of that (adjudicate +
// reduce) must stay far below the budget of one debounce window even for an account 100x busier than
// the seed world (which has ~40 activities).
func TestFullReconstructionCostStaysBounded(t *testing.T) {
	for _, n := range []int{200, 2000, 20000} {
		cs, acts := syntheticAccount(n)
		start := time.Now()
		in := input(day(500), cs, acts)
		st, _ := Reduce(in)
		elapsed := time.Since(start)
		t.Logf("full reconstruction of %6d claims and %5d activities: %v (blockers=%d members=%d)", n, len(acts), elapsed.Round(time.Millisecond),
			len(items(st.Fields.Blockers)), len(st.BuyingGroup))
		if budget := 5 * time.Second; elapsed > budget {
			t.Errorf("%d claims took %v, over the %v budget", n, elapsed, budget)
		}
	}
}
