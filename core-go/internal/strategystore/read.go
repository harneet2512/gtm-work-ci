package strategystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Service reads and writes the HAR-129 decision objects.
type Service struct {
	db      *sql.DB
	log     *slog.Logger
	labeler DeltaLabeler     // nil: every delta falls back to core's unlabeled form (HAR-139)
	params  evalinput.Params // the workspace configuration the send-time re-evaluation reads
	rules   *knowledge.Rules // nil: learned candidates seed without the episode evidence write (HAR-119)
}

// Option configures a Service.
type Option func(*Service)

// WithLabeler sets the worker delta labeler (POST /v1/human-delta). Without one, an edited send still
// writes its HumanDelta with empty semantic_labels (the core-written candidate criterion when unexplained).
func WithLabeler(l DeltaLabeler) Option {
	return func(s *Service) { s.labeler = l }
}

// WithEvalParams sets the workspace Policy and CRM rules the send-time re-evaluation applies. Empty
// fields take the dry-run defaults (evalinput.Params.Normalize).
func WithEvalParams(p evalinput.Params) Option {
	return func(s *Service) { s.params = p }
}

// WithKnowledgeRules sets the knowledge lifecycle rules the learning loop applies (HAR-119): the episode
// a candidate was learned from is recorded as its first decision evidence. Without them a candidate
// still seeds; a backtest records the evidence when it runs.
func WithKnowledgeRules(r knowledge.Rules) Option {
	return func(s *Service) { s.rules = &r }
}

// New builds the service. A nil logger discards logs.
func New(db *sql.DB, log *slog.Logger, opts ...Option) (*Service, error) {
	if db == nil {
		return nil, errors.New("strategystore: a database is required")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &Service{db: db, log: log, params: evalinput.Defaults("")}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// querier is the read side of *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const candidateJSON = `jsonb_build_object(
  'candidate_id', c.id, 'strategy_type', c.strategy_type, 'title', c.title, 'description', c.description,
  'ranking', c.ranking, 'preferred_by_agent', c.preferred_by_agent, 'rationale', c.rationale,
  'state_refs', to_jsonb(c.state_refs), 'evidence_refs', c.evidence_refs, 'knowledge_refs', to_jsonb(c.knowledge_refs),
  'action_type', c.action_type, 'action_class', c.action_class, 'five_questions', c.five_questions, 'selected_eval_suite', c.selected_eval_suite, 'to', c.to_recipients, 'cc', c.cc_recipients, 'subject', c.subject,
  'full_action_artifact', c.full_action_artifact, 'preview', c.preview, 'draft_index', c.draft_index,
  'eval_bundle_ref', c.eval_bundle_id)`

const strategySetSQL = `
SELECT jsonb_build_object(
  'id', s.id, 'decision_episode_id', s.decision_episode_id, 'agent_run_id', s.agent_run_id, 'account_id', s.account_id,
  'opportunity_id', s.opportunity_id, 'generated_at', s.generated_at, 'state_ref', s.state_ref,
  'state_diff_id', s.state_diff_id, 'trigger_activity_ids', to_jsonb(s.trigger_activity_ids),
  'decision_guidance_id', s.decision_guidance_id, 'no_acceptable_candidate', s.no_acceptable_candidate,
  'candidates', (SELECT jsonb_agg(` + candidateJSON + ` ORDER BY c.ranking) FROM strategy_candidates c WHERE c.strategy_set_id = s.id))::text,
 s.id::text
FROM strategy_sets s WHERE s.agent_run_id = $1::uuid`

const bundlesSQL = `
SELECT jsonb_build_object('id', b.id, 'agent_run_id', b.agent_run_id, 'draft_index', b.draft_index,
  'strategy_candidate_id', c.id, 'items', b.items, 'generated_at', b.generated_at,
  'selected_eval_suite', b.selected_eval_suite, 'candidate_policy', b.candidate_policy)::text
FROM strategy_candidates c JOIN eval_bundles b ON b.id = c.eval_bundle_id
WHERE c.strategy_set_id = $1::uuid ORDER BY c.ranking`

// Strategies returns {strategy_set, eval_bundles} of the run exactly as stored (GET /runs/{run_id}/strategies).
func (s *Service) Strategies(ctx context.Context, runID string) ([]byte, error) {
	if !IsUUID(runID) {
		return nil, &NotFoundError{Code: "not_found"}
	}
	var set, setID string
	err := s.db.QueryRowContext(ctx, strategySetSQL, runID).Scan(&set, &setID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.runMissing(ctx, runID, CodeNotReady)
	}
	if err != nil {
		return nil, fmt.Errorf("strategystore: read strategy set of run %s: %w", runID, err)
	}
	rows, err := s.db.QueryContext(ctx, bundlesSQL, setID)
	if err != nil {
		return nil, fmt.Errorf("strategystore: read eval bundles of run %s: %w", runID, err)
	}
	defer rows.Close()
	var bundles []json.RawMessage
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, fmt.Errorf("strategystore: scan eval bundle: %w", err)
		}
		bundles = append(bundles, json.RawMessage(b))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("strategystore: read eval bundles: %w", err)
	}
	if len(bundles) != 3 { // a set is only served whole: the demo path is exactly 3 candidates with 3 bundles
		return nil, &NotReadyError{Code: CodeNotReady}
	}
	return json.Marshal(map[string]any{"strategy_set": json.RawMessage(set), "eval_bundles": bundles})
}

// runMissing is not_found when the run does not exist and notReady when it does but has no object yet.
func (s *Service) runMissing(ctx context.Context, runID, notReady string) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agent_runs WHERE id = $1::uuid)`, runID).Scan(&exists); err != nil {
		return fmt.Errorf("strategystore: check run %s: %w", runID, err)
	}
	if !exists {
		return &NotFoundError{Code: "not_found"}
	}
	return &NotReadyError{Code: notReady}
}

