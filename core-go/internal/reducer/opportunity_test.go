package reducer

import (
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

var (
	dealA = id(21) // the busier, open deal
	dealB = id(22) // an older open deal
	dealC = id(23) // a closed deal
)

// on scopes a claim to a deal ("" = the account).
func on(c claims.Claim, deal string) claims.Claim { c.OpportunityID = deal; return c }

func dealActs(n int, deal string, when int) Activity {
	return Activity{ID: id(n), Type: "EmailReceived", OccurredAt: day(when), OpportunityID: deal,
		Participants: []Participant{{PersonID: priya, RawIdentity: "p@customer.com", Role: "from"}}}
}

func twoDeals() []claims.Claim {
	return []claims.Claim{
		on(claim(claims.FieldStage, `"Negotiation"`, claims.CRMExplicit, 1, day(5)), dealA),
		on(claim(claims.FieldOwner, `"`+dana+`"`, claims.CRMExplicit, 1, day(5)), dealA),
		on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(3)), dealB),
		on(claim(claims.FieldOwner, `"`+owen+`"`, claims.CRMExplicit, 1, day(3)), dealB),
		on(claim(claims.FieldStage, `"Closed"`, claims.CRMExplicit, 1, day(9)), dealC),
	}
}

func twoDealActs() []Activity {
	return []Activity{dealActs(80, dealA, 6), dealActs(81, dealB, 4), dealActs(82, dealC, 9)}
}

func deal(t *testing.T, res Result, opp string) OpportunityState {
	t.Helper()
	for _, d := range res.Opportunities {
		if d.OpportunityID == opp {
			return d
		}
	}
	t.Fatalf("no state for deal %s", opp)
	return OpportunityState{}
}

func TestEachDealFoldsOnlyItsOwnClaims(t *testing.T) {
	res := ReduceAll(input(day(20), twoDeals(), twoDealActs()))
	a, b := deal(t, res, dealA), deal(t, res, dealB)
	if str(a.Fields.Stage) != "Negotiation" || str(a.Fields.Owner) != dana {
		t.Fatalf("deal A = %v / %v", a.Fields.Stage.Value, a.Fields.Owner.Value)
	}
	if str(b.Fields.Stage) != "Discovery" || str(b.Fields.Owner) != owen {
		t.Fatalf("deal B = %v / %v", b.Fields.Stage.Value, b.Fields.Owner.Value)
	}
	if len(res.Opportunities) != 3 {
		t.Fatalf("deals = %d, want 3", len(res.Opportunities))
	}
}

func TestAccountHeadlineIsThePrimaryDealNotAMixture(t *testing.T) {
	res := ReduceAll(input(day(20), twoDeals(), twoDealActs()))
	acct := res.Account
	// A is the open deal with the latest activity; C is newer but closed.
	if acct.OpportunityID == nil || *acct.OpportunityID != dealA {
		t.Fatalf("primary = %v, want %s", acct.OpportunityID, dealA)
	}
	if str(acct.Fields.Stage) != "Negotiation" || str(acct.Fields.Owner) != dana {
		t.Fatalf("headline = %v / %v", acct.Fields.Stage.Value, acct.Fields.Owner.Value)
	}
	if got := acct.Fields.Stage.OpportunityID; got == nil || *got != dealA {
		t.Fatalf("headline must name its deal, got %v", got)
	}
	var order []string
	for _, s := range acct.Opportunities {
		order = append(order, s.OpportunityID)
	}
	if len(order) != 3 || order[0] != dealA || order[1] != dealB || order[2] != dealC {
		t.Fatalf("summaries = %v, want primary, open, closed", order)
	}
	if !acct.Opportunities[0].IsPrimary || acct.Opportunities[1].IsPrimary || acct.Opportunities[2].IsOpen {
		t.Fatalf("flags wrong: %+v", acct.Opportunities)
	}
	if acct.Opportunities[2].Stage != "Closed" || acct.Opportunities[1].Owner != owen {
		t.Fatalf("summaries = %+v", acct.Opportunities)
	}
}

