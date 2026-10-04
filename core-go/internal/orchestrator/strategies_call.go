package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// The calls to the worker's /v1/strategies and the classification of its failures live apart from the knowledge
// code: this file names the worker's request fields, including its operational budget, which no knowledge path
// may read (HAR-140).

// invalidCodes are worker error codes that mean "the model produced unusable output": retried once, then fatal.
var invalidCodes = map[string]bool{
	"invalid_strategies": true, "ungrounded_proposal": true, "inconsistent_draft": true, "tool_budget_exceeded": true,
}

func isInvalidOutput(err error) bool {
	var inv *invalidOutput
	return errors.As(err, &inv)
}

// workerFailure classifies a worker or breaker error. Unusable model output becomes an *invalidOutput (the caller
// retries once); a request the worker rejects as invalid is permanent; everything else is transient: a worker
// 5xx, a 429, an unreachable worker, a provider refusal, an open breaker and an expired run token (the worker's
// 502 core_unavailable) all leave the run resumable and burn no further paid calls.
func workerFailure(phase string, err error) error {
	var we *workerclient.Error
	switch {
	case errors.Is(err, context.Canceled):
		return transient(phase, err)
	case errors.As(err, &we) && invalidCodes[we.Code]:
		return &invalidOutput{Violations: []string{fmt.Sprintf("worker %s: %s", we.Code, we.Message)}}
	case claims.IsPermanent(err):
		return permanent(phase, err)
	}
	return transient(phase, err)
}

// requestStrategies asks the worker for the candidates and validates them. Unusable output is retried once with the
// model's own nondeterminism; the second failure is permanent. guidance nil withholds the DecisionGuidance; applied
// is the knowledge a candidate may then cite.
func (s *Service) requestStrategies(ctx context.Context, ec evalContext, episodeID string, guidance json.RawMessage, applied map[string]bool) (*generated, error) {
	run, w := ec.run, ec.w
	req := workerclient.StrategiesRequest{
		RunID: run.ID, AccountID: run.AccountID, DecisionEpisodeID: episodeID, Workflow: "post_interaction_followup",
		TriggerContext: w.Trigger, StateHeader: w.header(), RunToken: ec.token, CandidateCount: candidateCount,
		MaxToolCalls: s.cfg.MaxContextPulls, DecisionGuidance: guidance,
	}
	if w.AccountChangeID != "" {
		req.AccountChangeID = &w.AccountChangeID
	}
	if w.Transition != nil {
		req.StateTransition = mustJSON(w.Transition)
	}
	var last error
	for attempt := 1; attempt <= maxGenerationAttempts; attempt++ {
		resp, err := s.worker.Strategies(ctx, req)
		if err == nil {
			err = s.validate(ctx, run.AccountID, w.EventTime, resp.Candidates, applied)
		} else {
			err = workerFailure("generate", err)
		}
		switch {
		case err == nil:
			slices.SortStableFunc(resp.Candidates, func(a, b workerclient.Candidate) int { return a.Ranking - b.Ranking })
			return &generated{Candidates: resp.Candidates, Model: resp.Model}, nil
		case isInvalidOutput(err):
			last = err
			s.log.WarnContext(ctx, "invalid strategies", "run_id", run.ID, "attempt", attempt, "error", err)
		case IsTransient(err) || IsPermanent(err):
			return nil, err
		default: // a database error while validating
			return nil, transient("generate", err)
		}
	}
	return nil, permanent("generate", fmt.Errorf("invalid output after %d attempts: %w", maxGenerationAttempts, last))
}
