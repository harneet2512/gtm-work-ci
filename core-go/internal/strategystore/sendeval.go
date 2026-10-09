package strategystore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
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
	// The final artifact is the chosen candidate's action as the human will send it: the recomputed action span.
	results = deterministic.Stamped(results, deterministic.JudgedObject{Type: "StrategyCandidate", ID: chosen.ID},
		deterministic.SpanID(deterministic.SpanRecomputedAction, runID))
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
	if _, err := evalstore.SaveSend(ctx, tx, results); err != nil {
		return fmt.Errorf("strategystore: persist send-time evals: %w", err)
	}
	return nil
}

// persistRefused keeps the send-time results of a refused send in a separate transaction: the refusal is
// auditable even though the decision stayed pending. A failure here is logged, never returned.
func (s *Service) persistRefused(ctx context.Context, episodeID, runID string, results []deterministic.EvalResult, d8 []bucket2.Result) {
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
	if err := bucket2.SaveTx(ctx, tx, episodeID, d8); err != nil {
		s.log.WarnContext(ctx, "could not persist the refused send's D8", "run_id", runID, "error", err)
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

// semanticPredictors are the prior SEMANTIC evals of the chosen draft (same run, same draft index) that warned or
// failed on a dimension the human's edits touched. Semantic evals are never re-run at send, so the "fail now
// passes" test of repairedEvals can never explain an edit a semantic WARN or FAIL predicted (a CTA that was too
// strong, a champion bypassed): every such edit was wrongly reported unexplained and seeded candidate
// knowledge. A semantic verdict that named the corrected dimension now counts as the explanation (HAR-97 D6).
func semanticPredictors(ctx context.Context, tx *sql.Tx, runID string, draftIndex int, edits []Edit) ([]repaired, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id::text, evaluator, evaluator_version, kind, COALESCE(rationale, ''), COALESCE(suggested_correction, '')
 FROM eval_runs WHERE agent_run_id = $1::uuid AND draft_index = $2 AND kind = 'semantic' AND verdict IN ('fail', 'warn')
 ORDER BY evaluator, id`, runID, draftIndex)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read semantic evals of run %s: %w", runID, err)
	}
	defer rows.Close()
	var out []repaired
	for rows.Next() {
		var f repaired
		if err := rows.Scan(&f.id, &f.evalType, &f.version, &f.kind, &f.reason, &f.correction); err != nil {
			return nil, fmt.Errorf("strategystore: scan semantic eval: %w", err)
		}
		if semanticExplainsEdits(f, edits) {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// semanticExplainsEdits is true when the semantic eval flagged the SAME dimension the edits changed. A structured edit
// (a recipient, a CTA, a time, the CRM next step) names its dimension, so the eval's class must match it. A paragraph or
// subject edit does not name one, so the class alone is not enough: the eval's reason or suggested correction must also
// share a substantive word with the text the human changed. An unrelated warn therefore never explains an edit.
func semanticExplainsEdits(f repaired, edits []Edit) bool {
	classes := bucket2.EvalClasses(f.evalType)
	for _, e := range edits {
		for _, k := range bucket2.LiteralKindClasses(e.Kind) {
			if !containsString(classes, k) {
				continue
			}
			if bucket2.EditKindNamesItsClass(e.Kind) || sharesWord(f.reason+" "+f.correction, fmt.Sprint(e.Before)+" "+fmt.Sprint(e.After)) {
				return true
			}
		}
	}
	return false
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

const minSharedWord = 5 // letters: shorter words are too common to show the eval was about this text

// sharesWord reports whether a and b share a word of at least minSharedWord letters.
func sharesWord(a, b string) bool {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(b), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len(w) >= minSharedWord {
			words[w] = true
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(a), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if words[w] {
			return true
		}
	}
	return false
}

// refusalDetail is "eval: reason" for each blocking eval, for the log. The 422 message names the evals only.
func refusalDetail(blocked []deterministic.EvalResult) string {
	parts := make([]string, len(blocked))
	for i, r := range blocked {
		parts[i] = string(r.EvalType)
		if r.Reason != "" {
			parts[i] += ": " + r.Reason
		}
	}
	return strings.Join(parts, " | ")
}

// logRefusedSend records which blocking evals refused a send. The refusal rolls its transaction back and a reset may
// wipe the persisted rows, so without this line nothing says why Cliff did not send.
func (s *Service) logRefusedSend(ctx context.Context, runID string, blocked []deterministic.EvalResult) {
	s.log.WarnContext(ctx, "send refused", "run_id", runID, "reason", "blocking_eval", "blocked", refusalDetail(blocked))
}
