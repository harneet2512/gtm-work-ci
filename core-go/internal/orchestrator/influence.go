package orchestrator

import "github.com/harneet2512/gtm-work/core-go/internal/knowledgeinfluence"

// influence is the E7 record of one run (contracts/schemas/knowledge_attribution.v1.json, `influence`): retrieved
// and applicable come from the guidance, used from what each candidate cites, and changed from comparing the
// run's generation with the knowledge-withheld one when the run made it. The five E7 evals score it.
type influence struct {
	Counterfactual knowledgeinfluence.Summary     `json:"counterfactual"`
	Candidates     []knowledgeinfluence.Candidate `json:"candidates"`
	Evals          []knowledgeinfluence.Result    `json:"evals"`
}

// influenceOf derives the record. Rankings are the worker's own (before core re-ranks on eval failures and the
// transition policy): they are what knowledge could have moved. claimed is what each final candidate cites.
func influenceOf(gs guidanceSet, gen *generated, set []evaluated) influence {
	trace := knowledgeinfluence.Trace{Retrieved: nonNil(gs.Attribution.Retrieved), Applicable: nonNil(gs.Attribution.Applicable),
		ExceptionBlocked: nonNil(gs.Attribution.ExceptionBlocked), Counterfactual: knowledgeinfluence.Summary{Status: knowledgeinfluence.NotRun}}
	var changes []*knowledgeinfluence.Change
	if cf := gen.Counterfactual; cf != nil {
		switch cf.Status {
		case cfNoKnowledge:
			trace.Counterfactual.Status = knowledgeinfluence.NoKnowledge
		case cfUnavailable:
			trace.Counterfactual.Status, trace.Counterfactual.Reason = knowledgeinfluence.Unavailable, cf.Reason
		case cfGenerated:
			var with, without []knowledgeinfluence.Arm
			for _, c := range gen.Candidates {
				with = append(with, armOf(c))
			}
			for _, c := range cf.Candidates {
				without = append(without, armOf(c))
			}
			changes, trace.Counterfactual = knowledgeinfluence.Compare(with, without)
		}
	}
	changeOf := map[string]*knowledgeinfluence.Change{}
	for i, c := range gen.Candidates {
		if i < len(changes) {
			changeOf[c.CandidateID] = changes[i]
		}
	}
	workerRank := map[string]int{}
	for _, c := range gen.Candidates {
		workerRank[c.CandidateID] = c.Ranking
	}
	for _, e := range set {
		c := e.Final.Candidate
		trace.Candidates = append(trace.Candidates, knowledgeinfluence.Candidate{CandidateID: c.CandidateID, StrategyType: c.StrategyType,
			Ranking: workerRank[c.CandidateID], Claimed: nonNil(c.KnowledgeRefs), Change: changeOf[c.CandidateID]})
	}
	return influence{Counterfactual: trace.Counterfactual, Candidates: trace.Candidates, Evals: knowledgeinfluence.Grade(trace)}
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
