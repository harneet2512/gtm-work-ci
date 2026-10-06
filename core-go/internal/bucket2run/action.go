package bucket2run

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

// afterGates are D7, D9 and D10: propagation, execution and feedback emission. rejudged are the stored results that
// judged the edited artifact again (row ids), which D7 cites when the intent changed.
func (r *Runner) afterGates(ctx context.Context, d episodeData, rejudged []string) ([]bucket2.Result, error) {
	if d.decision == nil {
		return nil, nil
	}
	var out []bucket2.Result
	var errs []error
	if inv, err := r.recompute.Recomputation(ctx, d.runID); err != nil {
		errs = append(errs, fmt.Errorf("bucket2run: recomputation of run %s: %w", d.runID, err))
	} else {
		intent := d.inference != nil && contains(d.inference.Delta.Classes, "strategy")
		out = append(out, bucket2.PropagationResults(bucket2.PropagationInput{Invalidation: toInvalidation(inv), IntentChanged: intent, RejudgedAfterEdit: rejudged})...)
	}
	x, err := r.execution(ctx, d)
	errs = append(errs, err)
	if err == nil {
		out = append(out, bucket2.ExecutionResults(x)...)
	}
	fb, err := r.feedback(ctx, d)
	errs = append(errs, err)
	if err == nil {
		out = append(out, bucket2.FeedbackResults(fb)...)
	}
	return out, errors.Join(errs...)
}

// finalGates is D8 (normally already measured before the send, see JudgeFinalArtifact) plus, when the person changed the intent, the D2 judgment of the edited candidate: the edited
// artifact is judged again, not only the original.
func (r *Runner) finalGates(ctx context.Context, d episodeData, have map[string]bool) ([]bucket2.Result, error) {
	if d.decision == nil {
		return nil, nil
	}
	out, err := r.finalArtifactGates(ctx, d, have)
	if err != nil || r.judge == nil || d.decision == nil || d.decision.Send != "send" {
		return out, err
	}
	if d.inference == nil || !contains(d.inference.Delta.Classes, "strategy") {
		return out, nil
	}
	chosen, ok := d.candidate(d.decision.Selected)
	if !ok {
		return out, nil
	}
	final := chosen.Artifact
	if d.decision.FinalArt != nil {
		final = *d.decision.FinalArt
	}
	view := strategyView(chosen)
	view["artifact"] = map[string]any{"channel": final.Channel, "text": final.text()}
	resp, err := r.ask(ctx, "candidate_quality", chosen.ID, map[string]any{"candidate": view}, append([]string{"candidate:" + chosen.ID}, chosen.evidenceIDs()...))
	if err != nil {
		return out, err
	}
	return append(out, bucket2.FromDimensions(toJudged("D2", "edited_candidate", "FinalArtifact", chosen.ID, "recomputed_action:"+d.set.EpisodeID, resp))), nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// finalRecipients are the final to and cc: the saved edits when there are any, else the selected candidate's.
func finalRecipients(d episodeData, chosen candidateDoc) (to, cc []string) {
	src, srcCC := chosen.To, chosen.CC
	if d.decision.FinalTo != nil {
		src, srcCC = d.decision.FinalTo, d.decision.FinalCC
	}
	for _, p := range src {
		to = append(to, p.PersonID)
	}
	for _, p := range srcCC {
		cc = append(cc, p.PersonID)
	}
	return to, cc
}

func (r *Runner) sendEvals(ctx context.Context, d episodeData) ([]bucket2.SendEval, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT e.id::text, e.evaluator, e.verdict, e.blocking FROM send_eval_results s
 JOIN eval_runs e ON e.id = s.eval_run_id WHERE s.human_strategy_decision_id = $1::uuid ORDER BY e.evaluator`, d.decision.ID)
	if err != nil {
		return nil, fmt.Errorf("bucket2run: send-time evals of decision %s: %w", d.decision.ID, err)
	}
	defer rows.Close()
	var out []bucket2.SendEval
	for rows.Next() {
		var e bucket2.SendEval
		if err := rows.Scan(&e.ID, &e.EvalType, &e.Verdict, &e.Blocking); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Runner) people(ctx context.Context, accountID string) (all, internal map[string]bool, err error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id::text, internal_only FROM people WHERE account_id = $1::uuid OR kind = 'employee'`, accountID)
	if err != nil {
		return nil, nil, fmt.Errorf("bucket2run: people of account %s: %w", accountID, err)
	}
	defer rows.Close()
	all, internal = map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id string
		var only bool
		if err := rows.Scan(&id, &only); err != nil {
			return nil, nil, err
		}
		all[id], internal[id] = true, only
	}
	return all, internal, rows.Err()
}

