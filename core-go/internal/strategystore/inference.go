package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// JudgmentInferrer produces the JudgmentInference payload (the worker's POST /v1/judgment-inference,
// HAR-97 D5). *workerclient.Client implements it.
type JudgmentInferrer interface {
	InferJudgment(ctx context.Context, req workerclient.InferJudgmentRequest) (workerclient.InferJudgmentResponse, error)
}

// WithInferrer sets the worker judgment inferrer. Without one no inference is written: Slack Message 3
// waits (inference_not_ready) rather than render a made-up interpretation.
func WithInferrer(i JudgmentInferrer) Option {
	return func(s *Service) { s.inferrer = i }
}

// semanticDelta is the part of inferred_semantic_delta the table keeps in typed columns.
type semanticDelta struct {
	Statement           string          `json:"statement"`
	SemanticLabels      []string        `json:"semantic_labels"`
	EditClass           []string        `json:"edit_class"`
	SignalStrength      string          `json:"signal_strength"`
	ExplicitInstruction json.RawMessage `json:"explicit_instructions"`
	Unknown             bool            `json:"unknown"`
	Confidence          *float64        `json:"confidence"`
}

// InferJudgment writes the episode's JudgmentInference from the worker, after the send committed. It is
// idempotent (one inference per episode) and never invents one: no inferrer, a worker failure or an answer
// out of contract returns an error and leaves the episode without an inference.
func (s *Service) InferJudgment(ctx context.Context, runID string) error {
	if s.inferrer == nil {
		return errors.New("strategystore: no judgment inferrer is configured")
	}
	strategies, err := s.Strategies(ctx, runID)
	if err != nil {
		return fmt.Errorf("strategystore: read strategies for the inference: %w", err)
	}
	decision, err := s.Decision(ctx, runID)
	if err != nil {
		return fmt.Errorf("strategystore: read decision for the inference: %w", err)
	}
	var parts struct {
		StrategySet json.RawMessage   `json:"strategy_set"`
		EvalBundles []json.RawMessage `json:"eval_bundles"`
	}
	var dec struct {
		EpisodeID string `json:"decision_episode_id"`
		Send      string `json:"send_decision"`
	}
	if err := errors.Join(json.Unmarshal(strategies, &parts), json.Unmarshal(decision, &dec)); err != nil {
		return fmt.Errorf("strategystore: decode the inference inputs: %w", err)
	}
	if dec.Send != "send" {
		return fmt.Errorf("strategystore: run %s was not sent, so there is nothing to interpret", runID)
	}
	var have bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM judgment_inferences WHERE decision_episode_id = $1::uuid)`,
		dec.EpisodeID).Scan(&have); err != nil {
		return fmt.Errorf("strategystore: check inference of %s: %w", dec.EpisodeID, err)
	}
	if have {
		return nil
	}
	// The worker is self-contained; the contract still asks for a run-scoped token string, so core sends the run id padded to length.
	resp, err := s.inferrer.InferJudgment(ctx, workerclient.InferJudgmentRequest{
		DecisionEpisodeID: dec.EpisodeID, StrategySet: parts.StrategySet, EvalBundles: parts.EvalBundles,
		HumanStrategyDecision: decision, RunToken: "gtm_ai-inference-" + runID})
	if err != nil {
		return fmt.Errorf("strategystore: infer judgment of %s: %w", dec.EpisodeID, err)
	}
	return s.storeInference(ctx, dec.EpisodeID, resp)
}

func (s *Service) storeInference(ctx context.Context, episodeID string, resp workerclient.InferJudgmentResponse) error {
	var d semanticDelta
	if err := json.Unmarshal(resp.InferredSemanticDelta, &d); err != nil || d.Statement == "" {
		return fmt.Errorf("strategystore: the inference of %s has no usable statement", episodeID)
	}
	instructions := d.ExplicitInstruction
	if len(instructions) == 0 {
		instructions = json.RawMessage(`[]`)
	}
	signal := sql.NullString{String: d.SignalStrength, Valid: d.SignalStrength != ""}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO judgment_inferences (decision_episode_id, human_strategy_decision_id, agent_preference, human_choice, agreement,
  inferred_statement, semantic_labels, edit_class, signal_strength, explicit_instructions, is_unknown, confidence, evidence, generated_at, model)
SELECT h.decision_episode_id, h.id, h.original_agent_preference, h.selected_candidate_id,
       CASE WHEN h.original_agent_preference = h.selected_candidate_id THEN 'agreed' ELSE 'overrode' END,
       $2, $3::text[], $4::text[], $5, $6::jsonb, $7, $10::numeric, $8::jsonb, now(), $9
FROM human_strategy_decisions h WHERE h.decision_episode_id = $1::uuid
ON CONFLICT (decision_episode_id) DO NOTHING`,
		episodeID, d.Statement, textArray(d.SemanticLabels), textArray(d.EditClass), signal, string(instructions),
		d.Unknown, string(resp.Evidence), resp.Model, sql.NullFloat64{Float64: floatOr(d.Confidence), Valid: d.Confidence != nil})
	if err != nil {
		return fmt.Errorf("strategystore: store the inference of %s: %w", episodeID, err)
	}
	return nil
}

// textArray renders a Postgres text[] literal ({"a","b"}); the values come from a checked vocabulary but are quoted anyway.
func textArray(v []string) string {
	out := "{"
	for i, x := range v {
		if i > 0 {
			out += ","
		}
		b, _ := json.Marshal(x)
		out += string(b)
	}
	return out + "}"
}

func floatOr(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
