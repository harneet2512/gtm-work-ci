package strategystore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// sendRun is the part of the agent_runs row a send needs; checkSendable row-locks it.
type sendRun struct {
	Mode, Status  string
	OpportunityID string
	TriggerIDs    []string
}

// sendEval is the send-time deterministic re-evaluation of the final artifact (HAR-139), plus the
// account's shadow axis results (HAR-119) and the replay clock they were judged at.
type sendEval struct {
	Results []deterministic.EvalResult // one result per eval incl. shadow axes, content-hashed ids
	At      time.Time                  // the run's replay clock
	// StateVersion and StateHash identify the account state the evaluation actually read (the version and the
	// sha-256 of its document); the send stores them so "state preserved" is a stored comparison.
	StateVersion int
	StateHash    string
}

// evalFinal re-evaluates the FINAL artifact — the selected candidate overlaid with the human's saved
// edits — through the same deterministic engine the candidates were judged by, at the run's replay
// clock, at the evaluators' active persisted versions and alongside the account's shadow axis checks
// (HAR-119: learned candidate criteria run but never block). A human edit is therefore checked by the
// evals, and an edit that repairs a failure passes.
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
	hash, err := evalinput.StateDigest(state)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: digest the state of account %s: %w", ep.AccountID, err)
	}
	in, err := evalinput.Assemble(ctx, tx,
		evalinput.Run{ID: runID, AccountID: ep.AccountID, Mode: run.Mode, OpportunityID: run.OpportunityID, TriggerIDs: run.TriggerIDs},
		finalOutput(chosen, final), chosen.DraftIndex, at, state, s.params)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: eval input of run %s: %w", runID, err)
	}
	in.Versions, err = learning.ActiveVersions(ctx, tx, ep.AccountID)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: active evaluator versions: %w", err)
	}
	results := deterministic.Results(deterministic.Evaluate(in))
	axes, err := learning.LoadAxes(ctx, tx, ep.AccountID)
	if err != nil {
		return sendEval{}, fmt.Errorf("strategystore: shadow axes of account %s: %w", ep.AccountID, err)
	}
	results = append(results, learning.ShadowResults(in, axes)...)
	return sendEval{Results: results, At: at, StateVersion: state.Version, StateHash: hash}, nil
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

// repaired is one prior failure the send repaired: its result id, eval axis, version, kind, the
// failure's rationale and its suggested correction (what the generator_feedback instruction is
// built from).
type repaired struct {
	id, evalType, version, kind, reason, correction string
}

// repairedEvals are the prior eval results (same run, same draft index) whose verdict was fail and
// whose exact evaluator+version+kind now passes on the final artifact: they are what the HumanDelta
// is explained by. Priors predate the human's edit — the send-time batch just persisted inside this
// transaction post-dates it, so it is excluded by id. The match key is (evaluator, version, kind),
// not the axis name alone: a learned axis can share a canonical eval name, and once that version is
// active both families carry the same '<axis>:v<N>' tag — only kind ('human_delta' vs 'deterministic')
// still separates them.
func repairedEvals(ctx context.Context, tx *sql.Tx, runID string, draftIndex int, results []deterministic.EvalResult) ([]repaired, error) {
	sentIDs := make([]string, 0, len(results))
	for _, r := range results {
		sentIDs = append(sentIDs, r.ID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id::text, evaluator, evaluator_version, kind, COALESCE(rationale, ''), COALESCE(suggested_correction, '')
 FROM eval_runs
 WHERE agent_run_id = $1::uuid AND draft_index = $2 AND verdict = 'fail'
   AND NOT (id = ANY($3::uuid[]))
 ORDER BY evaluator, id`, runID, draftIndex, signalstore.UUIDArray(sentIDs))
	if err != nil {
		return nil, fmt.Errorf("strategystore: read failing evals of run %s: %w", runID, err)
	}
	defer rows.Close()
	var priors []repaired
	for rows.Next() {
		var f repaired
		if err := rows.Scan(&f.id, &f.evalType, &f.version, &f.kind, &f.reason, &f.correction); err != nil {
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
			pass[string(r.EvalType)+"|"+r.EvalVersion+"|"+r.Kind] = true
		}
	}
	var out []repaired
	for _, p := range priors {
		if pass[p.evalType+"|"+p.version+"|"+p.kind] {
			out = append(out, p)
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
