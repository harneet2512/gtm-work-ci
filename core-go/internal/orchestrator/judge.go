package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// judgedItem is one bundle item the worker returned with its decoded EvalResult (nil for not_relevant).
type judgedItem struct {
	Item   workerclient.BundleItem
	Result *deterministic.EvalResult
}

type judged struct {
	Items []judgedItem
	Model string
}

// judge asks the worker for the semantic evals of one draft and checks the answer. A malformed answer (a verdict
// with no usable result, a result about another run or draft, an item that contradicts its own result) is retried
// once, like unusable generation output; a second one fails the run permanently, so the account is not held open
// behind a worker that cannot produce a valid judgment. Transient worker failures are not retried here.
func (s *Service) judge(ctx context.Context, ec evalContext, c workerclient.Candidate, index int, suite string, excluded []string) (judged, error) {
	var last error
	for attempt := 1; attempt <= maxGenerationAttempts; attempt++ {
		resp, err := s.worker.Judge(ctx, s.judgeRequest(ec, c, index, suite, excluded))
		if err != nil {
			f := workerFailure("judge", err)
			if !isInvalidOutput(f) {
				return judged{}, f
			}
			last = f
			s.log.WarnContext(ctx, "invalid judge answer", "run_id", ec.run.ID, "draft_index", index, "attempt", attempt, "error", f)
			continue
		}
		out := judged{Model: resp.Model}
		last = nil
		for _, it := range resp.Items {
			ji := judgedItem{Item: it}
			if it.Verdict != "not_relevant" {
				res, err := s.checkJudged(ec, index, it)
				if err != nil {
					last = err
					break
				}
				ji.Result = &res
			}
			out.Items = append(out.Items, ji)
		}
		if last == nil {
			return out, nil
		}
		s.log.WarnContext(ctx, "malformed judge answer", "run_id", ec.run.ID, "draft_index", index, "attempt", attempt, "error", last)
	}
	return judged{}, permanent("judge", fmt.Errorf("invalid judge output after %d attempts: %w", maxGenerationAttempts, last))
}

func (s *Service) judgeRequest(ec evalContext, c workerclient.Candidate, index int, suite string, excluded []string) workerclient.JudgeRequest {
	req := workerclient.JudgeRequest{RunID: ec.run.ID, AccountID: ec.run.AccountID, DraftIndex: index, Candidate: c,
		RunToken: ec.token, TriggerContext: &ec.w.Trigger}
	if suite != "" {
		req.EvalSuite = &workerclient.EvalSuite{Name: suite, ExcludedEvalTypes: excluded}
	}
	for _, k := range ec.gs.Knowledge {
		req.OfferedKnowledge = append(req.OfferedKnowledge, mustJSON(k))
	}
	return req
}

// checkJudged decodes a worker EvalResult and refuses one that is about another run or draft or contradicts its
// own bundle item: worker output is untrusted until it is checked.
func (s *Service) checkJudged(ec evalContext, index int, it workerclient.BundleItem) (deterministic.EvalResult, error) {
	var r deterministic.EvalResult
	if err := json.Unmarshal(it.Result, &r); err != nil || len(it.Result) == 0 || string(it.Result) == "null" {
		return r, &invalidOutput{Violations: []string{fmt.Sprintf("judge item %s has no usable result", it.EvalType)}}
	}
	switch {
	case r.AgentRunID != ec.run.ID || r.DraftIndex != index:
		return r, &invalidOutput{Violations: []string{fmt.Sprintf("judge result %s is about another run or draft", it.EvalType)}}
	case r.Verdict != it.Verdict || string(r.EvalType) != it.EvalType:
		return r, &invalidOutput{Violations: []string{fmt.Sprintf("judge item %s disagrees with its result", it.EvalType)}}
	case r.Blocking && r.Verdict != "fail":
		return r, &invalidOutput{Violations: []string{fmt.Sprintf("judge result %s blocks without failing", it.EvalType)}}
	}
	return r, nil
}
