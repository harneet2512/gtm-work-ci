package demomine

import (
	"fmt"
	"strings"
	"testing"
)

// seqOf builds a sequence whose events move the given dimensions in order; the last entry is the held-out event.
func seqOf(opp string, moves ...[]string) Sequence {
	s := Sequence{AccountID: "acct-" + opp, OpportunityID: opp, AccountName: "A " + opp, OpportunityName: "O " + opp}
	for i, dims := range moves {
		r := rec(i+1, i+1, dims...)
		r.EventID = fmt.Sprintf("%s-e%d", opp, i+1)
		s.Events = append(s.Events, r)
	}
	return s
}

func dims(d ...string) []string { return d }

func TestCaseCarriesItsDimensionSignature(t *testing.T) {
	c := loadConfig(t)
	cs, ok := c.BestCase(seqOf("A", dims(DimIntent), dims(DimIntent, DimRisk), dims(DimStakeholder), dims(DimNextStep, DimAction)))
	if !ok {
		t.Fatal("no case")
	}
	sig := cs.Signature
	if sig.History[DimIntent] != 2 || sig.History[DimRisk] != 1 || sig.History[DimStakeholder] != 1 || len(sig.History) != 3 {
		t.Errorf("history signature counts the history events that moved each dimension: %+v", sig.History)
	}
	if strings.Join(sig.HeldOut, ",") != DimNextStep+","+DimAction {
		t.Errorf("held-out signature %v", sig.HeldOut)
	}
}

func TestSimilarToRanksTheClosestShapeFirstAndExplainsTheScore(t *testing.T) {
	c := loadConfig(t)
	twin := func(opp string) Sequence {
		return seqOf(opp, dims(DimIntent), dims(DimIntent, DimRisk), dims(DimStakeholder), dims(DimNextStep, DimAction))
	}
	near := seqOf("B", dims(DimIntent), dims(DimIntent, DimRisk), dims(DimStakeholder), dims(DimNextStep))
	far := seqOf("C", dims(DimOwnership), dims(DimOwnership, DimAction), dims(DimRisk), dims(DimRisk, DimStakeholder))
	cases := c.Rank([]Sequence{twin("A"), twin("A2"), near, far})
	byOpp := map[string]Case{}
	for _, cs := range cases {
		byOpp[cs.OpportunityID] = cs
	}
	a := byOpp["A"]
	if len(a.SimilarTo) == 0 {
		t.Fatal("similar_to is empty")
	}
	if a.SimilarTo[0].OpportunityID != "A2" || a.SimilarTo[0].Similarity != 1 {
		t.Fatalf("an identical shape must rank first with similarity 1: %+v", a.SimilarTo[0])
	}
	if len(a.SimilarTo) < 2 || a.SimilarTo[1].OpportunityID != "B" || a.SimilarTo[1].Similarity >= 1 {
		t.Fatalf("the near case must come second, below 1: %+v", a.SimilarTo)
	}
	for i, s := range a.SimilarTo {
		if s.OpportunityID == "A" {
			t.Error("a case is not similar to itself")
		}
		if s.Rank == 0 || s.Explanation == "" || s.Similarity <= 0 || s.Similarity > 1 {
			t.Errorf("entry %d is incomplete: %+v", i, s)
		}
		if i > 0 && s.Similarity > a.SimilarTo[i-1].Similarity {
			t.Errorf("similar_to is not ranked by similarity: %+v", a.SimilarTo)
		}
	}
	e := a.SimilarTo[1].Explanation
	for _, want := range []string{"history", "held-out", "shared", "0.5"} {
		if !strings.Contains(e, want) {
			t.Errorf("the explanation must show its parts (%q missing): %s", want, e)
		}
	}
	if len(a.SimilarTo) > SimilarCount {
		t.Errorf("at most %d similar cases are listed", SimilarCount)
	}
}

func TestSimilarityIsSymmetricAndZeroWhenNothingIsShared(t *testing.T) {
	a := Signature{History: map[string]int{DimIntent: 2}, HeldOut: []string{DimAction}}
	b := Signature{History: map[string]int{DimRisk: 1}, HeldOut: []string{DimStakeholder}}
	if s, _ := Similarity(a, b); s != 0 {
		t.Errorf("disjoint signatures: %v", s)
	}
	x := Signature{History: map[string]int{DimIntent: 3, DimRisk: 1}, HeldOut: []string{DimAction, DimRisk}}
	y := Signature{History: map[string]int{DimIntent: 1}, HeldOut: []string{DimRisk}}
	s1, _ := Similarity(x, y)
	s2, _ := Similarity(y, x)
	if s1 != s2 || s1 <= 0 || s1 >= 1 {
		t.Errorf("symmetry: %v vs %v", s1, s2)
	}
}

// Any ranked case can be frozen, not only the ten the report lists in full.
func TestApplySelectionMatchesAnyRankedCase(t *testing.T) {
	c := loadConfig(t)
	var seqs []Sequence
	for i := 0; i < TopCases+3; i++ {
		// Later opportunities get one more history event, so their score is lower and they rank below the top ten.
		moves := [][]string{dims(DimIntent), dims(DimRisk)}
		for j := 0; j < i; j++ {
			moves = append(moves, dims())
		}
		moves = append(moves, dims(DimAction))
		seqs = append(seqs, seqOf(fmt.Sprintf("O%02d", i), moves...))
	}
	cases := c.Rank(seqs)
	rep := Report{Scoring: ScoringInfo{Version: c.Version}, Counts: Counts{AccountsScanned: 5}}
	rep.Top, rep.Ranking = cases[:TopCases], briefs(cases)
	deep := cases[len(cases)-1]
	if deep.Rank <= TopCases {
		t.Fatalf("setup: the case must rank below the top ten, got rank %d", deep.Rank)
	}
	m := Manifest{HeldOutEvent: HeldOutEvent{EventID: deep.HeldOut.EventID}}
	for _, e := range deep.History {
		m.Events = append(m.Events, ManifestEvent{Event: HeldOutEvent{EventID: e.EventID}, MaterialDimensions: e.Dimensions})
	}
	got, err := applySelection(m, FreezeOptions{OpportunityID: deep.OpportunityID, Report: &rep})
	if err != nil {
		t.Fatalf("a ranked case outside the top ten was refused: %v", err)
	}
	if got.WhySelected != deep.WhySelected || got.Mining == nil || got.Mining.SequenceScore != deep.Score.Total ||
		got.SelectionExpectations == nil || len(got.SelectionExpectations.MaterialDimensions) != 1 {
		t.Fatalf("selection not carried over: %+v", got)
	}
	// The brief is checked against the replay as strictly as a full case.
	stale := m
	stale.Events = append([]ManifestEvent{}, m.Events...)
	stale.Events[0].MaterialDimensions = []string{DimOwnership}
	if _, err := applySelection(stale, FreezeOptions{OpportunityID: deep.OpportunityID, Report: &rep}); err == nil {
		t.Fatal("a stale report entry must be refused")
	}
}
