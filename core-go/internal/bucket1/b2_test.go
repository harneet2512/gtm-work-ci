package bucket1

import "testing"

func TestB2GoodEpisodePasses(t *testing.T) {
	r := GradeB2(goodEpisode())
	if r.Verdict != Pass || r.Grader != GraderDeterministic || len(r.EvidenceRefs) == 0 {
		t.Fatalf("%s: %s", r.Verdict, r.Why)
	}
}

func TestB2APersonFromAnotherAccountFails(t *testing.T) {
	ep := goodEpisode()
	ep.People[0].AccountID = "acc-2"
	wantVerdict(t, byName(t, GradeB2(ep), "person_resolved"), Fail)
}

func TestB2AnOpportunityOfAnotherAccountFails(t *testing.T) {
	ep := goodEpisode()
	ep.Opportunities[0].AccountID = "acc-2"
	wantVerdict(t, byName(t, GradeB2(ep), "account_opportunity_correct"), Fail)
}

func TestB2InternalExternalFollowsTheEmailDomain(t *testing.T) {
	ep := goodEpisode()
	ep.Resolutions[0].Internal = true // the person is external
	wantVerdict(t, byName(t, GradeB2(ep), "internal_external_correct"), Fail)
	ep = goodEpisode()
	ep.People[0].Email = "lee@seller.example" // internal domain, directory says external
	wantVerdict(t, byName(t, GradeB2(ep), "internal_external_correct"), Fail)
}

func TestB2AmbiguousIdentityMustAbstain(t *testing.T) {
	ep := goodEpisode()
	ep.Resolutions[0].Candidates = []string{"p-ext", "p-other"}
	wantVerdict(t, byName(t, GradeB2(ep), "ambiguous_identity_abstains"), Fail)
	ep.Resolutions[0].PersonID = ""
	wantVerdict(t, byName(t, GradeB2(ep), "ambiguous_identity_abstains"), Pass)
}

func TestB2CrossAccountClaimsFail(t *testing.T) {
	ep := goodEpisode()
	ep.PriorClaims[0].AccountID = "acc-2"
	wantVerdict(t, byName(t, GradeB2(ep), "no_cross_account_contamination"), Fail)
	ep = goodEpisode()
	ep.Claims[0].AccountID = "acc-2"
	wantVerdict(t, byName(t, GradeB2(ep), "no_cross_account_contamination"), Fail)
}

func TestB2LostSourceMappingFails(t *testing.T) {
	ep := goodEpisode()
	ep.Resolutions[0].Mapping = ""
	wantVerdict(t, byName(t, GradeB2(ep), "source_mapping_retained"), Fail)
}

func TestB2NoResolutionsIsNotMeasured(t *testing.T) {
	ep := goodEpisode()
	ep.Resolutions = nil
	if r := GradeB2(ep); r.Verdict != Unknown {
		t.Fatal(r.Verdict)
	}
}
