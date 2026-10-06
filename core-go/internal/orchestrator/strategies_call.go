package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
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
		return transient(phase, stageevents.MarkTransport(err))
	case errors.As(err, &we) && invalidCodes[we.Code]:
		return &invalidOutput{Violations: []string{fmt.Sprintf("worker %s: %s", we.Code, we.Message)}}
	case claims.IsPermanent(err):
		return permanent(phase, err)
	}
	return transient(phase, stageevents.MarkTransport(err)) // the worker or the provider, not Ghost, could not answer
}

// requestStrategies asks the worker for the candidates and validates them. Unusable output is retried once with the
// model's own nondeterminism; the second failure is permanent. guidance nil withholds the DecisionGuidance; applied
// is the knowledge a candidate may then cite. The account's unconsumed generator_feedback (explained human
// corrections, HAR-119) rides the request; a row is offered to exactly one run.
func (s *Service) requestStrategies(ctx context.Context, ec evalContext, episodeID string, guidance json.RawMessage, applied map[string]bool) (*generated, error) {
	run, w := ec.run, ec.w
	feedback, fbIDs, err := s.pendingFeedback(ctx, run.AccountID, run.ID)
	if err != nil {
		return nil, transient("generate", err)
	}
	req := workerclient.StrategiesRequest{
		RunID: run.ID, AccountID: run.AccountID, DecisionEpisodeID: episodeID, Workflow: "post_interaction_followup",
		TriggerContext: w.Trigger, StateHeader: w.header(), RunToken: ec.token, CandidateCount: candidateCount,
		MaxToolCalls: s.cfg.MaxContextPulls, DecisionGuidance: guidance, GeneratorFeedback: feedback,
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
			// The run received the corrections; mark them consumed so no later run is offered them again.
			// A mark lost to a crash only re-offers the row, never applies it twice in one request.
			if err := s.consumeFeedback(ctx, run.ID, fbIDs); err != nil {
				return nil, transient("generate", err)
			}
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

// pendingFeedback is the account's explained corrections this run may receive: rows no run consumed,
// plus the ones this run already consumed (a regeneration asks again and must see the same corrections).
// The cap is the contract's maxItems.
func (s *Service) pendingFeedback(ctx context.Context, accountID, runID string) ([]workerclient.GeneratorFeedback, []string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, eval_run_id::text, eval_type::text, instruction
 FROM generator_feedback
 WHERE account_id = $1::uuid AND withdrawn_at IS NULL
   AND (consumed_by_run_id IS NULL OR consumed_by_run_id = $2::uuid)
 ORDER BY created_at, id LIMIT 20`, accountID, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("read generator feedback of account %s: %w", accountID, err)
	}
	defer rows.Close()
	var out []workerclient.GeneratorFeedback
	var ids []string
	for rows.Next() {
		var id string
		var f workerclient.GeneratorFeedback
		if err := rows.Scan(&id, &f.EvalResultID, &f.EvalType, &f.Instruction); err != nil {
			return nil, nil, fmt.Errorf("scan generator feedback: %w", err)
		}
		out, ids = append(out, f), append(ids, id)
	}
	return out, ids, rows.Err()
}

// consumeFeedback marks the rows this run received. Only rows still unconsumed move; a row another run
// raced to consume stays with it (the UNIQUE guarantee is the consumed flag, not the attempt).
func (s *Service) consumeFeedback(ctx context.Context, runID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE generator_feedback SET consumed_by_run_id = $1::uuid
 WHERE id = ANY($2::uuid[]) AND consumed_by_run_id IS NULL`, runID, signalstore.UUIDArray(ids)); err != nil {
		return fmt.Errorf("consume generator feedback of run %s: %w", runID, err)
	}
	return nil
}
