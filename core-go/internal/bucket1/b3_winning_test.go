package bucket1

import "testing"

func TestB3AnOldClaimThatNoLongerWinsItsFieldIsNotCarriedForward(t *testing.T) {
	ep := b3Episode()
	ep.Claims[0].Field, ep.PriorClaims[0].Field = "stage", "stage"
	ep.PriorClaims[0].Status = "active"
	ep.Claims[0].Supersedes, ep.Claims[0].SupersessionReason = "", ""
	ep.WinningClaims = map[string]string{"stage": "c1"}
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Pass)
	ep.WinningClaims = map[string]string{"stage": "pc1"} // the old claim still wins and nothing links the two
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Fail)
}