func TestNoOpenDealMeansNoPrimaryAndAccountScopeOnly(t *testing.T) {
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Closed"`, claims.CRMExplicit, 1, day(3)), dealC),
		on(claim(claims.FieldHealth, `"at_risk"`, claims.FirstPartyAI, 0.9, day(4)), ""),
	}
	acct := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealC, 3)})).Account
	if acct.OpportunityID != nil {
		t.Fatalf("primary = %v, want null", *acct.OpportunityID)
	}
	if acct.Fields.Stage.Known {
		t.Fatalf("a closed deal must not be the account's current stage: %v", acct.Fields.Stage.Value)
	}
	if str(acct.Fields.Health) != "at_risk" || acct.Fields.Health.OpportunityID != nil {
		t.Fatalf("an unattributed claim is the account-scoped fallback: %+v", acct.Fields.Health)
	}
}

func TestUnattributedClaimsNeverEnterADeal(t *testing.T) {
	cs := []claims.Claim{
		on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(2)), dealA),
		on(claim(claims.FieldHealth, `"at_risk"`, claims.FirstPartyAI, 0.9, day(4)), ""),
	}
	res := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealA, 3)}))
	if d := deal(t, res, dealA); d.Fields.Health.Known {
		t.Fatalf("deal inherited an account-scoped claim: %v", d.Fields.Health.Value)
	}
}

func TestNewerUnattributedClaimIsSurfacedAsAConflictAndNeverWins(t *testing.T) {
	older := on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(2)), dealA)
	newer := on(claim(claims.FieldStage, `"Negotiation"`, claims.FirstPartyAI, 0.9, day(8)), "")
	res := ReduceAll(input(day(20), []claims.Claim{older, newer}, []Activity{dealActs(80, dealA, 3)}))
	f := deal(t, res, dealA).Fields.Stage
	if str(f) != "Discovery" || f.WinningClaimID == nil || *f.WinningClaimID != older.ID {
		t.Fatalf("the deal's own claim must stand: %+v", f)
	}
	if len(f.Conflicts) != 1 || f.Conflicts[0].ClaimID != newer.ID || len(f.CompetingClaimIDs) != 1 {
		t.Fatalf("conflicts = %+v competing = %v", f.Conflicts, f.CompetingClaimIDs)
	}
	// Older or equal unattributed claims, and agreement, raise nothing.
	quiet := on(claim(claims.FieldStage, `"Discovery"`, claims.FirstPartyAI, 0.9, day(8)), "")
	res = ReduceAll(input(day(20), []claims.Claim{older, quiet}, []Activity{dealActs(80, dealA, 3)}))
	if got := deal(t, res, dealA).Fields.Stage.Conflicts; len(got) != 0 {
		t.Fatalf("agreement must not conflict: %+v", got)
	}
}

func TestDealClaimsDoNotCompeteWithOtherDealsOrTheAccount(t *testing.T) {
	// Same field, same standing, the other deal's claim is newer: it must not outrank this deal's.
	cs := []claims.Claim{
		on(claim(claims.FieldOwner, `"`+dana+`"`, claims.CRMExplicit, 1, day(1)), dealA),
		on(claim(claims.FieldOwner, `"`+owen+`"`, claims.CRMExplicit, 1, day(9)), dealB),
	}
	adj := claims.Adjudicate(cs, day(20))
	if len(adj.Updates) != 0 {
		t.Fatalf("scoped claims must not outrank each other: %v", adj.Updates)
	}
}

