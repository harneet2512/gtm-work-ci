// Package knowledgeinfluence is HAR-97's E7, knowledge influence attribution (HAR-128): retrieval is not influence.
// For one decision episode it keeps four things apart: knowledge retrieved, judged applicable, used (cited by a
// candidate) and, when the run also generated the same state with knowledge withheld (arm A, the counterfactual),
// whether using it changed a candidate, the ranking or the preferred action. Compare derives the per-candidate
// `changed` attribution from the two generations; Grade scores the trace with the five deterministic E7 evals.
package knowledgeinfluence

import (
	"slices"
	"sort"
)

// Counterfactual statuses.
const (
	NotRun      = "not_run"      // the run did not generate arm A (flag off)
	Compared    = "compared"     // arm A exists and was compared with the run's generation
	Unavailable = "unavailable"  // arm A was attempted and produced no usable answer
	NoKnowledge = "no_knowledge" // nothing was retrieved, so arm A would equal the run: nothing to compare
)

// Verdicts of an E7 eval.
const (
	Pass    = "pass"
	Warn    = "warn"
	Fail    = "fail"
	Unknown = "unknown"
)

// Arm is the part of one generated candidate the comparison reads.
type Arm struct {
	StrategyType string   `json:"strategy_type"`
	Ranking      int      `json:"ranking"`
	ActionClass  string   `json:"action_class"`
	ActionType   string   `json:"action_type"`
	People       []string `json:"people"` // distinct recipients (to and cc), any order
}

// Change is what knowledge changed about one candidate, measured against the knowledge-withheld generation.
type Change struct {
	// Matched: arm A produced a candidate of the same strategy_type. An unmatched candidate exists only because of
	// the knowledge (or sampling noise: arm A is one sample).
	Matched           bool `json:"matched"`
	RankingChanged    bool `json:"ranking_changed"`
	ActionChanged     bool `json:"action_changed"`
	RecipientsChanged bool `json:"recipients_changed"`
	Changed           bool `json:"changed"`
}

// Candidate is one candidate of the trace.
type Candidate struct {
	CandidateID  string   `json:"candidate_id"`
	StrategyType string   `json:"strategy_type"`
	Ranking      int      `json:"ranking"` // the worker's ranking, before core's eval-driven re-ranking
	Claimed      []string `json:"claimed"` // knowledge ids the candidate cites as used
	Change       *Change  `json:"change"`  // nil unless the counterfactual was compared
}

// Summary is the set-level comparison.
type Summary struct {
	Status           string `json:"status"`
	Reason           string `json:"reason,omitempty"`
	RankingChanged   bool   `json:"ranking_changed"`
	PreferredChanged bool   `json:"preferred_action_changed"`
}

// Trace is the E7 record of one run: retrieved -> applicable -> used -> changed.
type Trace struct {
	Retrieved        []string    `json:"retrieved"`
	Applicable       []string    `json:"applicable"`
	ExceptionBlocked []string    `json:"exception_blocked"`
	Counterfactual   Summary     `json:"counterfactual"`
	Candidates       []Candidate `json:"candidates"`
}

// Result is the verdict of one E7 eval on a trace.
type Result struct {
	Eval    string `json:"eval"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Compare measures, for each candidate of the run (arm B, knowledge available) against arm A (knowledge withheld),
// whether its ranking, action or recipients differ, and whether the preferred action moved. a is nil or empty when
// there is no usable arm A: every Change is then nil and the summary is NotRun.
func Compare(b, a []Arm) ([]*Change, Summary) {
	out := make([]*Change, len(b))
	if len(a) == 0 || len(b) == 0 {
		return out, Summary{Status: NotRun}
	}
	byType := map[string]Arm{}
	for _, x := range a {
		byType[x.StrategyType] = x
	}
	sum := Summary{Status: Compared}
	for i, x := range b {
		prev, ok := byType[x.StrategyType]
		c := &Change{Matched: ok}
		if ok {
			c.RankingChanged = prev.Ranking != x.Ranking
			c.ActionChanged = prev.ActionClass != x.ActionClass || prev.ActionType != x.ActionType
			c.RecipientsChanged = !sameSet(prev.People, x.People)
		}
		c.Changed = !ok || c.RankingChanged || c.ActionChanged || c.RecipientsChanged
		sum.RankingChanged = sum.RankingChanged || c.RankingChanged
		out[i] = c
	}
	pb, pa := first(b), first(a)
	sum.PreferredChanged = pb.StrategyType != pa.StrategyType || pb.ActionClass != pa.ActionClass || pb.ActionType != pa.ActionType
	return out, sum
}

// first is the candidate the model ranked first (smallest ranking).
func first(arms []Arm) Arm {
	best := arms[0]
	for _, x := range arms[1:] {
		if x.Ranking < best.Ranking {
			best = x
		}
	}
	return best
}

func sameSet(x, y []string) bool {
	x, y = slices.Clone(x), slices.Clone(y)
	sort.Strings(x)
	sort.Strings(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}
