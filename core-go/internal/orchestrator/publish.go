package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// publish is phase 5: one transaction writes the drafts, the EvalResults, the EvalBundles, the DecisionEpisode
// (awaiting_choice), the StrategySet with its three candidates and flips the run to awaiting_human. Its commit is
// the single visibility point: either the whole set is visible or nothing is (invariants I1, I2). No model call
// and no worker call happens inside it (I9).
func (s *Service) publish(ctx context.Context, run runRow, w world, gs guidanceSet, episodeID string, gen generation) (Outcome, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, transient("publish", err)
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM agent_runs WHERE id = $1::uuid FOR UPDATE`, run.ID).Scan(&status); err != nil {
		return Outcome{}, transient("publish", fmt.Errorf("lock run: %w", err))
	}
	if status != "context_built" {
		if out, ok, err := s.published(ctx, run.ID); err == nil && ok {
			return out, nil
		}
		return Outcome{}, fmt.Errorf("%w: run %s is %s at publish", ErrNotRunnable, run.ID, status)
	}
	setID := newID()
	if err := s.writeSet(ctx, tx, run, w, gs, episodeID, setID, gen); err != nil {
		return Outcome{}, transient("publish", err)
	}
	if err := tx.Commit(); err != nil {
		if out, ok, perr := s.published(ctx, run.ID); perr == nil && ok {
			return out, nil // a concurrent caller won the race: converge on its set
		}
		return Outcome{}, transient("publish", fmt.Errorf("commit: %w", err))
	}
	out := Outcome{RunID: run.ID, SetID: setID, EpisodeID: episodeID, Published: true}
	if !gen.NoAcceptable {
		out.PreferredID = gen.Set[0].Final.Candidate.CandidateID
	}
	s.log.InfoContext(ctx, "strategy set published", "run_id", run.ID, "set_id", setID, "preferred", out.PreferredID,
		"no_acceptable_candidate", gen.NoAcceptable)
	if s.after != nil {
		s.after(ctx, run.ID)
	}
	return out, nil
}

func (s *Service) writeSet(ctx context.Context, tx *sql.Tx, run runRow, w world, gs guidanceSet, episodeID, setID string, gen generation) error {
	status, to, from := w.transitionFields()
	suite := s.cfg.Routing.Select(status, to, from, w.relationship()) // the episode's own route; each candidate has its own below
	bundles := map[int]string{}                                       // draft_index -> bundle id
	set := gen.Set
	for _, e := range set {
		for _, v := range versionsOf(e) {
			if err := s.writeDraft(ctx, tx, run, v); err != nil {
				return err
			}
			if _, err := evalstore.Save(ctx, tx, v.Results); err != nil {
				return err
			}
			id, err := s.writeBundle(ctx, tx, run.ID, v, CandidatePolicy(status, v.Candidate))
			if err != nil {
				return err
			}
			bundles[v.DraftIndex] = id
		}
	}
	if err := s.writeEpisode(ctx, tx, run, w, gs, episodeID, suite); err != nil {
		return err
	}
	stateRef := mustJSON(map[string]any{"account_id": run.AccountID, "version": w.State.Version})
	if _, err := tx.ExecContext(ctx, `INSERT INTO strategy_sets (id, decision_episode_id, agent_run_id, account_id, opportunity_id, generated_at,
 state_ref, state_diff_id, trigger_activity_ids, decision_guidance_id, no_acceptable_candidate)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7::jsonb, $8::uuid, $9::uuid[], $10::uuid, $11)`,
		setID, episodeID, run.ID, run.AccountID, nullable(run.OpportunityID), s.clk.Now().UTC(), string(stateRef),
		nullable(w.StateDiffID), signalstore.UUIDArray(run.TriggerIDs), gs.Doc.ID, gen.NoAcceptable); err != nil {
		return fmt.Errorf("insert strategy set: %w", err)
	}
	for _, e := range set {
		if err := writeCandidate(ctx, tx, run.ID, setID, e.Final, bundles[e.Final.DraftIndex]); err != nil {
			return err
		}
	}
	return s.finishRun(ctx, tx, run.ID, gs, gen)
}

func versionsOf(e evaluated) []version {
	if e.Original != nil {
		return []version{*e.Original, e.Final}
	}
	return []version{e.Final}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) writeDraft(ctx context.Context, tx *sql.Tx, run runRow, v version) error {
	c := v.Candidate
	decision := map[string]any{"action": c.ActionType, "action_class": c.ActionClass, "why_now": clip(c.FiveQuestions.WhyNextAction, 1000),
		"used_guidance": len(c.KnowledgeRefs) > 0, "who_to_involve": involved(c), "who_not_to_involve": []any{}}
	feedback := v.Feedback
	if feedback == nil {
		feedback = []workerclient.Feedback{}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_run_drafts (agent_run_id, draft_index, source, output, revision_feedback, model, decision)
 VALUES ($1::uuid, $2, $3, $4::jsonb, $5::jsonb, $6, $7::jsonb)`, run.ID, v.DraftIndex, v.Source,
		string(mustJSON(draftOutput(c))), string(mustJSON(feedback)), v.Model, string(mustJSON(decision))); err != nil {
		return fmt.Errorf("insert draft %d: %w", v.DraftIndex, err)
	}
	return nil
}

