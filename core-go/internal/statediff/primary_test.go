package statediff_test

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/statediff"
)

func headline(opp, stage, champion string) func(*reducer.AccountState) {
	return func(s *reducer.AccountState) {
		s.OpportunityID = &opp
		s.Fields.Stage = known(stage, "stage-"+opp)
		s.Fields.ChampionStatus = known(champion, "champ-"+opp)
		s.Fields.Stage.OpportunityID = &opp
	}
}

// An email on another open deal changes the primary. The two deals' headlines differ, but that is not a stage
// change or a champion change: the diff must say only that the primary changed, and must not be material.
func TestAPrimaryChangeIsNotAStageOrChampionChange(t *testing.T) {
	prev := state(1, headline("deal-a", "Negotiation", "active"))
	next := state(2, headline("deal-b", "Discovery", "weakening"))
	d := statediff.Compute(&prev, next, []string{"act-1"})

	for _, f := range []string{"stage", "champion_status"} {
		if _, ok := d.Change(f); ok {
			t.Errorf("%s: a difference between two deals is not a change", f)
		}
	}
	c, ok := d.Change(statediff.FieldPrimaryChanged)
	if !ok || c.Op != statediff.OpChanged || c.Material || c.Before != "deal-a" || c.After != "deal-b" {
		t.Fatalf("primary change = %+v (found %v)", c, ok)
	}
	if d.IsMaterial {
		t.Errorf("a primary change alone must not make a diff material: %+v", d)
	}
}

func TestAccountScopedFieldsAreStillComparedWhenThePrimaryChanges(t *testing.T) {
	prev := state(1, func(s *reducer.AccountState) {
		headline("deal-a", "Negotiation", "active")(s)
		s.CoverageGaps = []string{}
	})
	next := state(2, func(s *reducer.AccountState) {
		headline("deal-b", "Discovery", "active")(s)
		s.CoverageGaps = []string{"economic_buyer"}
		s.BuyingGroup = []reducer.Member{{PersonID: "p1", Roles: []string{"champion"}, Status: "new"}}
	})
	d := statediff.Compute(&prev, next, nil)
	for _, f := range []string{statediff.FieldCoverageGaps, statediff.FieldBuyingGroup} {
		if c, ok := d.Change(f); !ok || !c.Material {
			t.Errorf("%s must still be a material change: %+v", f, c)
		}
	}
	if !d.IsMaterial {
		t.Error("the diff is material through the account-scoped changes")
	}
}

func TestPrimaryLostOrAppearingIsAPrimaryChange(t *testing.T) {
	withDeal := state(1, headline("deal-a", "Negotiation", "active"))
	without := state(2, nil)
	d := statediff.Compute(&withDeal, without, nil)
	if c, ok := d.Change(statediff.FieldPrimaryChanged); !ok || c.Before != "deal-a" || c.After != nil {
		t.Fatalf("all deals closed: %+v", c)
	}
	d = statediff.Compute(&without, state(3, headline("deal-b", "Discovery", "active")), nil)
	if c, ok := d.Change(statediff.FieldPrimaryChanged); !ok || c.Before != nil || c.After != "deal-b" {
		t.Fatalf("a deal appears: %+v", c)
	}
}

func TestSamePrimaryStillDiffsItsFieldsAndTagsTheDeal(t *testing.T) {
	prev := state(1, headline("deal-a", "Discovery", "active"))
	next := state(2, headline("deal-a", "Negotiation", "active"))
	d := statediff.Compute(&prev, next, nil)
	c, ok := d.Change("stage")
	if !ok || !c.Material || c.OpportunityID == nil || *c.OpportunityID != "deal-a" {
		t.Fatalf("stage change = %+v", c)
	}
	if _, ok := d.Change(statediff.FieldPrimaryChanged); ok {
		t.Error("no primary change when the primary is the same")
	}
}
