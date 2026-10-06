package bucket1

import "testing"

func TestB3WithoutTheStateAfterTheEventADisagreementIsUnknownNotFail(t *testing.T) {
	ep := b3Episode()
	ep.Claims[0].Field, ep.PriorClaims[0].Field = "stage", "stage"
	ep.PriorClaims[0].Status = "active"
	ep.Claims[0].Supersedes, ep.Claims[0].SupersessionReason = "", ""
	ep.StateUnread = true
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Unknown)
}