func (s *Service) writeBundle(ctx context.Context, tx *sql.Tx, runID string, v version, policy *PolicyVerdict) (string, error) {
	id := newID()
	var pol any
	if policy != nil {
		pol = string(mustJSON(policy))
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO eval_bundles (id, agent_run_id, draft_index, items, generated_at, selected_eval_suite, candidate_policy)
 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5, $6, $7::jsonb)`, id, runID, v.DraftIndex, string(mustJSON(v.Items)),
		s.clk.Now().UTC(), nullable(v.Suite), pol); err != nil {
		return "", fmt.Errorf("insert eval bundle of draft %d: %w", v.DraftIndex, err)
	}
	return id, nil
}

func (s *Service) writeEpisode(ctx context.Context, tx *sql.Tx, run runRow, w world, gs guidanceSet, episodeID, suite string) error {
	var transition any
	var tstatus any
	supporting, missing := []any{}, []any{}
	if t := w.Transition; t != nil {
		transition, tstatus = string(mustJSON(t)), t.Status
		for _, f := range t.SupportingFacts {
			supporting = append(supporting, map[string]any{"fact_key": f.Key, "evidence_refs": f.EvidenceRefs})
		}
		for _, f := range t.MissingFacts {
			missing = append(missing, map[string]any{"fact_key": f.Key, "required": f.Required, "description": f.Description})
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_episodes (id, agent_run_id, account_id, state_version, state_diff_id, decision_guidance_id,
 status, account_change_id, business_intelligence_update_id, state_transition, transition_status, supporting_evidence, missing_evidence,
 selected_eval_suite)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid, $6::uuid, 'awaiting_choice', $7::uuid, $8::uuid, $9::jsonb, $10, $11::jsonb, $12::jsonb, $13)`,
		episodeID, run.ID, run.AccountID, w.State.Version, nullable(w.StateDiffID), gs.Doc.ID, nullable(w.AccountChangeID),
		nullable(w.BIUpdateID), transition, tstatus, string(mustJSON(supporting)), string(mustJSON(missing)), nullable(suite)); err != nil {
		return fmt.Errorf("insert decision episode: %w", err)
	}
	return nil
}

func writeCandidate(ctx context.Context, tx *sql.Tx, runID, setID string, v version, bundleID string) error {
	c := v.Candidate
	if _, err := tx.ExecContext(ctx, `INSERT INTO strategy_candidates (id, strategy_set_id, agent_run_id, draft_index, strategy_type, title, description,
 ranking, preferred_by_agent, rationale, state_refs, evidence_refs, knowledge_refs, action_type, to_recipients, cc_recipients, subject,
 full_action_artifact, preview, eval_bundle_id, action_class, five_questions, selected_eval_suite)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9, $10, $11::text[], $12::jsonb, $13::uuid[], $14, $15::jsonb, $16::jsonb, $17,
 $18::jsonb, $19, $20::uuid, $21, $22::jsonb, $23)`,
		c.CandidateID, setID, runID, v.DraftIndex, c.StrategyType, c.Title, c.Description, c.Ranking, c.PreferredByAgent, c.Rationale,
		signalstore.UUIDArray(c.StateRefs), string(mustJSON(c.EvidenceRefs)), signalstore.UUIDArray(c.KnowledgeRefs), c.ActionType,
		string(mustJSON(c.To)), string(mustJSON(c.CC)), c.Subject, string(mustJSON(c.FullActionArtifact)), c.Preview, bundleID,
		c.ActionClass, string(mustJSON(c.FiveQuestions)), nullable(v.Suite)); err != nil {
		return fmt.Errorf("insert candidate %s: %w", c.StrategyType, err)
	}
	return nil
}

// finishRun flips the run to awaiting_human (never rests in drafted), mirrors the preferred output on the run,
// completes the E7 attribution (retrieved -> applicable -> used -> changed, graded) and records the steps.
func (s *Service) finishRun(ctx context.Context, tx *sql.Tx, runID string, gs guidanceSet, gen generation) error {
	used := usedKnowledge(gen.Set)
	best := gen.Set[0].Final
	res, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status = 'awaiting_human', output = $2::jsonb, model = $3, knowledge_refs_used = $4::jsonb,
 updated_at = now() WHERE id = $1::uuid AND status = 'context_built'`, runID, string(mustJSON(draftOutput(best.Candidate))), best.Model, string(mustJSON(used)))
	if err != nil {
		return fmt.Errorf("move run to awaiting_human: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the run left context_built during publish")
	}
	steps := []struct {
		query string
		args  []any
	}{
		{`UPDATE agent_run_steps SET detail = jsonb_set(jsonb_set(detail, '{knowledge_attribution,used}', $2::jsonb), '{knowledge_attribution,influence}', $3::jsonb)
 WHERE agent_run_id = $1::uuid AND step = 'build_context'`, []any{runID, string(mustJSON(used)), string(mustJSON(gen.Influence))}},
		{`UPDATE agent_run_steps SET status = 'succeeded', finished_at = now(), detail = detail || '{"published": true}'::jsonb WHERE agent_run_id = $1::uuid AND step = 'draft'`, []any{runID}},
		{`UPDATE agent_run_steps SET status = 'skipped', detail = '{"reason": "the CRM intent is recorded with the human decision"}'::jsonb WHERE agent_run_id = $1::uuid AND step = 'crm_intent'`, []any{runID}},
		{`UPDATE agent_run_steps SET status = 'running', started_at = now() WHERE agent_run_id = $1::uuid AND step = 'await_human'`, []any{runID}},
	}
	for _, q := range steps {
		if _, err := tx.ExecContext(ctx, q.query, q.args...); err != nil {
			return fmt.Errorf("record steps: %w", err)
		}
	}
	return nil
}

func involved(c workerclient.Candidate) []map[string]string {
	out := []map[string]string{}
	for _, r := range c.To {
		why := r.Why
		if why == "" {
			why = "recipient"
		}
		out = append(out, map[string]string{"person_id": r.PersonID, "why": clip(why, 500)})
	}
	return out
}
