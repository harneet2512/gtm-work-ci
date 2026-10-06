package knowledge

import (
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func reducedState() reducer.AccountState {
	st := reducer.AccountState{AccountID: "acct", OpportunityID: strp(opp), AsOf: now}
	for _, name := range reducer.FieldNames() {
		*st.Fields.Field(name) = reducer.Field{Value: "unknown"}
	}
	st.Fields.Motion = reducer.Field{Value: "expansion", Known: true,
		EvidenceRefs: []reducer.EvidenceRef{{ActivityID: "a1", ClaimID: "c1"}, {ClaimID: "no-activity"}}}
	st.Fields.Stage = reducer.Field{Value: "Discovery", Known: true, Conflicts: []reducer.FieldConflict{{ClaimID: "c9"}}}
	st.Fields.Blockers = reducer.Field{Known: true, Value: []reducer.Item{{Text: "Security review", ClaimID: "c2", Status: "open"}}}
	st.Fields.Objections = reducer.Field{Value: []reducer.Item{}}
	st.BuyingGroup = []reducer.Member{{PersonID: "p1", Roles: []string{"champion"}, Status: "active"}}
	st.CoverageGaps = []string{"economic_buyer"}
	return st
}

func TestFromAccountStateMapsEveryField(t *testing.T) {
	sig := Signal{Type: "security_blocker_appeared", SubjectClaimID: strp("c2"), CreatedAt: now.Add(-40 * day)}
	s := FromAccountState(reducedState(), now, []Signal{sig}, "REORG", &Transition{FromState: "REORG", Status: "UNRESOLVED"})
	if s.OpportunityID != opp || s.RelationshipState != "REORG" || s.Transition == nil || len(s.Signals) != 1 {
		t.Fatalf("situation = %+v", s)
	}
	if v := s.Fields["motion"]; !v.Known || v.Scalar != "expansion" || len(v.EvidenceRefs) != 1 || v.EvidenceRefs[0].ClaimID != "c1" {
		t.Fatalf("motion = %+v", v)
	}
	if v := s.Fields["economic_buyer"]; v.Known {
		t.Fatalf("the literal unknown must map to an unknown value: %+v", v)
	}
	if v := s.Fields["blockers"]; !v.List || len(v.Items) != 1 || v.Items[0].ClaimID != "c2" {
		t.Fatalf("blockers = %+v", v)
	}
	if v := s.Fields["objections"]; !v.List || v.Known {
		t.Fatalf("objections = %+v", v)
	}
	if got := s.Conflicts["stage"]; len(got) != 1 || got[0] != "c9" {
		t.Fatalf("conflicts = %v", s.Conflicts)
	}
	checks := []struct {
		c    Condition
		want bool
	}{
		{cond("diff.security_blocker_appeared", OpExists), true},
		{cond("coverage_gaps", OpContains, "economic_buyer"), true},
		{cond("buying_group.champion", OpExists), true},
		{cond("economic_buyer", OpIsUnknown), true},
		{cond("transition.status", OpEq, "UNRESOLVED"), true},
	}
	for _, c := range checks {
		if got := holdsIn(t, s, c.c); got != c.want {
			t.Fatalf("%s = %v", render(c.c), got)
		}
	}
}

func TestFromAccountStateCopiesItsInputs(t *testing.T) {
	st := reducedState()
	signals := []Signal{{Type: "customer_replied", CreatedAt: now.Add(-time.Hour)}}
	s := FromAccountState(st, now, signals, "", nil)
	s.Signals[0].Type = "changed"
	s.BuyingGroup[0].Roles[0] = "changed"
	if signals[0].Type != "customer_replied" || st.BuyingGroup[0].Roles[0] != "champion" {
		t.Fatal("FromAccountState must not alias its inputs")
	}
	if s.OpportunityID != opp {
		t.Fatal("opportunity lost")
	}
	none := reducedState()
	none.OpportunityID = nil
	if FromAccountState(none, now, nil, "", nil).OpportunityID != "" {
		t.Fatal("no opportunity must map to empty")
	}
}
