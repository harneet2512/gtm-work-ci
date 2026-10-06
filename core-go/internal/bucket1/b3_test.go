package bucket1

import "testing"

func b3Episode() Episode {
	ep := goodEpisode()
	ep.Claims[0].Supersedes, ep.Claims[0].SupersessionReason = "pc1", "the customer states a blocker explicitly"
	ep.PriorClaims[0].Status = "superseded"
	ep.PriorStatusAfter = map[string]string{"pc1": "superseded", "pc2": "active"}
	return ep
}

func TestB3GoodEpisodeIsUnknownOnlyForTheMissingModelJudgment(t *testing.T) {
	r := GradeB3(b3Episode(), nil)
	for _, n := range []string{"supersession_justified", "chronology_respected", "conflicts_surfaced", "unrelated_state_preserved"} {
		wantVerdict(t, byName(t, r, n), Pass)
	}
	wantVerdict(t, byName(t, r, "supporting_and_conflicting_links"), Unknown)
	if r.Verdict != Unknown {
		t.Fatal(r.Verdict)
	}
}

func TestB3SupersessionNeedsTheSameFieldAndAReason(t *testing.T) {
	ep := b3Episode()
	ep.Claims[0].SupersessionReason = ""
	wantVerdict(t, byName(t, GradeB3(ep, nil), "supersession_justified"), Fail)
	ep = b3Episode()
	ep.Claims[0].Supersedes = "pc2" // owner, not blockers
	wantVerdict(t, byName(t, GradeB3(ep, nil), "supersession_justified"), Fail)
	ep = b3Episode()
	ep.Claims[0].Supersedes = "ghost"
	wantVerdict(t, byName(t, GradeB3(ep, nil), "supersession_justified"), Fail)
}

func TestB3AnOlderClaimMustNotSupersedeANewerOne(t *testing.T) {
	ep := b3Episode()
	ep.PriorClaims[0].OccurredAt = t0.Add(48 * 3600e9)
	wantVerdict(t, byName(t, GradeB3(ep, nil), "chronology_respected"), Fail)
}

func TestB3AContradictedActivePriorFactCannotBeCarriedForwardSilently(t *testing.T) {
	ep := b3Episode()
	ep.Claims[0].Field, ep.PriorClaims[0].Field = "stage", "stage"
	ep.PriorClaims[0].Status = "active"
	ep.Claims[0].Supersedes, ep.Claims[0].SupersessionReason = "", ""
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Fail)
	ep.Claims[0].ConflictsWith = []string{"pc1"}
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Pass)
}

func TestB3UnrelatedPriorStateMustKeepItsStatus(t *testing.T) {
	ep := b3Episode()
	ep.PriorStatusAfter["pc2"] = "superseded"
	wantVerdict(t, byName(t, GradeB3(ep, nil), "unrelated_state_preserved"), Fail)
	ep.PriorStatusAfter = nil
	wantVerdict(t, byName(t, GradeB3(ep, nil), "unrelated_state_preserved"), Unknown)
}

func TestB3EmptyGraphIsNotMeasured(t *testing.T) {
	ep := goodEpisode()
	ep.Claims, ep.PriorClaims = nil, nil
	if GradeB3(ep, nil).Verdict != Unknown {
		t.Fatal("not measured")
	}
}

func TestB3ListFieldsMayHoldSeveralValues(t *testing.T) {
	ep := b3Episode()
	ep.PriorClaims[0].Status = "active"
	ep.Claims[0].Supersedes, ep.Claims[0].SupersessionReason = "", ""
	// blockers is a list field: a new blocker next to an active old one is not a contradiction
	wantVerdict(t, byName(t, GradeB3(ep, nil), "conflicts_surfaced"), Pass)
}