// finalArtifactGates is D8: deterministic recipient, stale-content and policy checks plus one model judgment
// of the exact artifact. Only a send has a final artifact to judge; otherwise D8 is not measured. D8 is live and
// required immediately BEFORE the send (strategystore runs it inside the send-time evaluation and stores the model
// judgment): this post-send pass only refreshes the deterministic checks and asks the model only when no pre-send
// model result exists (a core started without the pre-send judge), so one send costs one D8 model call.
func (r *Runner) finalArtifactGates(ctx context.Context, d episodeData, have map[string]bool) ([]bucket2.Result, error) {
	chosen, ok := d.candidate(d.decision.Selected)
	if !ok || d.decision.Send != "send" {
		return nil, nil
	}
	to, cc := finalRecipients(d, chosen)
	body, final := chosen.Artifact.Body, chosen.Artifact
	if d.decision.FinalArt != nil {
		final = *d.decision.FinalArt
		body = final.Body
	}
	var others []string
	for _, c := range d.set.Candidates {
		if c.ID != chosen.ID {
			others = append(others, c.Artifact.Body)
		}
	}
	people, internal, err := r.people(ctx, d.set.AccountID)
	if err != nil {
		return nil, err
	}
	evals, err := r.sendEvals(ctx, d)
	if err != nil {
		return nil, err
	}
	out := bucket2.FinalArtifactResults(bucket2.FinalArtifact{EpisodeID: d.set.EpisodeID, CandidateID: chosen.ID, To: to, CC: cc,
		AccountPeople: people, InternalOnly: internal, Body: body, SelectedBody: chosen.Artifact.Body, OtherBodies: others, SendEvals: evals})
	if r.judge == nil || have["D8|model|"+chosen.ID] {
		return out, nil
	}
	payload := map[string]any{"intent": strategyView(chosen), "recipients": append(append([]string{}, to...), cc...),
		"artifact": map[string]any{"channel": final.Channel, "text": final.text()}}
	resp, err := r.ask(ctx, "final_artifact", chosen.ID, payload, append([]string{"candidate:" + chosen.ID}, chosen.evidenceIDs()...))
	if err != nil {
		return out, err
	}
	j := toJudged("D8", "model", "FinalArtifact", chosen.ID, "recomputed_action:"+d.set.EpisodeID, resp)
	return append(out, bucket2.FromDimensions(j)), nil
}

// JudgeFinalArtifact implements strategystore.FinalArtifactJudge: the model half of D8 on the exact artifact about
// to be sent, through the worker's normal decision-judge path (cached and replayed like every other judge call).
func (r *Runner) JudgeFinalArtifact(ctx context.Context, in strategystore.FinalArtifactInput) (bucket2.Result, error) {
	if r.judge == nil {
		return bucket2.Result{}, errors.New("bucket2run: no model judge is configured")
	}
	to := make([]map[string]string, 0, len(in.To))
	for _, p := range in.To {
		to = append(to, map[string]string{"person_id": p})
	}
	intent := map[string]any{"candidate_id": in.CandidateID, "action_type": in.ActionType, "description": in.Intent,
		"rationale": in.Rationale, "to": to, "knowledge_refs": in.KnowledgeRefs, "evidence": in.EvidenceIDs}
	art := artifactDoc{Channel: in.Artifact.Channel, Subject: in.Artifact.Subject, Body: in.Artifact.Body}
	payload := map[string]any{"intent": intent, "recipients": append(append([]string{}, in.To...), in.CC...),
		"artifact": map[string]any{"channel": art.Channel, "text": art.text()}}
	resp, err := r.ask(ctx, "final_artifact", in.CandidateID, payload, in.EvidenceIDs)
	if err != nil {
		return bucket2.Result{}, err
	}
	return bucket2.FromDimensions(toJudged("D8", "model", "FinalArtifact", in.CandidateID, "recomputed_action:"+in.EpisodeID, resp)), nil
}

