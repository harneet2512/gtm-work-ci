package reducer

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestARoleInAMemberClaimStaysOnItsDeal(t *testing.T) {
	// The model extractor files "Priya is our champion" as a member claim carrying a role. On deal A it makes
	// her champion there; deal B, whose call she also joined, must not inherit the role or lose its gap.
	champ := on(about(claim(claims.FieldBuyingGroupMember, `{"role":"champion"}`, claims.FirstPartyAI, 0.9, day(2)), priya), dealA)
	eb := on(about(claim(claims.FieldBuyingGroupMember, `{"role":"economic_buyer"}`, claims.FirstPartyAI, 0.9, day(2)), priya), dealA)
	title := on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(1)), priya), dealA)
	acts := []Activity{dealActs(80, dealA, 3), dealActs(81, dealB, 4)}
	res := ReduceAll(input(day(20), []claims.Claim{champ, eb, title}, acts))

	a, b := deal(t, res, dealA), deal(t, res, dealB)
	if len(a.BuyingGroup) != 1 || strings.Join(a.BuyingGroup[0].Roles, ",") != "champion,economic_buyer" || len(a.CoverageGaps) != 0 {
		t.Fatalf("deal A = %+v gaps %v", a.BuyingGroup, a.CoverageGaps)
	}
	// Deal B has no role claim of its own: Priya is there (she took part and has an account-wide title) but with
	// no role, and its economic buyer is still a gap.
	if len(b.BuyingGroup) != 1 || strings.Join(b.BuyingGroup[0].Roles, ",") != "unknown" || len(b.CoverageGaps) != 1 || b.CoverageGaps[0] != "economic_buyer" {
		t.Fatalf("deal B = %+v gaps %v, must not inherit deal A's roles", b.BuyingGroup, b.CoverageGaps)
	}
}

func TestADealsLastChangeIsItsOwnNotAnotherDealsPersonFact(t *testing.T) {
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(2)), dealA),
		// A title said in deal B's activity, later than anything deal A did, about someone on deal A's calls.
		on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(9)), priya), dealB),
	}
	acts := []Activity{dealActs(80, dealA, 3), dealActs(81, dealB, 10)}
	a := deal(t, ReduceAll(input(day(20), cs, acts)), dealA)
	f := a.Fields.LastMeaningfulChange
	if !f.Known || !strings.HasPrefix(str(f), "stage updated") || len(f.EvidenceRefs) != 1 || !f.EvidenceRefs[0].OccurredAt.Equal(day(2)) {
		t.Fatalf("last_meaningful_change = %+v, want deal A's own stage change", f)
	}
}

func TestADealWithoutActivityIsNotRankedByAnotherDealsPersonFact(t *testing.T) {
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(1)), dealA),
		on(claim(claims.FieldStage, `"Quote"`, claims.CRMExplicit, 1, day(2)), dealB),
		on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(15)), priya), dealB),
	}
	// Neither deal has an activity; Priya, who has a title claim from day 15, took part in deal A's.
	acts := []Activity{dealActs(80, dealA, 0)}
	res := ReduceAll(input(day(20), cs, acts))
	if got := deal(t, res, dealB).evidenceAt; !got.Equal(day(2)) {
		t.Fatalf("deal B evidence = %v, want its own latest claim (day 2)", got)
	}
}

func TestASpellingOfTheSameEnumIsNotAConflict(t *testing.T) {
	deal1 := on(claim(claims.FieldRelationshipRisk, `"high"`, claims.CRMExplicit, 1, day(2)), dealA)
	same := on(claim(claims.FieldRelationshipRisk, `"High risk"`, claims.FirstPartyAI, 0.9, day(8)), "")
	res := ReduceAll(input(day(20), []claims.Claim{deal1, same}, []Activity{dealActs(80, dealA, 3)}))
	f := deal(t, res, dealA).Fields.RelationshipRisk
	if str(f) != "high" {
		t.Fatalf("risk = %v", f.Value)
	}
	if len(f.Conflicts) != 0 {
		t.Fatalf("two spellings of one enum value must not conflict: %+v", f.Conflicts)
	}
	unmappable := on(claim(claims.FieldRelationshipRisk, `"the vibes are off"`, claims.FirstPartyAI, 0.9, day(8)), "")
	res = ReduceAll(input(day(20), []claims.Claim{deal1, unmappable}, []Activity{dealActs(80, dealA, 3)}))
	if got := deal(t, res, dealA).Fields.RelationshipRisk.Conflicts; len(got) != 0 {
		t.Fatalf("a value that maps to no enum shows nothing to contradict: %+v", got)
	}
}

func TestHeadlineDoesNotShareMemoryWithItsDeal(t *testing.T) {
	cs := []claims.Claim{on(claim(claims.FieldBlockers, `"Security review"`, claims.FirstPartyAI, 0.9, day(2)), dealA)}
	res := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealA, 3)}))
	head, own := items(res.Account.Fields.Blockers), items(deal(t, res, dealA).Fields.Blockers)
	if len(head) != 1 || len(own) != 1 {
		t.Fatalf("head=%v own=%v", head, own)
	}
	head[0].Text = "changed"
	res.Account.Fields.Blockers.EvidenceRefs[0].Quote = "changed"
	if own[0].Text == "changed" || deal(t, res, dealA).Fields.Blockers.EvidenceRefs[0].Quote == "changed" {
		t.Fatal("the account headline must be a copy of the deal's field")
	}
}
