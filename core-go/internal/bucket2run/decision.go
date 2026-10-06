package bucket2run

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// strategyView is what the decision judges see: the strategy, never the email body (rule R2).
func strategyView(c candidateDoc) map[string]any {
	to := make([]map[string]string, 0, len(c.To))
	for _, p := range c.To {
		to = append(to, map[string]string{"person_id": p.PersonID, "why": p.Why})
	}
	return map[string]any{"candidate_id": c.ID, "strategy_type": c.StrategyType, "action_class": c.ActionClass, "action_type": c.ActionType,
		"description": c.Title, "rationale": c.Rationale, "five_questions": c.FiveQuestions, "to": to,
		"knowledge_refs": c.KnowledgeRefs, "evidence": c.evidenceIDs()}
}

func (d episodeData) evidenceIDs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, c := range d.set.Candidates {
		add("candidate:" + c.ID)
		for _, e := range c.evidenceIDs() {
			add(e)
		}
	}
	sort.Strings(out)
	return out
}

func (d episodeData) knowledgeIDs() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range d.set.Candidates {
		for _, k := range c.KnowledgeRefs {
			if !seen[k] {
				seen[k] = true
				out = append(out, "knowledge:"+k)
			}
		}
	}
	return out
}

func (d episodeData) views() []map[string]any {
	out := make([]map[string]any, 0, len(d.set.Candidates))
	for _, c := range d.set.Candidates {
		out = append(out, strategyView(c))
	}
	return out
}

func (d episodeData) transitionStatus() string {
	for _, c := range d.set.Candidates {
		if p := d.bundles[c.ID].CandidatePolicy; p != nil {
			return p.TransitionStatus
		}
	}
	return ""
}

func (r *Runner) ask(ctx context.Context, kind, subject string, payload any, evidence []string) (workerclient.DecisionJudgeResponse, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return workerclient.DecisionJudgeResponse{}, err
	}
	resp, err := r.judge.DecisionJudge(ctx, workerclient.DecisionJudgeRequest{Kind: kind, SubjectID: subject, Payload: raw, EvidenceIDs: evidence})
	if err != nil {
		return resp, fmt.Errorf("bucket2run: %s judge of %s: %w", kind, subject, err)
	}
	return resp, nil
}

func toJudged(gate, sub, typ, id, span string, resp workerclient.DecisionJudgeResponse) bucket2.Judged {
	dims := make([]bucket2.Dimension, 0, len(resp.Dimensions))
	for _, x := range resp.Dimensions {
		dims = append(dims, bucket2.Dimension{Name: x.Dimension, Verdict: x.Verdict, Why: x.Why, EvidenceRefs: x.EvidenceRefs})
	}
	return bucket2.Judged{Gate: gate, SubGate: sub, Type: typ, ID: id, SpanID: span, Model: resp.Model, PromptVersion: resp.PromptVersion,
		Dimensions: dims, Summary: resp.Summary}
}

// decisionGates are D1, D2 and D3: the intent, the three candidates and the ranking.
func (r *Runner) decisionGates(ctx context.Context, d episodeData, have map[string]bool) ([]bucket2.Result, error) {
	if r.judge == nil {
		return nil, nil
	}
	var out []bucket2.Result
	var errs []error
	ev := d.evidenceIDs()
	span := "candidates:" + d.set.ID
	policy := map[string]any{"transition_status": d.transitionStatus(), "no_acceptable_candidate": d.set.NoAcceptable,
		"applicable_knowledge": d.knowledgeIDs()}

	if have["D1||"+d.set.ID] {
		// measured already for this strategy set
	} else if resp, err := r.ask(ctx, "intent_fit", d.set.ID, map[string]any{"candidates": d.views(), "state": policy}, ev); err != nil {
		errs = append(errs, err)
	} else {
		j := toJudged("D1", "", "StrategySet", d.set.ID, span, resp)
		if resp.RightReaction != nil {
			j.Extra = "right reaction: " + *resp.RightReaction
		}
		if resp.QuietMoveConsidered != nil {
			j.Extra += fmt.Sprintf("; quiet move considered: %t", *resp.QuietMoveConsidered)
		}
		out = append(out, bucket2.FromDimensions(j))
	}
	if have["D2|set|"+d.set.ID] {
		// measured already
	} else if resp, err := r.ask(ctx, "set_quality", d.set.ID, map[string]any{"candidates": d.views(), "state": policy}, ev); err != nil {
		errs = append(errs, err)
	} else {
		out = append(out, bucket2.FromDimensions(toJudged("D2", "set", "StrategySet", d.set.ID, span, resp)))
	}
	for _, c := range d.set.Candidates {
		view := strategyView(c)
		view["artifact"] = map[string]any{"channel": c.Artifact.Channel, "text": c.Artifact.text()}
		cev := append([]string{"candidate:" + c.ID}, c.evidenceIDs()...)
		if have["D2|candidate|"+c.ID] {
			continue
		}
		if resp, err := r.ask(ctx, "candidate_quality", c.ID, map[string]any{"candidate": view}, cev); err != nil {
			errs = append(errs, err)
		} else {
			out = append(out, bucket2.FromDimensions(toJudged("D2", "candidate", "StrategyCandidate", c.ID, "candidates:"+c.ID, resp)))
		}
	}
	rs, cited, err := r.rankingGates(ctx, d, ev, have)
	out = append(out, rs...)
	if err != nil {
		errs = append(errs, err)
	}
	if !have["D1|knowledge_use|"+d.set.EpisodeID] {
		if ku, err := r.knowledgeUse(ctx, d, cited); err != nil {
			errs = append(errs, err)
		} else {
			out = append(out, ku)
		}
	}
	return out, errors.Join(errs...)
}

