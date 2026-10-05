package signals_test

import (
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func headlineOf(opp, stage, champion string) func(*reducer.AccountState) {
	return func(s *reducer.AccountState) {
		s.OpportunityID = &opp
		s.Fields.Stage = known(stage, "stage-"+opp)
		s.Fields.ChampionStatus = known(champion, "champ-"+opp)
	}
}

func summary(opp, stage string, open bool, last string) reducer.OpportunitySummary {
	s := reducer.OpportunitySummary{OpportunityID: opp, Stage: stage, IsOpen: open, AsOf: t0}
	if last != "" {
		s.LastActivityID = &last
	}
	return s
}

// An email on another open deal moves the primary. Deal B is at an earlier stage with a weaker champion than
// deal A, which used to read as stage_regressed and champion_weakened and could start a run.
func TestANewPrimaryDealIsNotAStageRegressionOrAWeakenedChampion(t *testing.T) {
	prev := state(1, headlineOf("deal-a", "Negotiation", "active"))
	next := state(2, func(s *reducer.AccountState) {
		headlineOf("deal-b", "Discovery", "weakening")(s)
		s.CoverageGaps = []string{"economic_buyer"} // a real account-level change keeps the diff material
	})
	got := run(&prev, next, nil, nil)
	for _, bad := range []string{"stage_regressed", "stage_advanced", "champion_weakened", "champion_reactivated"} {
		for _, s := range got {
			if s.Type == bad {
				t.Errorf("%s emitted for a change of primary deal: %v", bad, types(got))
			}
		}
	}
	only(t, got, "stakeholder_gap") // account-scoped rules still run
}

func TestTheSamePrimaryStillEmitsItsStageSignal(t *testing.T) {
	prev := state(1, headlineOf("deal-a", "Discovery", "active"))
	next := state(2, headlineOf("deal-a", "Negotiation", "active"))
	only(t, run(&prev, next, nil, nil), "stage_advanced")
}

// A closed deal is never the primary, so its Closed Won cannot show in the headline. The deal-closed rule reads
// the per-deal summaries, tags the signal with the deal, and fires although the diff itself is not material.
func TestADealReachingClosedWonProducesTheStageSignalForThatDeal(t *testing.T) {
	prev := state(1, func(s *reducer.AccountState) {
		headlineOf("deal-a", "Negotiation", "active")(s)
		s.Opportunities = []reducer.OpportunitySummary{summary("deal-a", "Negotiation", true, "act-9"), summary("deal-b", "Discovery", true, "")}
	})
	next := state(2, func(s *reducer.AccountState) {
		headlineOf("deal-b", "Discovery", "active")(s) // deal-a closed, so deal-b is the primary now
		s.Opportunities = []reducer.OpportunitySummary{summary("deal-b", "Discovery", true, ""), summary("deal-a", "Closed Won", false, "act-10")}
	})
	got := run(&prev, next, nil, nil)
	s := only(t, got, "stage_advanced")
	if s.OpportunityID != "deal-a" || s.Details["to"] != "Closed Won" || s.Details["outcome"] != "won" || s.Rule != "sig.deal_closed@1" {
		t.Fatalf("signal = %+v", s)
	}
	if len(s.EvidenceRefs) != 1 || s.EvidenceRefs[0].ActivityID != "act-10" || s.ExpiresAt == nil {
		t.Fatalf("evidence or window = %+v / %v", s.EvidenceRefs, s.ExpiresAt)
	}
	if !strings.Contains(s.DedupeKey, ":v2:") {
		t.Errorf("dedupe key = %s", s.DedupeKey)
	}
}

func TestADealLostOrChurnedIsAStageRegressionTaggedWithItsDeal(t *testing.T) {
	for _, stage := range []string{"Closed Lost", "Churned", "Lost"} {
		prev := state(1, func(s *reducer.AccountState) {
			s.Opportunities = []reducer.OpportunitySummary{summary("deal-a", "Negotiation", true, "")}
		})
		next := state(2, func(s *reducer.AccountState) {
			s.Opportunities = []reducer.OpportunitySummary{summary("deal-a", stage, false, "")}
		})
		s := only(t, run(&prev, next, nil, nil), "stage_regressed")
		if s.OpportunityID != "deal-a" || s.Details["outcome"] != "lost" {
			t.Errorf("%s: %+v", stage, s)
		}
	}
}

func TestNoDealClosedSignalForADealThatWasAlreadyClosedOrIsNew(t *testing.T) {
	prev := state(1, func(s *reducer.AccountState) {
		s.Opportunities = []reducer.OpportunitySummary{summary("deal-a", "Closed Won", false, "")}
	})
	next := state(2, func(s *reducer.AccountState) {
		s.Opportunities = []reducer.OpportunitySummary{summary("deal-a", "Closed Won", false, ""), summary("deal-new", "Closed Lost", false, "")}
	})
	if got := run(&prev, next, nil, nil); len(got) != 0 {
		t.Fatalf("emitted %v", types(got))
	}
	if got := run(nil, next, nil, nil); len(got) != 0 {
		t.Fatalf("a first state reports nothing about closing: %v", types(got))
	}
}
