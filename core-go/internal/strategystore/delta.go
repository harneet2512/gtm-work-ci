package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// DeltaLabeler computes the semantic part of a HumanDelta (the worker's POST /v1/human-delta).
// *workerclient.Client implements it.
type DeltaLabeler interface {
	LabelDelta(ctx context.Context, req workerclient.LabelDeltaRequest) (workerclient.LabelDeltaResponse, error)
}

// semanticLabelVocab is the human_delta.v1.json semantic_labels enum — the labels a worker answer may use.
var semanticLabelVocab = map[string]bool{
	"reduced_pressure": true, "increased_pressure": true, "kept_champion_involved": true,
	"removed_unnecessary_stakeholders": true, "added_missing_stakeholder": true, "delayed_cta": true,
	"removed_cta": true, "smaller_ask": true, "larger_ask": true, "changed_channel": true,
	"corrected_fact": true, "deferred_to_buyer_timing": true, "style_only": true,
}

// writeDelta persists the HumanDelta of an edited send and its explaining eval links, and returns the
// delta's id (the episode's human_delta_id). The literal changes are core's own diff; the semantics come
// from the labeler, with a deterministic fallback when it is unreachable or answers out of contract.
// "" means no delta was written (the artifact is byte-identical to the candidate's). Downstream of the
// row the HAR-119 loop writes: explained deltas queue generator_feedback for the account's next
// strategies call; unexplained deltas seed the candidate criterion (evaluator version + knowledge).
func (s *Service) writeDelta(ctx context.Context, tx *sql.Tx, ep episode, runID string, chosen candidate, final Draft, se sendEval) (string, error) {
	edits := ComputeEdits(chosen.Draft, final)
	if len(edits) == 0 {
		return "", nil
	}
	repaired, err := repairedEvals(ctx, tx, runID, chosen.DraftIndex, se.Results)
	if err != nil {
		return "", err
	}
	literal, err := marshal(edits)
	if err != nil {
		return "", err
	}
	unexplained := len(repaired) == 0
	labels, criterion, model := s.deltaSemantics(ctx, tx, ep, runID, chosen, final, edits, literal, unexplained, explainingEvals(repaired))
	var deltaID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO human_deltas (decision_episode_id, literal_changes, semantic_labels, unexplained, candidate_criterion, model)
VALUES ($1::uuid, $2::jsonb, $3::text[], $4, $5::jsonb, $6) RETURNING id::text`,
		ep.ID, string(literal), labels, unexplained, criterion, model).Scan(&deltaID); err != nil {
		return "", fmt.Errorf("strategystore: write human delta of episode %s: %w", ep.ID, err)
	}
	for _, e := range repaired {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO human_delta_explanations (human_delta_id, eval_run_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
			deltaID, e.id); err != nil {
			return "", fmt.Errorf("strategystore: link delta %s to eval %s: %w", deltaID, e.id, err)
		}
	}
	if unexplained {
		if err := s.seedCriterion(ctx, tx, ep, deltaID, edits, criterion, se.At); err != nil {
			return "", err
		}
	} else if err := queueFeedback(ctx, tx, ep, deltaID, repaired); err != nil {
		return "", err
	}
	return deltaID, nil
}

// deltaSemantics are the semantic_labels, candidate_criterion and model of the delta: the labeler's answer
// when it is in contract, else the fallback (empty labels; a core-written criterion when unexplained).
// literal is the already-marshaled edits payload the caller writes; it is reused for the labeler request.
func (s *Service) deltaSemantics(ctx context.Context, tx *sql.Tx, ep episode, runID string, chosen candidate, final Draft,
	edits []Edit, literal []byte, unexplained bool, explained []workerclient.ExplainingEval) (labels []string, criterion, model any) {
	fallback := func(why error) ([]string, any, any) {
		s.log.WarnContext(ctx, "delta labeler unusable; writing the unlabeled fallback", "run_id", runID, "episode_id", ep.ID, "error", why)
		return fallbackSemantics(edits, unexplained)
	}
	if s.labeler == nil {
		return fallback(fmt.Errorf("no delta labeler is configured"))
	}
	var cand string
	if err := tx.QueryRowContext(ctx, `SELECT `+candidateJSON+` FROM strategy_candidates c WHERE c.id = $1::uuid`,
		chosen.ID).Scan(&cand); err != nil {
		return fallback(fmt.Errorf("read the selected candidate: %w", err))
	}
	resp, err := s.labeler.LabelDelta(ctx, workerclient.LabelDeltaRequest{
		RunID: runID, DecisionEpisodeID: ep.ID, AccountID: ep.AccountID,
		SelectedCandidate: []byte(cand),
		FinalAction: workerclient.DeltaFinalAction{To: toWire(final.To), CC: toWire(final.CC),
			Artifact: workerclient.Artifact{Channel: final.Artifact.Channel, Subject: final.Artifact.Subject,
				Body: final.Artifact.Body, Attachments: final.Artifact.Attachments}},
		LiteralChanges:  literal,
		Unexplained:     unexplained,
		ExplainingEvals: explained,
	})
	if err != nil {
		return fallback(err)
	}
	if bad := labelErr(resp, unexplained); bad != nil {
		return fallback(bad)
	}
	crit := any(nil)
	if unexplained {
		c, err := marshal(resp.CandidateCriterion)
		if err != nil {
			return fallback(err)
		}
		crit = string(c)
	}
	return resp.SemanticLabels, crit, resp.Model
}

// labelErr reports why a labeler answer is out of contract: a label outside the vocabulary, or a missing
// or unusable criterion on an unexplained delta (the suggested axis must be a catalogued eval_type — an
// invented axis could never hold an evaluator version). (A criterion on an explained delta is dropped,
// not an error.)
func labelErr(resp workerclient.LabelDeltaResponse, unexplained bool) error {
	for _, l := range resp.SemanticLabels {
		if !semanticLabelVocab[l] {
			return fmt.Errorf("worker returned a label outside the HumanDelta vocabulary: %q", l)
		}
	}
	if unexplained {
		c := resp.CandidateCriterion
		if c == nil || strings.TrimSpace(c.Statement) == "" || len(c.Statement) > 1000 ||
			!learning.ValidAxis(strings.TrimSpace(c.SuggestedEvalType)) {
			return fmt.Errorf("worker returned no usable candidate_criterion for an unexplained delta")
		}
	}
	return nil
}

// fallbackSemantics is the core-written delta content when no labeler answer is usable: empty
// semantic_labels and, for an unexplained delta, a candidate criterion derived from the literal edits.
func fallbackSemantics(edits []Edit, unexplained bool) ([]string, any, any) {
	if !unexplained {
		return []string{}, nil, nil
	}
	c, err := marshal(fallbackCriterion(edits))
	if err != nil {
		return []string{}, nil, nil
	}
	return []string{}, string(c), nil
}

// fallbackCriterion proposes the eval axis the edit kinds point at. The first edit wins the axis: a person
// change asks about stakeholder selection, a channel change about channel fit, anything else about action
// quality.
func fallbackCriterion(edits []Edit) workerclient.DeltaCriterion {
	axis := map[string]string{
		"recipient_added": "stakeholder_selection", "recipient_removed": "stakeholder_selection",
		"recipient_role_changed": "stakeholder_selection", "channel_changed": "channel_appropriateness",
		"attachment_added": "evidence_sufficiency", "attachment_removed": "evidence_sufficiency",
	}
	evalType := "next_action_quality"
	var kinds []string
	for _, e := range edits {
		if t, ok := axis[e.Kind]; ok && evalType == "next_action_quality" {
			evalType = t
		}
		if !contains(kinds, e.Kind) {
			kinds = append(kinds, e.Kind)
		}
	}
	return workerclient.DeltaCriterion{
		Statement: fmt.Sprintf("The human's send-time edit is unexplained by the eval set: it changed %s. "+
			"Candidate eval axis for the learning loop to scope and backtest: %s.", strings.Join(kinds, ", "), evalType),
		SuggestedEvalType: evalType,
	}
}

func toWire(rs []Recipient) []workerclient.Recipient {
	out := make([]workerclient.Recipient, len(rs))
	for i, r := range rs {
		out[i] = workerclient.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why}
	}
	return out
}

// explainingEvals is the labeler-request view of the repaired evals (workerclient's human-delta shape).
func explainingEvals(rs []repaired) []workerclient.ExplainingEval {
	out := make([]workerclient.ExplainingEval, len(rs))
	for i, r := range rs {
		out[i] = workerclient.ExplainingEval{EvalResultID: r.id, EvalType: r.evalType, Reason: r.reason}
	}
	return out
}

// queueFeedback enqueues one generator_feedback row per explaining eval (HAR-119): the eval whose
// earlier failure the send repaired is a proven correction the account's next strategies call receives.
// The instruction is the failure's suggested correction, else its rationale — the same text the
// revision planner would have been given.
func queueFeedback(ctx context.Context, tx *sql.Tx, ep episode, deltaID string, repaired []repaired) error {
	for _, r := range repaired {
		instruction := r.correction
		if strings.TrimSpace(instruction) == "" {
			instruction = r.reason
		}
		if strings.TrimSpace(instruction) == "" {
			continue // a feedback without an instruction teaches nothing
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO generator_feedback
 (account_id, human_delta_id, decision_episode_id, eval_run_id, eval_type, instruction)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6)
 ON CONFLICT (human_delta_id, eval_run_id) DO NOTHING`,
			ep.AccountID, deltaID, ep.ID, r.id, r.evalType, clipText(instruction)); err != nil {
			return fmt.Errorf("strategystore: queue generator feedback of delta %s: %w", deltaID, err)
		}
	}
	return nil
}