type tierInput struct {
	CandidateID string `json:"candidate_id"`
	Blocking    bool   `json:"blocking"`
	Restricted  bool   `json:"restricted"`
	WorkerRank  int    `json:"worker_rank"`
}

func (d episodeData) tiers() []tierInput {
	out := make([]tierInput, 0, len(d.set.Candidates))
	for _, c := range d.set.Candidates {
		b := d.bundles[c.ID]
		t := tierInput{CandidateID: c.ID, WorkerRank: c.Ranking, Restricted: b.CandidatePolicy != nil && b.CandidatePolicy.Status == "restricted"}
		for _, it := range b.Items {
			if it.Result != nil && it.Result.Blocking {
				t.Blocking = true
			}
		}
		out = append(out, t)
	}
	return out
}

// rankingGates persists the DecisionRanking (order, tier inputs, one reason per adjacent pair) and judges it (D3).
func (r *Runner) rankingGates(ctx context.Context, d episodeData, ev []string, have map[string]bool) ([]bucket2.Result, []string, error) {
	if have["D3|ranking|"+d.set.ID] {
		return nil, r.citedByStored(ctx, d), nil
	}
	cands := append([]candidateDoc(nil), d.set.Candidates...)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Ranking < cands[j].Ranking })
	if len(cands) == 0 {
		return nil, nil, errors.New("bucket2run: the set has no candidates to rank")
	}
	order := make([]string, len(cands))
	for i, c := range cands {
		order[i] = c.ID
	}
	// The D3 judge reads the STORED ranking rationale. It is generated and stored once; a later run reads it back.
	reasons, found, err := r.storedReasons(ctx, d)
	if err != nil {
		return nil, nil, err
	}
	if !found {
		rat, err := r.ask(ctx, "rank_rationale", d.set.ID, map[string]any{"order": order, "candidates": d.views(), "knowledge_ids": d.knowledgeIDs(), "tier_inputs": d.tiers()}, ev)
		if err != nil {
			return nil, nil, err
		}
		raw, err := json.Marshal(rat.PairwiseReasons)
		if err != nil {
			return nil, nil, err
		}
		if err := r.saveRanking(ctx, d, order, raw, rat.Model); err != nil {
			return nil, nil, err
		}
		reasons = rat.PairwiseReasons
	}
	var cited []string
	for _, p := range reasons {
		for _, k := range p.KnowledgeRefs {
			cited = append(cited, strings.TrimPrefix(k, "knowledge:"))
		}
	}
	resp, err := r.ask(ctx, "ranking", d.set.ID, map[string]any{"order": order, "preferred": order[0], "tier_inputs": d.tiers(),
		"pairwise_reasons": reasons, "candidates": d.views(), "state": map[string]any{"transition_status": d.transitionStatus()}}, ev)
	if err != nil {
		return nil, cited, err
	}
	out := []bucket2.Result{bucket2.FromDimensions(toJudged("D3", "ranking", "DecisionRanking", d.set.ID, "ranking:"+d.set.ID, resp))}
	return append(out, preferredNotBlocked(d, order[0])), cited, nil
}

