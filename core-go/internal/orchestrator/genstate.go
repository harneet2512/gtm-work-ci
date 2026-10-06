package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// The draft step's detail holds what a resume must not redo: the validated candidates ("generated", with the
// counterfactual generation when one was made) and each candidate's evaluated versions ("evaluated", keyed by
// candidate id). Every model call whose answer is stored here is made at most once per run, whatever fails later.

// generated is the validated worker answer.
type generated struct {
	Candidates []workerclient.Candidate `json:"candidates"`
	Model      string                   `json:"model"`
	// Regenerated: the transition policy found no allowed candidate in the first answer and the run asked once more;
	// this answer is the one it kept, so a resume never asks a third time.
	Regenerated    bool            `json:"regenerated,omitempty"`
	Counterfactual *counterfactual `json:"counterfactual,omitempty"`
}

// counterfactual is arm A of E7: the same state generated with the DecisionGuidance withheld.
type counterfactual struct {
	Status     string                   `json:"status"` // generated | unavailable | no_knowledge
	Reason     string                   `json:"reason,omitempty"`
	Candidates []workerclient.Candidate `json:"candidates,omitempty"`
}

// Counterfactual statuses as stored (knowledgeinfluence maps them to its trace statuses).
const (
	cfGenerated   = "generated"
	cfUnavailable = "unavailable"
	cfNoKnowledge = "no_knowledge"
)

// saveGenerated stores the validated candidates. It clears the evaluated versions: they belong to the candidates
// they were made for, and a regeneration replaces those.
func (s *Service) saveGenerated(ctx context.Context, runID string, gen *generated) error {
	raw, err := json.Marshal(gen)
	if err != nil {
		return permanent("generate", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_run_steps SET detail = detail || jsonb_build_object('generated', $2::jsonb, 'evaluated', '{}'::jsonb)
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID, string(raw)); err != nil {
		return transient("generate", fmt.Errorf("store the generated candidates: %w", err))
	}
	return nil
}

func (s *Service) saveCounterfactual(ctx context.Context, runID string, cf *counterfactual) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_run_steps SET detail = jsonb_set(detail, '{generated,counterfactual}', $2::jsonb)
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID, string(mustJSON(cf))); err != nil {
		return transient("generate", fmt.Errorf("store the counterfactual generation: %w", err))
	}
	return nil
}

func (s *Service) loadGenerated(ctx context.Context, runID string) (*generated, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT detail -> 'generated' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID).Scan(&raw)
	if err != nil {
		return nil, transient("generate", fmt.Errorf("read the generated candidates: %w", err))
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var gen generated
	if err := json.Unmarshal(raw, &gen); err != nil {
		return nil, permanent("generate", fmt.Errorf("decode the stored candidates: %w", err))
	}
	return &gen, nil
}

// saveEvaluated stores one candidate's evaluated versions (judge results and, when it was revised, Draft 2). Calls
// for different candidates run in parallel; each is one atomic update of the step's detail.
func (s *Service) saveEvaluated(ctx context.Context, runID string, e evaluated) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return permanent("evaluate", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_run_steps SET detail = detail || jsonb_build_object('evaluated',
 COALESCE(detail -> 'evaluated', '{}'::jsonb) || jsonb_build_object($2::text, $3::jsonb))
 WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID, e.Final.Candidate.CandidateID, string(raw)); err != nil {
		return transient("evaluate", fmt.Errorf("store the evaluated candidate: %w", err))
	}
	return nil
}

func (s *Service) loadEvaluated(ctx context.Context, runID string) (map[string]evaluated, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT detail -> 'evaluated' FROM agent_run_steps WHERE agent_run_id = $1::uuid AND step = 'draft'`, runID).Scan(&raw)
	if err != nil {
		return nil, transient("evaluate", fmt.Errorf("read the evaluated candidates: %w", err))
	}
	out := map[string]evaluated{}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, permanent("evaluate", fmt.Errorf("decode the stored evaluations: %w", err))
	}
	return out, nil
}