// seedCriterion feeds the unexplained delta into the learning loop (HAR-119): the candidate criterion
// becomes a candidate evaluator version (executable spec = the literal changes) and a candidate
// knowledge row, and the delta's stored criterion links the knowledge it seeded.
func (s *Service) seedCriterion(ctx context.Context, tx *sql.Tx, ep episode, deltaID string, edits []Edit, criterion any, at time.Time) error {
	crit, ok := decodeCriterion(criterion)
	if !ok {
		return nil // unexplained but no usable criterion was stored: nothing to seed
	}
	sit, err := learning.EpisodeSituation(ctx, tx, ep.ID)
	if err != nil {
		return err
	}
	res, err := learning.SeedDeltaCriterion(ctx, tx, learning.DeltaSeed{
		DeltaID: deltaID, EpisodeID: ep.ID, AccountID: ep.AccountID,
		Criterion: learning.Criterion{Statement: crit.Statement, SuggestedEvalType: crit.SuggestedEvalType},
		Changes:   literalChanges(edits), Situation: sit, At: at}, s.rules)
	if err != nil {
		return fmt.Errorf("strategystore: seed criterion of delta %s: %w", deltaID, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE human_deltas SET candidate_criterion =
 jsonb_set(candidate_criterion, '{knowledge_candidate_id}', to_jsonb($2::text)) WHERE id = $1::uuid`,
		deltaID, res.KnowledgeID); err != nil {
		return fmt.Errorf("strategystore: link delta %s to knowledge %s: %w", deltaID, res.KnowledgeID, err)
	}
	return nil
}

// decodeCriterion reads the candidate_criterion payload writeDelta just stored (a jsonb string) back
// into the workerclient shape.
func decodeCriterion(v any) (workerclient.DeltaCriterion, bool) {
	var c workerclient.DeltaCriterion
	s, ok := v.(string)
	if !ok || s == "" {
		return c, false
	}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return c, false
	}
	return c, c.Statement != "" && learning.ValidAxis(c.SuggestedEvalType)
}

// literalChanges converts the edit diff into the learning spec's change list.
func literalChanges(edits []Edit) []learning.LiteralChange {
	out := make([]learning.LiteralChange, len(edits))
	for i, e := range edits {
		out[i] = learning.LiteralChange{Kind: e.Kind, Before: e.Before, After: e.After}
	}
	return out
}

// clipText bounds a stored instruction the contract's way (UTF-8 safe, 1000 runes).
func clipText(s string) string {
	const max = 1000
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