const biSQL = `
SELECT jsonb_build_object('id', id, 'account_id', account_id, 'opportunity_id', opportunity_id,
  'account_change_id', account_change_id, 'summary', summary, 'claims', claims, 'why_it_matters', why_it_matters,
  'knowledge_refs', to_jsonb(knowledge_refs), 'account_map_ref', account_map_ref, 'transition', transition, 'model', model,
  'created_at', created_at)::text
FROM business_intelligence_updates WHERE account_id = $1::uuid ORDER BY created_at DESC, id LIMIT 1`

// LatestBusinessIntelligence returns the account's newest update (GET /accounts/{id}/business-intelligence/latest).
func (s *Service) LatestBusinessIntelligence(ctx context.Context, accountID string) ([]byte, error) {
	if !IsUUID(accountID) {
		return nil, &NotFoundError{Code: "not_found"}
	}
	var doc string
	err := s.db.QueryRowContext(ctx, biSQL, accountID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1::uuid)`, accountID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("strategystore: check account %s: %w", accountID, err)
		}
		if !exists {
			return nil, &NotFoundError{Code: "not_found"}
		}
		return nil, &NotFoundError{Code: CodeNoUpdate}
	}
	if err != nil {
		return nil, fmt.Errorf("strategystore: read business intelligence of %s: %w", accountID, err)
	}
	return []byte(doc), nil
}

const decisionSelect = `
SELECT jsonb_build_object('id', h.id, 'decision_episode_id', h.decision_episode_id, 'agent_run_id', h.agent_run_id,
  'strategy_set_id', h.strategy_set_id, 'selected_candidate_id', h.selected_candidate_id,
  'original_agent_preference', h.original_agent_preference, 'surface', h.surface, 'actor_person_id', h.actor_person_id,
  'actor_label', h.actor_label, 'chosen_at', h.chosen_at, 'final_to', h.final_to, 'final_cc', h.final_cc,
  'final_artifact', h.final_artifact, 'edits', h.edits, 'send_decision', h.send_decision,
  'send_decided_at', h.send_decided_at, 'human_decision_id', h.human_decision_id)::text
FROM human_strategy_decisions h WHERE h.agent_run_id = $1::uuid`

// Decision returns the run's HumanStrategyDecision (GET /runs/{run_id}/strategy-decision).
func (s *Service) Decision(ctx context.Context, runID string) ([]byte, error) {
	if !IsUUID(runID) {
		return nil, &NotFoundError{Code: "not_found"}
	}
	doc, err := readDecision(ctx, s.db, runID)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM agent_runs WHERE id = $1::uuid)`, runID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("strategystore: check run %s: %w", runID, err)
		}
		if !exists {
			return nil, &NotFoundError{Code: "not_found"}
		}
		return nil, &NotFoundError{Code: CodeNoDecision}
	}
	return doc, err
}

func readDecision(ctx context.Context, q querier, runID string) ([]byte, error) {
	var doc string
	if err := q.QueryRowContext(ctx, decisionSelect, runID).Scan(&doc); err != nil {
		return nil, err
	}
	return []byte(doc), nil
}

const inferenceSelect = `
SELECT jsonb_build_object('id', j.id, 'decision_episode_id', j.decision_episode_id,
  'human_strategy_decision_id', j.human_strategy_decision_id, 'agent_preference', j.agent_preference,
  'human_choice', j.human_choice, 'agreement', j.agreement,
  'inferred_semantic_delta', jsonb_build_object('statement', j.inferred_statement, 'semantic_labels', to_jsonb(j.semantic_labels)),
  'evidence', j.evidence, 'human_verdict', j.human_verdict, 'corrected_statement', j.corrected_statement,
  'human_note', j.human_note, 'verdict_surface', j.verdict_surface, 'verdict_actor_label', j.verdict_actor_label,
  'verdict_at', j.verdict_at, 'model', j.model, 'generated_at', j.generated_at)::text
FROM judgment_inferences j WHERE j.decision_episode_id = $1::uuid`

// Inference returns the episode's JudgmentInference (GET /episodes/{episode_id}/judgment-inference).
func (s *Service) Inference(ctx context.Context, episodeID string) ([]byte, error) {
	if !IsUUID(episodeID) {
		return nil, &NotFoundError{Code: "not_found"}
	}
	doc, err := readInference(ctx, s.db, episodeID)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM decision_episodes WHERE id = $1::uuid)`, episodeID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("strategystore: check episode %s: %w", episodeID, err)
		}
		if !exists {
			return nil, &NotFoundError{Code: "not_found"}
		}
		return nil, &NotReadyError{Code: CodeInferenceWait}
	}
	return doc, err
}

func readInference(ctx context.Context, q querier, episodeID string) ([]byte, error) {
	var doc string
	if err := q.QueryRowContext(ctx, inferenceSelect, episodeID).Scan(&doc); err != nil {
		return nil, err
	}
	return []byte(doc), nil
}
