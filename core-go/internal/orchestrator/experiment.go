package orchestrator

import (
	"context"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Two seams for experiments that must be reproducible and must score every arm alike (HAR-128 A/B/C uplift). Neither
// changes what a production run does.

// newID is the id of a guidance, strategy set, draft-step episode or eval bundle: Config.NewID when set (a fixed,
// deterministic sequence so a replayed run builds byte-identical prompts), a random v4 uuid otherwise.
func (s *Service) newID() string {
	if s.cfg.NewID != nil {
		return s.cfg.NewID()
	}
	return newID()
}

// BlockingFinding is one deterministic eval that failed with a blocking result on a candidate.
type BlockingFinding struct {
	EvalType string `json:"eval_type"`
	Reason   string `json:"reason"`
}

// DeterministicBlocking runs the in-core deterministic evals (ADR-0014) on one candidate as the run's first draft and
// returns the blocking failures. It reads the run's world at its replay clock and calls no model and writes nothing,
// so any arm's candidate, including the knowledge-withheld counterfactual's (which a run never evaluates), is scored
// by the same evals. Each blocking failure is a correction a human reviewer would have to make: the correction
// proxy of the A/B/C report.
func (s *Service) DeterministicBlocking(ctx context.Context, runID string, c workerclient.Candidate) ([]BlockingFinding, error) {
	run, err := s.loadRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	w, err := s.readWorld(ctx, run)
	if err != nil {
		return nil, err
	}
	in, err := s.evalInput(ctx, run, w, c, 1)
	if err != nil {
		return nil, err
	}
	out := []BlockingFinding{}
	for _, j := range deterministic.Evaluate(in) {
		if j.Result.Blocking {
			out = append(out, BlockingFinding{EvalType: string(j.Result.EvalType), Reason: clip(j.Result.Reason, 300)})
		}
	}
	return out, nil
}