func (r *Runner) execution(ctx context.Context, d episodeData) (bucket2.Execution, error) {
	chosen, _ := d.candidate(d.decision.Selected)
	to, _ := finalRecipients(d, chosen)
	x := bucket2.Execution{EpisodeID: d.set.EpisodeID, RunID: d.runID, DecisionID: d.decision.ID, SendDecision: d.decision.Send,
		EpisodeLinked: d.decision.EpisodeID == d.set.EpisodeID && d.decision.RunID == d.runID}
	if len(to) > 0 {
		x.ExpectedTargets = to[:1] // the recorded effect carries the primary recipient only; cc is in the stored decision and D8
	}
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(detail->'recorded_effect'->>'kind', ''), COALESCE(detail->'recorded_effect'->>'idempotency_key', ''),
 COALESCE(detail->'recorded_effect'->>'target', '') FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'execute' AND status = 'recorded'`, d.runID)
	if err != nil {
		return x, fmt.Errorf("bucket2run: recorded effects of run %s: %w", d.runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var e bucket2.Effect
		var target string
		if err := rows.Scan(&e.Kind, &e.IdempotencyKey, &target); err != nil {
			return x, err
		}
		e.Targets = []string{target}
		x.Effects = append(x.Effects, e)
	}
	if err := rows.Err(); err != nil {
		return x, err
	}
	evals, err := r.sendEvals(ctx, d)
	if err != nil {
		return x, err
	}
	for _, e := range evals {
		if e.Blocking && bucket2.ParseVerdict(e.Verdict) == bucket2.Fail {
			x.BlockingFailAtSend = true
		}
	}
	x.RequiredWrites = map[string]bool{}
	for name, q := range map[string]string{
		"human_decision":  `SELECT EXISTS (SELECT 1 FROM human_decisions WHERE agent_run_id = $1::uuid)`,
		"episode_decided": `SELECT EXISTS (SELECT 1 FROM decision_episodes WHERE agent_run_id = $1::uuid AND status IN ('decided', 'judged'))`,
	} {
		var ok bool
		if err := r.db.QueryRowContext(ctx, q, d.runID).Scan(&ok); err != nil {
			return x, fmt.Errorf("bucket2run: required write %s of run %s: %w", name, d.runID, err)
		}
		x.RequiredWrites[name] = ok
	}
	return x, nil
}

func (r *Runner) feedback(ctx context.Context, d episodeData) (bucket2.Feedback, error) {
	f := bucket2.Feedback{EpisodeID: d.set.EpisodeID, Chose: true, Sent: d.decision.Send == "send", Edited: d.decision.EditsCount > 0,
		SelectionID: d.decision.ID}
	if d.inference != nil {
		f.SemanticEditID = d.inference.ID
		var id sql.NullString
		err := r.db.QueryRowContext(ctx, `SELECT id::text FROM judgment_verdicts WHERE judgment_inference_id = $1::uuid
 AND corrected_statement IS NOT NULL ORDER BY created_at LIMIT 1`, d.inference.ID).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, fmt.Errorf("bucket2run: explanation of %s: %w", d.inference.ID, err)
		}
		f.ExplanationID = id.String
	}
	if f.Sent {
		// The executed action is the effect the run really recorded (its idempotency key), never an assumed id.
		var key sql.NullString
		err := r.db.QueryRowContext(ctx, `SELECT detail->'recorded_effect'->>'idempotency_key' FROM agent_run_steps
 WHERE agent_run_id = $1::uuid AND step = 'execute' AND status = 'recorded' ORDER BY seq LIMIT 1`, d.runID).Scan(&key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, fmt.Errorf("bucket2run: recorded effect of run %s: %w", d.runID, err)
		}
		f.ExecutedActionID = key.String
	}
	var reaction sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id::text FROM customer_reactions WHERE agent_run_id = $1::uuid ORDER BY created_at LIMIT 1`, d.runID).Scan(&reaction)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return f, fmt.Errorf("bucket2run: reaction of run %s: %w", d.runID, err)
	}
	f.ReactionID = strings.TrimSpace(reaction.String)
	return f, nil
}
