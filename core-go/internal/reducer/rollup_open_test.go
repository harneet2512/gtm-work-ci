package reducer

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

func TestAccountGroupAndGapsComeFromOpenDealsOnly(t *testing.T) {
	// An economic buyer from an old closed-lost deal must not hide the open deal's gap.
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Negotiation"`, claims.CRMExplicit, 1, day(5)), dealA),
		on(claim(claims.FieldStage, `"Closed Lost"`, claims.CRMExplicit, 1, day(2)), dealC),
		on(claim(claims.FieldEconomicBuyer, `"`+owen+`"`, claims.CRMExplicit, 1, day(1)), dealC),
		on(about(claim(claims.FieldBuyingGroupMember, `{"role":"champion"}`, claims.CRMExplicit, 1, day(4)), priya), dealA),
	}
	acts := []Activity{dealActs(80, dealA, 5), dealActs(81, dealC, 2)}
	res := ReduceAll(input(day(20), cs, acts))

	if got := peopleOf(res.Account.BuyingGroup); strings.Contains(got, owen) || !strings.Contains(got, priya) {
		t.Errorf("account group = %s, want the open deal's people only", got)
	}
	if got := strings.Join(res.Account.CoverageGaps, ","); !strings.Contains(got, "economic_buyer") {
		t.Errorf("account gaps = %q, the open deal has no economic buyer", got)
	}
	if got := peopleOf(deal(t, res, dealC).BuyingGroup); !strings.Contains(got, owen) {
		t.Errorf("the closed deal still lists its own people: %s", got)
	}
	mustValidate(t, res.Account)
}

func TestNoOpenDealMeansNoGapsButAccountScopedPeopleStay(t *testing.T) {
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Closed Won"`, claims.CRMExplicit, 1, day(3)), dealC),
		about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(1)), owen),
	}
	acct := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealC, 3)})).Account
	if len(acct.CoverageGaps) != 0 {
		t.Errorf("gaps = %v: with no open deal nothing needs a role", acct.CoverageGaps)
	}
	if !strings.Contains(peopleOf(acct.BuyingGroup), owen) {
		t.Errorf("an account-scoped person fact stays: %s", peopleOf(acct.BuyingGroup))
	}
}

func TestStageClosedKnowsMoreThanThePrefix(t *testing.T) {
	for stage, want := range map[string]bool{
		"Closed": true, "Closed Won": true, "closed-lost": true, "Won": true, "Lost": true, "Churned": true,
		" closed won ": true, "Negotiation": false, "Discovery": false, "Wonderful fit": false, "": false,
	} {
		if got := StageClosed(stage); got != want {
			t.Errorf("StageClosed(%q) = %v, want %v", stage, got, want)
		}
	}
	if !StageWon("Closed Won") || !StageWon("won") || StageWon("Closed Lost") || StageWon("Churned") || StageWon("Negotiation") {
		t.Error("StageWon must be true only for a closed stage that says won")
	}
}

func TestAWonOrLostStageClosesTheDeal(t *testing.T) {
	for _, stage := range []string{"Lost", "Churned", "Won"} {
		cs := []claims.Claim{on(claim(claims.FieldStage, `"`+stage+`"`, claims.CRMExplicit, 1, day(3)), dealA)}
		res := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealA, 3)}))
		if deal(t, res, dealA).IsOpen || res.Account.OpportunityID != nil {
			t.Errorf("stage %q must close the deal and leave no primary", stage)
		}
	}
}

func TestPrimaryChanged(t *testing.T) {
	a, b := dealA, dealB
	st := func(opp *string) AccountState { return AccountState{OpportunityID: opp} }
	prevA := st(&a)
	for name, c := range map[string]struct {
		prev *AccountState
		next AccountState
		want bool
	}{
		"first state":         {nil, st(&a), false},
		"same":                {&prevA, st(&a), false},
		"other deal":          {&prevA, st(&b), true},
		"primary lost":        {&prevA, st(nil), true},
		"primary appeared":    {&AccountState{}, st(&a), true},
		"no primary, no deal": {&AccountState{}, st(nil), false},
	} {
		if got := PrimaryChanged(c.prev, c.next); got != c.want {
			t.Errorf("%s: PrimaryChanged = %v, want %v", name, got, c.want)
		}
	}
}

func TestProvenanceIsPerRoleNotPerMember(t *testing.T) {
	// One person: a CRM-recorded economic buyer and a model-inferred champion.
	eb := on(claim(claims.FieldEconomicBuyer, `"`+priya+`"`, claims.CRMExplicit, 1, day(2)), dealA)
	ch := on(about(claim(claims.FieldBuyingGroupMember, `{"role":"champion"}`, claims.FirstPartyAI, 0.9, day(3)), priya), dealA)
	res := ReduceAll(input(day(20), []claims.Claim{eb, ch}, []Activity{dealActs(80, dealA, 3)}))

	for name, group := range map[string][]Member{"deal": deal(t, res, dealA).BuyingGroup, "account": res.Account.BuyingGroup} {
		m := roleOf(t, group, priya)
		got := map[string]RoleProvenance{}
		for _, p := range m.RoleProvenance {
			got[p.Role] = p
		}
		if got["economic_buyer"].Source != RoleRecorded || got["champion"].Source != RoleInferred || len(got) != 2 {
			t.Errorf("%s: per-role provenance = %+v, want economic_buyer recorded and champion inferred", name, m.RoleProvenance)
		}
		if m.RoleProvenance[0].Role != "champion" || m.RoleProvenance[1].Role != "economic_buyer" {
			t.Errorf("%s: provenance must follow the order of roles: %+v", name, m.RoleProvenance)
		}
		if m.RoleSource == nil || *m.RoleSource != RoleRecorded {
			t.Errorf("%s: the member-level summary is the strongest claim's: %v", name, deref(m.RoleSource))
		}
	}
	mustValidate(t, res.Account)
}

func TestAMemberWithoutARoleHasNoProvenance(t *testing.T) {
	title := on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(1)), owen), dealA)
	res := ReduceAll(input(day(20), []claims.Claim{title}, []Activity{dealActs(80, dealA, 3)}))
	m := roleOf(t, res.Account.BuyingGroup, owen)
	if m.RoleProvenance == nil || len(m.RoleProvenance) != 0 || m.RoleSource != nil {
		t.Errorf("no role, no provenance: %+v", m)
	}
}
