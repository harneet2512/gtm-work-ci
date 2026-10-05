package demomine

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

const configPath = "../../../bench/config/demo_case_scoring.v1.json"

func loadConfig(t *testing.T) Config {
	t.Helper()
	c, err := LoadConfig(filepath.FromSlash(configPath))
	if err != nil {
		t.Fatalf("load scoring config: %v", err)
	}
	return c
}

func rec(pos int, day int, dims ...string) EventRecord {
	at := time.Date(2023, 1, day, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	return EventRecord{Position: pos, OccurredAt: at, SourceEventKey: fmt.Sprintf("k%d", pos), Layer: LayerBase,
		Dimensions: dims, Evidence: []string{}}
}

func withKey(r EventRecord, key string) EventRecord {
	r.SourceEventKey = key
	return r
}

func TestCommittedConfigIsCompleteAndRationalised(t *testing.T) {
	c := loadConfig(t)
	if c.SHA256 == "" || len(c.SHA256) != 64 {
		t.Fatalf("config digest missing: %q", c.SHA256)
	}
	// Equal weights: the config must not presume a dimension is more demo-worthy (HAR-129: no story first).
	for _, d := range Dimensions {
		if c.DimensionWeights[d] != 1.0 {
			t.Errorf("dimension %s weight %v, want 1.0 (untuned)", d, c.DimensionWeights[d])
		}
	}
}

func TestConfigRejectsAWeightWithoutRationale(t *testing.T) {
	c := loadConfig(t)
	delete(c.Rationale, "held_out.decision_change")
	if err := c.Validate(); err == nil {
		t.Fatal("a config with an unexplained weight must be rejected")
	}
}

func TestScoreRewardsDistinctDimensionsAndADecisionChangingHeldOutEvent(t *testing.T) {
	c := loadConfig(t)
	hist := []EventRecord{rec(1, 1, DimStakeholder), rec(2, 2, DimIntent), rec(3, 3, DimRisk, DimStakeholder)}
	held := rec(4, 4, DimNextStep, DimAction)
	s, ok, why := c.ScoreSequence(hist, held, true)
	if !ok {
		t.Fatalf("not a candidate: %s", why)
	}
	// history: 3 distinct x1.0 + 3 material x0.25 + 1 revisited (stakeholder) x0.5
	// held-out: 1.0 + 2 dims x1.0 + 2 novel x1.0 + decision 3.0
	want := 3.0 + 0.75 + 0.5 + 1.0 + 2.0 + 2.0 + 3.0
	if s.Total != want {
		t.Fatalf("total %v, want %v: %+v", s.Total, want, s)
	}
	if !s.DecisionChange || len(s.NovelDimensions) != 2 || len(s.Revisited) != 1 {
		t.Fatalf("components wrong: %+v", s)
	}
}

func TestScoreRequirements(t *testing.T) {
	c := loadConfig(t)
	two := []EventRecord{rec(1, 1, DimStakeholder), rec(2, 2, DimIntent)}
	for name, tc := range map[string]struct {
		hist  []EventRecord
		held  EventRecord
		later bool
	}{
		"history too short":               {two[:1], rec(3, 3, DimRisk), true},
		"one distinct dimension":          {[]EventRecord{rec(1, 1, DimIntent), rec(2, 2, DimIntent)}, rec(3, 3, DimRisk), true},
		"held-out changes nothing":        {two, rec(3, 3), true},
		"held-out not strictly after N-1": {two, rec(3, 3, DimRisk), false},
		"held-out is a final-stage value": {two, withKey(rec(3, 3, DimRisk), "field:StageName:Closed"), true},
	} {
		if _, ok, _ := c.ScoreSequence(tc.hist, tc.held, tc.later); ok {
			t.Errorf("%s: want rejected", name)
		}
	}
}

func TestBestCasePrefersTheRicherSplitAndKeepsChronology(t *testing.T) {
	c := loadConfig(t)
	seq := Sequence{OpportunityID: "006A", Events: []EventRecord{
		rec(1, 1, DimStakeholder), rec(2, 2, DimIntent), rec(3, 3), rec(4, 4, DimRisk, DimAction), rec(5, 5, DimStakeholder),
	}}
	cs, ok := c.BestCase(seq)
	if !ok {
		t.Fatal("no case")
	}
	if cs.HeldOut.Position != 4 || len(cs.History) != 3 {
		t.Fatalf("held-out position %d with %d history events, want 4 and 3", cs.HeldOut.Position, len(cs.History))
	}
	for i, e := range cs.History {
		if e.Position != i+1 {
			t.Fatalf("history reordered: position %d at index %d", e.Position, i)
		}
	}
	if cs.WhySelected == "" || cs.Provenance[LayerBase] != 4 {
		t.Fatalf("why/provenance: %q %v", cs.WhySelected, cs.Provenance)
	}
}

func TestRankIsDeterministicAndBreaksTiesByLengthThenID(t *testing.T) {
	c := loadConfig(t)
	mk := func(id string, extra bool) Sequence {
		ev := []EventRecord{rec(1, 1, DimStakeholder), rec(2, 2, DimIntent), rec(3, 3, DimRisk)}
		if extra {
			ev = append([]EventRecord{rec(0, 1)}, ev...)
			for i := range ev {
				ev[i].Position = i + 1
			}
		}
		return Sequence{OpportunityID: id, Events: ev}
	}
	got := c.Rank([]Sequence{mk("006B", false), mk("006A", false), mk("006C", true)})
	ids := []string{got[0].OpportunityID, got[1].OpportunityID, got[2].OpportunityID}
	if fmt.Sprint(ids) != "[006A 006B 006C]" {
		t.Fatalf("order %v", ids)
	}
	if got[0].Rank != 1 || got[2].Rank != 3 {
		t.Fatalf("ranks %d %d", got[0].Rank, got[2].Rank)
	}
}

func TestClassify(t *testing.T) {
	dims, ev := Classify(Observation{ChangedFields: []string{"stage", "summary", "blockers"}, Signals: []string{"customer_replied", "pricing_interest"},
		TriggerEligible: true, TriggerReasons: []string{"eligible_blocker_change"}})
	if fmt.Sprint(dims) != "[buyer_intent blockers_risk recommended_action]" {
		t.Fatalf("dims %v (evidence %v)", dims, ev)
	}
	// The deal's first state earns nothing from field changes.
	dims, _ = Classify(Observation{DealCreated: true, ChangedFields: []string{"stage", "owner"}})
	if len(dims) != 0 {
		t.Fatalf("a created deal must earn no dimension, got %v", dims)
	}
	// An eligible trigger is a decision point on its own: the agent would run on this event.
	dims, _ = Classify(Observation{TriggerEligible: true, TriggerReasons: []string{"eligible_customer_replied"}})
	if fmt.Sprint(dims) != "[recommended_action]" {
		t.Fatalf("dims %v", dims)
	}
}
