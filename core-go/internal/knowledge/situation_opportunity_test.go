package knowledge

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

const otherOpp = "00000000-0000-4000-8000-0000000000a2"

func dealState(deal, stage string) reducer.OpportunityState {
	st := reducer.OpportunityState{AccountID: "acct", OpportunityID: deal, AsOf: now, IsOpen: true}
	for _, name := range reducer.FieldNames() {
		*st.Fields.Field(name) = reducer.Field{Value: "unknown"}
	}
	st.Fields.Stage = reducer.Field{Value: stage, Known: true, Conflicts: []reducer.FieldConflict{{ClaimID: "c-" + deal}}}
	st.Fields.Blockers = reducer.Field{Known: true, Value: []reducer.Item{{Text: "Security review", ClaimID: "c2", Status: "open"}}}
	st.BuyingGroup = []reducer.Member{{PersonID: "p-" + deal, Roles: []string{"champion"}, Status: "active"}}
	st.CoverageGaps = []string{"legal"}
	return st
}

func TestFromOpportunityStateReadsOnlyThatDeal(t *testing.T) {
	s := FromOpportunityState(dealState(opp, "Negotiation"), now, nil, "REORG", &Transition{FromState: "REORG", Status: "UNRESOLVED"})
	if s.AccountID != "acct" || s.OpportunityID != opp || s.RelationshipState != "REORG" || s.Transition == nil {
		t.Fatalf("situation = %+v", s)
	}
	if v := s.Fields["stage"]; !v.Known || v.Scalar != "Negotiation" {
		t.Fatalf("stage = %+v", v)
	}
	if got := s.Conflicts["stage"]; len(got) != 1 || got[0] != "c-"+opp {
		t.Fatalf("conflicts = %v", s.Conflicts)
	}
	if len(s.BuyingGroup) != 1 || s.BuyingGroup[0].PersonID != "p-"+opp {
		t.Fatalf("buying group = %+v", s.BuyingGroup)
	}
	if !holdsIn(t, s, cond("coverage_gaps", OpContains, "legal")) || !holdsIn(t, s, cond("blockers", OpExists)) || holdsIn(t, s, cond("stage", OpEq, "Discovery")) {
		t.Fatal("conditions must evaluate against the deal's own fields")
	}
	o := FromOpportunityState(dealState(otherOpp, "Discovery"), now, nil, "", nil)
	if !holdsIn(t, o, cond("stage", OpEq, "Discovery")) || o.OpportunityID != otherOpp {
		t.Fatalf("a different deal of the same account is a different situation: %+v", o.Fields["stage"])
	}
}

func TestFromOpportunityStateKeepsSignalsOfThisDealOrNoDeal(t *testing.T) {
	signals := []Signal{
		{ID: "mine", Type: "customer_replied", OpportunityID: strp(opp), CreatedAt: now},
		{ID: "account", Type: "customer_replied", CreatedAt: now},
		{ID: "theirs", Type: "customer_replied", OpportunityID: strp(otherOpp), CreatedAt: now},
	}
	s := FromOpportunityState(dealState(opp, "Quote"), now, signals, "", nil)
	var got []string
	for _, sig := range s.Signals {
		got = append(got, sig.ID)
	}
	if len(got) != 2 || got[0] != "mine" || got[1] != "account" {
		t.Fatalf("signals = %v, want this deal's and the account's", got)
	}
	s.Signals[0].Type = "changed"
	s.BuyingGroup[0].Roles[0] = "changed"
	if signals[0].Type != "customer_replied" {
		t.Fatal("FromOpportunityState must not alias its inputs")
	}
}

func TestAccountSituationReadsLikeItsPrimaryDeal(t *testing.T) {
	st := reducedState() // OpportunityID = opp, the primary
	s := FromAccountState(st, now, nil, "", nil)
	d := dealState(opp, "Discovery")
	d.Fields.Stage, d.Fields.Motion, d.Fields.Blockers = st.Fields.Stage, st.Fields.Motion, st.Fields.Blockers
	d.CoverageGaps = st.CoverageGaps
	ds := FromOpportunityState(d, now, nil, "", nil)
	for _, name := range []string{"stage", "motion", "blockers", "coverage_gaps"} {
		if s.Fields[name].Known != ds.Fields[name].Known || len(s.Fields[name].Items) != len(ds.Fields[name].Items) {
			t.Fatalf("%s: the account headline and the deal view must read alike", name)
		}
	}
}