func TestDealBuyingGroupHoldsOnlyItsPeopleAndAccountFactsAboutThem(t *testing.T) {
	role := func(person, r string, deal string, when int) claims.Claim {
		return on(about(claim(claims.FieldStakeholderRole, `"`+r+`"`, claims.CRMExplicit, 1, day(when)), person), deal)
	}
	title := on(about(claim(claims.FieldBuyingGroupMember, `{"title":"CFO"}`, claims.CRMExplicit, 1, day(1)), owen), dealB)
	cs := []claims.Claim{role(priya, "security", dealA, 2), role(owen, "legal", dealB, 2), title,
		on(about(claim(claims.FieldBuyingGroupMember, `{}`, claims.CRMExplicit, 1, day(1)), marco), "")}
	acts := []Activity{dealActs(80, dealA, 3), {ID: id(81), Type: "EmailReceived", OccurredAt: day(4), OpportunityID: dealB,
		Participants: []Participant{{PersonID: owen, RawIdentity: "o@c.com", Role: "from"}}}}
	res := ReduceAll(input(day(20), cs, acts))

	if got := peopleOf(deal(t, res, dealA).BuyingGroup); got != priya {
		t.Fatalf("deal A group = %q, want only Priya", got)
	}
	b := deal(t, res, dealB).BuyingGroup
	if peopleOf(b) != owen || b[0].Title == nil || *b[0].Title != "CFO" {
		t.Fatalf("deal B group = %+v", b)
	}
	// The account sees everyone it has evidence for, including the person no deal mentions.
	if got := len(res.Account.BuyingGroup); got != 3 {
		t.Fatalf("account group = %d members, want 3", got)
	}
}

func peopleOf(ms []Member) string {
	out := ""
	for _, m := range ms {
		if out != "" {
			out += ","
		}
		out += m.PersonID
	}
	return out
}

func TestAmountFoldsIntoTheDealOnly(t *testing.T) {
	cs := []claims.Claim{on(claim(claims.FieldAmount, `120000`, claims.CRMExplicit, 1, day(2)), dealA)}
	res := ReduceAll(input(day(20), cs, []Activity{dealActs(80, dealA, 3)}))
	d := deal(t, res, dealA)
	if v, ok := d.Fields.Amount.Value.(float64); !d.Fields.Amount.Known || !ok || v != 120000 {
		t.Fatalf("amount = %+v", d.Fields.Amount)
	}
	if s := res.Account.Opportunities[0]; s.Amount == nil || *s.Amount != 120000 {
		t.Fatalf("summary amount = %v", s.Amount)
	}
	if deal(t, ReduceAll(input(day(20), nil, []Activity{dealActs(80, dealA, 3)})), dealA).Fields.Amount.Known {
		t.Fatal("amount without a claim must be unknown")
	}
}

func TestStatesValidateAgainstTheContracts(t *testing.T) {
	res := ReduceAll(input(day(20), twoDeals(), twoDealActs()))
	mustValidate(t, res.Account)
	for _, d := range res.Opportunities {
		d.Version = 1
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if err := validat.Validate("opportunity_state", raw); err != nil {
			t.Fatalf("deal %s violates opportunity_state.v1.json: %v\n%s", d.OpportunityID, err, raw)
		}
	}
}

func TestReduceAllIsDeterministicIdempotentAndOrderIndependent(t *testing.T) {
	cs, acts := twoDeals(), twoDealActs()
	first := ReduceAll(input(day(20), cs, acts))
	again := ReduceAll(input(day(20), cs, acts))
	rev := append([]claims.Claim(nil), cs...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	revActs := []Activity{acts[2], acts[1], acts[0]}
	second := ReduceAll(input(day(20), rev, revActs))
	a, _ := json.Marshal(first)
	for name, other := range map[string]Result{"again": again, "reordered": second} {
		b, _ := json.Marshal(other)
		if string(a) != string(b) {
			t.Fatalf("%s: result differs:\n%s\n%s", name, a, b)
		}
	}
}

func TestReduceKeepsTheAccountOnlyShapeWhenNoClaimNamesADeal(t *testing.T) {
	cs := []claims.Claim{on(claim(claims.FieldStage, `"Discovery"`, claims.CRMExplicit, 1, day(2)), "")}
	res := ReduceAll(input(day(20), cs, nil))
	if len(res.Opportunities) != 0 || res.Account.OpportunityID != nil || len(res.Account.Opportunities) != 0 {
		t.Fatalf("no deal evidence, no deals: %+v", res.Opportunities)
	}
	if str(res.Account.Fields.Stage) != "Discovery" || res.Account.Fields.Stage.OpportunityID != nil {
		t.Fatalf("account-scoped stage must fold as before: %+v", res.Account.Fields.Stage)
	}
	mustValidate(t, res.Account)
}
