package orchestrator

import (
	"context"
	"slices"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledgeinfluence"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// generation is everything generate hands to publish.
type generation struct {
	Set          []evaluated
	NoAcceptable bool // every candidate is blocked or restricted: Ghost recommends none
	Influence    influence
}

// generate returns the run's three candidates, evaluated and ranked: phases 3 and 4 plus the ranking step.
func (s *Service) generate(ctx context.Context, run runRow, w world, gs guidanceSet, episodeID string) (generation, error) {
	token, err := s.signer.Issue(run.ID, s.clk.Now())
	if err != nil {
		return generation{}, permanent("generate", err)
	}
	status, to, from := w.transitionFields()
	ec := evalContext{run: run, w: w, gs: gs, token: token, status: status, to: to, from: from, rel: w.relationship()}
	gen, err := s.candidates(ctx, ec, episodeID)
	if err != nil {
		return generation{}, err
	}
	done, err := s.loadEvaluated(ctx, run.ID)
	if err != nil {
		return generation{}, err
	}
	finals, err := s.evaluateAll(ctx, ec, gen.Candidates, done)
	if err != nil {
		return generation{}, err
	}
	set, none := rankEvaluated(finals, status)
	return generation{Set: set, NoAcceptable: none, Influence: influenceOf(gs, gen, set)}, nil
}

// candidates returns the run's validated candidates, generating at most once per purpose however often the run is
// resumed: the first answer, one regeneration when the transition policy leaves no allowed candidate, and (when
// configured) the knowledge-withheld arm of E7. Each is stored on the draft step as soon as it is valid.
func (s *Service) candidates(ctx context.Context, ec evalContext, episodeID string) (*generated, error) {
	gen, err := s.loadGenerated(ctx, ec.run.ID)
	if err != nil {
		return nil, err
	}
	if gen == nil {
		if gen, err = s.requestStrategies(ctx, ec, episodeID, ec.gs.Raw, ec.gs.Applied); err != nil {
			return nil, err
		}
		if err := s.saveGenerated(ctx, ec.run.ID, gen); err != nil {
			return nil, err
		}
	}
	if !gen.Regenerated && !hasAllowed(ec.status, gen.Candidates) {
		if gen, err = s.regenerate(ctx, ec, episodeID, gen); err != nil {
			return nil, err
		}
	}
	if s.cfg.KnowledgeCounterfactual && gen.Counterfactual == nil {
		cf, err := s.counterfactual(ctx, ec, episodeID)
		if err != nil {
			return nil, err
		}
		if err := s.saveCounterfactual(ctx, ec.run.ID, cf); err != nil {
			return nil, err
		}
		gen.Counterfactual = cf
	}
	return gen, nil
}

// regenerate asks once more when no candidate passes the transition policy. A permanent failure of the second ask
// keeps the first answer (the set is then marked "no acceptable candidate"); a transient one leaves the run resumable.
func (s *Service) regenerate(ctx context.Context, ec evalContext, episodeID string, first *generated) (*generated, error) {
	s.log.WarnContext(ctx, "no candidate passes the transition policy; regenerating once", "run_id", ec.run.ID, "transition_status", ec.status)
	again, err := s.requestStrategies(ctx, ec, episodeID, ec.gs.Raw, ec.gs.Applied)
	switch {
	case err == nil:
		again.Counterfactual = nil // a counterfactual of the discarded set would not compare with this one
	case IsPermanent(err):
		s.log.WarnContext(ctx, "regeneration failed; keeping the first answer", "run_id", ec.run.ID, "error", err)
		kept := *first
		again = &kept
	default:
		return nil, err
	}
	again.Regenerated = true
	return again, s.saveGenerated(ctx, ec.run.ID, again)
}

// counterfactual generates arm A: the same state with the DecisionGuidance withheld. With nothing retrieved arm A
// would equal the run, so none is made. Unusable output after the retry is recorded, never fatal: it is an analysis.
func (s *Service) counterfactual(ctx context.Context, ec evalContext, episodeID string) (*counterfactual, error) {
	if len(ec.gs.Attribution.Retrieved) == 0 {
		return &counterfactual{Status: cfNoKnowledge}, nil
	}
	arm, err := s.requestStrategies(ctx, ec, episodeID, nil, map[string]bool{})
	switch {
	case err == nil:
		return &counterfactual{Status: cfGenerated, Candidates: arm.Candidates}, nil
	case IsTransient(err):
		return nil, err
	}
	s.log.WarnContext(ctx, "counterfactual generation unusable", "run_id", ec.run.ID, "error", err)
	return &counterfactual{Status: cfUnavailable, Reason: clip(err.Error(), 500)}, nil
}

// rankEvaluated is the core ranking step, after the evals (invariant I7): a candidate with a blocking failure
// ranks below one without, a candidate the transition policy restricted ranks below an allowed one (it can never
// be preferred while an unrestricted alternative exists), and ties keep the worker's order. The final order is
// the one humans see and the one preference learning compares against. The second result is true when no
// candidate is acceptable (every one blocked or restricted): the set is then flagged, Ghost recommends none, and
// the order only arranges the display.
func rankEvaluated(es []evaluated, transitionStatus string) ([]evaluated, bool) {
	out := slices.Clone(es)
	for i := range out {
		out[i].Policy = CandidatePolicy(transitionStatus, out[i].Final.Candidate)
	}
	tier := func(e evaluated) int {
		switch {
		case e.Final.Blocking:
			return 2
		case e.Policy.Restricted():
			return 1
		}
		return 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ti, tj := tier(out[i]), tier(out[j]); ti != tj {
			return ti < tj
		}
		return out[i].Final.Candidate.Ranking < out[j].Final.Candidate.Ranking
	})
	none := len(out) == 0 || tier(out[0]) != 0
	for i := range out {
		c := out[i].Final.Candidate
		c.Ranking, c.PreferredByAgent = i+1, i == 0
		out[i].Final.Candidate = c
	}
	return out, none
}

// usedKnowledge is the E7 "used" column: knowledge the final candidates cite.
func usedKnowledge(es []evaluated) []string {
	used := []string{}
	for _, e := range es {
		for _, k := range e.Final.Candidate.KnowledgeRefs {
			used = appendUnique(used, k)
		}
	}
	slices.Sort(used)
	return used
}

// armOf is the comparison view of a worker candidate.
func armOf(c workerclient.Candidate) knowledgeinfluence.Arm {
	return knowledgeinfluence.Arm{StrategyType: c.StrategyType, Ranking: c.Ranking, ActionClass: c.ActionClass,
		ActionType: c.ActionType, People: distinctPeople(c)}
}