// storedReasons reads the persisted DecisionRanking's pairwise reasons for the set.
func (r *Runner) storedReasons(ctx context.Context, d episodeData) ([]workerclient.PairReason, bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT pairwise_reasons::text FROM decision_rankings WHERE strategy_set_id = $1::uuid`, d.set.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("bucket2run: stored ranking of set %s: %w", d.set.ID, err)
	}
	var out []workerclient.PairReason
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, false, fmt.Errorf("bucket2run: decode the stored ranking of set %s: %w", d.set.ID, err)
	}
	return out, true, nil
}

// citedByStored are the knowledge ids the stored ranking reasons cite (empty when none is stored).
func (r *Runner) citedByStored(ctx context.Context, d episodeData) []string {
	reasons, found, err := r.storedReasons(ctx, d)
	if err != nil || !found {
		return nil
	}
	var cited []string
	for _, p := range reasons {
		for _, k := range p.KnowledgeRefs {
			cited = append(cited, strings.TrimPrefix(k, "knowledge:"))
		}
	}
	return cited
}

// preferredNotBlocked is the deterministic half of D3: no blocked or restricted candidate may be preferred.
func preferredNotBlocked(d episodeData, preferred string) bucket2.Result {
	r := bucket2.Result{Gate: "D3", SubGate: "no_blocked_preferred", JudgedType: "DecisionRanking", JudgedID: d.set.ID, SpanID: "ranking:" + d.set.ID,
		Grader: bucket2.Deterministic, EvidenceRefs: []string{"candidate:" + preferred, "strategy_set:" + d.set.ID}}
	for _, t := range d.tiers() {
		if t.CandidateID != preferred {
			continue
		}
		r.Observed = fmt.Sprintf("preferred %s: blocking=%t restricted=%t", preferred, t.Blocking, t.Restricted)
		if t.Blocking || t.Restricted {
			r.Verdict, r.Why = bucket2.Fail, "a blocked or restricted candidate is the preferred one"
			return r.Finalize()
		}
	}
	r.Verdict, r.Why = bucket2.Pass, "the preferred candidate is neither blocked nor restricted"
	if r.Observed == "" {
		r.Verdict, r.Observed, r.Why = bucket2.Unknown, "the preferred candidate has no tier input", "it cannot be shown to be unblocked"
	}
	return r.Finalize()
}

// saveRanking stores the DecisionRanking once per set (decision_rankings, migration 0036). It is immutable:
// a second call leaves the first row.
func (r *Runner) saveRanking(ctx context.Context, d episodeData, order []string, reasons []byte, model string) error {
	tiers, err := json.Marshal(d.tiers())
	if err != nil {
		return err
	}
	abstained := d.set.NoAcceptable
	if c, ok := d.candidate(order[0]); ok && (c.ActionType == "wait" || c.ActionType == "no_action" || c.ActionType == "internal_note") {
		abstained = true
	}
	_, err = r.db.ExecContext(ctx, `
INSERT INTO decision_rankings (strategy_set_id, decision_episode_id, agent_run_id, order_ids, preferred_candidate_id, tier_inputs, pairwise_reasons, abstained, model)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid[], $5::uuid, $6::jsonb, $7::jsonb, $8, $9) ON CONFLICT (strategy_set_id) DO NOTHING`,
		d.set.ID, d.set.EpisodeID, d.runID, signalstore.UUIDArray(order), order[0], string(tiers), string(reasons), abstained, sql.NullString{String: model, Valid: model != ""})
	if err != nil {
		return fmt.Errorf("bucket2run: store the ranking of set %s: %w", d.set.ID, err)
	}
	return nil
}

// knowledgeUse reports retrieved, applicable and used as three separate values (EcoLite continuation). Retrieved
// and applicable come from the run's knowledge_attribution; used is the applicable knowledge that was in the D1 to
// D3 inputs and is cited in the ranking reasons or the intent rationale.
func (r *Runner) knowledgeUse(ctx context.Context, d episodeData, citedInRanking []string) (bucket2.Result, error) {
	var raw sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT (detail->'knowledge_attribution')::text FROM agent_run_steps
 WHERE agent_run_id = $1::uuid AND step = 'build_context' ORDER BY seq DESC LIMIT 1`, d.runID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return bucket2.Result{}, fmt.Errorf("bucket2run: knowledge attribution of run %s: %w", d.runID, err)
	}
	var attr struct {
		Retrieved  []string `json:"retrieved"`
		Applicable []string `json:"applicable"`
	}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &attr); err != nil {
			return bucket2.Result{}, fmt.Errorf("bucket2run: decode knowledge attribution of run %s: %w", d.runID, err)
		}
	}
	var offered, cited []string
	cited = append(cited, citedInRanking...)
	for _, c := range d.set.Candidates {
		offered = append(offered, c.KnowledgeRefs...)
		for _, k := range c.KnowledgeRefs {
			if strings.Contains(c.Rationale, k) || strings.Contains(string(c.FiveQuestions), k) {
				cited = append(cited, k) // the intent rationale names it
			}
		}
	}
	_, res := bucket2.MeasureKnowledgeUse(bucket2.KnowledgeUseInput{EpisodeID: d.set.EpisodeID, Retrieved: attr.Retrieved,
		Applicable: attr.Applicable, Offered: offered, Cited: cited})
	return res, nil
}
