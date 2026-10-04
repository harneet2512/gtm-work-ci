package strategystore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// sendRun is the part of the agent_runs row a send needs; checkSendable row-locks it.
type sendRun struct {
	Mode, Status  string
	OpportunityID string
	TriggerIDs    []string
}

// sendEval is the send-time deterministic re-evaluation of the final artifact (HAR-139).
type sendEval struct {
	Results []deterministic.EvalResult // one result per deterministic eval, content-hashed ids
}

// evalFinal re-evaluates the FINAL artifact — the selected candidate overlaid with the human's saved
// edits — through the same deterministic engine the candidates were judged by, at the run's replay clock.
// A human edit is therefore checked by the evals, and an edit that repairs a blocking failure passes.
func (s *Service) evalFinal(ctx context.Context, tx *sql.Tx, ep episode, run sendRun, runID string, chosen candidate, final Draft) (sendEval, error) {
	at, err := evalinput.ReplayClock(ctx, tx, run.TriggerIDs)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: replay clock of run %s: %w", runID, err)
	}
	state, found, err := evalinput.State(ctx, tx, ep.AccountID, at)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: state of account %s: %w", ep.AccountID, err)
	}
	if !found {
		return sendEval{}, fmt.Errorf("strategystore: account %s has no state at the replay clock", ep.AccountID)
	}
	in, err := evalinput.Assemble(ctx, tx,
		evalinput.Run{ID: runID, AccountID: ep.AccountID, Mode: run.Mode, OpportunityID: run.OpportunityID, TriggerIDs: run.TriggerIDs},
		finalOutput(chosen, final), chosen.DraftIndex, at, state, s.params)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: eval input of run %s: %w", runID, err)
	}
	return sendEval{Results: deterministic.Results(deterministic.Evaluate(in))}, nil
}

// finalOutput is the deterministic draft of what the human is about to send: the final artifact and its
// recipients, with the candidate's evidence, intent and knowledge carried over (the human changes neither).
// It shares the generation-time skeleton (evalinput.Output), so the two judgments see the same shape.
func finalOutput(chosen candidate, final Draft) deterministic.Output {
	p := evalinput.OutputParts{
		ActionType: chosen.ActionType, Intent: chosen.Description, Reason: chosen.Rationale,
		Knowledge: chosen.KnowledgeRefs,
		Artifact: evalinput.Artifact{Channel: final.Artifact.Channel, Subject: final.Artifact.Subject,
			Body: final.Artifact.Body, Attachments: final.Artifact.Attachments},
		Evidence: chosen.EvidenceRefs,
	}
	for _, r := range append(append([]Recipient{}, final.To...), final.CC...) {
		p.Recipients = append(p.Recipients, evalinput.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why})
	}
	return evalinput.Output(p)
}

// blockingFailures are the send-time results that refuse the send.
func blockingFailures(results []deterministic.EvalResult) []deterministic.EvalResult {
	var out []deterministic.EvalResult
	for _, r := range results {
		if r.Verdict == "fail" && r.Blocking {
			out = append(out, r)
		}
	}
	return out
}

// saveFinalResults persists the send-time results inside the send transaction (content-hashed ids make a
// retried write a no-op).
func saveFinalResults(ctx context.Context, tx *sql.Tx, results []deterministic.EvalResult) error {
	if _, err := evalstore.Save(ctx, tx, results); err != nil {
		return fmt.Errorf("strategystore: persist send-time evals: %w", err)
	}
	return nil
}

// persistRefused keeps the send-time results of a refused send in a separate transaction: the refusal is
// auditable even though the decision stayed pending. A failure here is logged, never returned.
func (s *Service) persistRefused(ctx context.Context, runID string, results []deterministic.EvalResult) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.log.WarnContext(ctx, "could not start the refused-send eval write", "run_id", runID, "error", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveFinalResults(ctx, tx, results); err != nil {
		s.log.WarnContext(ctx, "could not persist the refused send's evals", "run_id", runID, "error", err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.log.WarnContext(ctx, "could not commit the refused send's evals", "run_id", runID, "error", err)
	}
}

// repairedEvals are the candidate-version eval results (same run, same draft index) whose verdict was fail
// and whose eval type now passes on the final artifact: they are what the HumanDelta is explained by.
func repairedEvals(ctx context.Context, tx *sql.Tx, runID string, draftIndex int, results []deterministic.EvalResult) ([]workerclient.ExplainingEval, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text, evaluator, COALESCE(rationale, '') FROM eval_runs
 WHERE agent_run_id = $1::uuid AND draft_index = $2 AND verdict = 'fail' ORDER BY evaluator, id`, runID, draftIndex)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read failing evals of run %s: %w", runID, err)
	}
	defer rows.Close()
	type priorFail struct{ id, evalType, reason string }
	var priors []priorFail
	for rows.Next() {
		var f priorFail
		if err := rows.Scan(&f.id, &f.evalType, &f.reason); err != nil {
			return nil, fmt.Errorf("strategystore: scan failing eval: %w", err)
		}
		priors = append(priors, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pass := map[string]bool{}
	for _, r := range results {
		if r.Verdict == "pass" {
			pass[string(r.EvalType)] = true
		}
	}
	var out []workerclient.ExplainingEval
	for _, p := range priors {
		if pass[p.evalType] {
			out = append(out, workerclient.ExplainingEval{EvalResultID: p.id, EvalType: p.evalType, Reason: p.reason})
		}
	}
	return out, nil
}

// blockingReason is the 422 message: which evals still fail blocking on the final artifact.
func blockingReason(blocked []deterministic.EvalResult) string {
	names := make([]string, len(blocked))
	for i, r := range blocked {
		names[i] = string(r.EvalType)
	}
	return fmt.Sprintf("the final artifact still fails a blocking eval (%s); edit the draft or discard",
		strings.Join(names, ", "))
}
