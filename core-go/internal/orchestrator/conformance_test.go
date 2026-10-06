package orchestrator_test

import (
	"encoding/json"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// TestEveryPersistedObjectConformsToItsContract: what the orchestrator writes is what the contract schemas
// describe (DecisionGuidance, AgentRunDraft for the generator and for the revision planner, StrategyCandidate as
// the worker sends it, the episode with its transition).
func TestEveryPersistedObjectConformsToItsContract(t *testing.T) {
	sc := newScene(t)
	fw := newFake(sc)
	blockWhen(fw, func(r workerclient.JudgeRequest) bool {
		return r.Candidate.StrategyType == "bring_in_technical_lead" && r.DraftIndex == 2
	})
	revisedBody(fw)
	out := mustRun(t, service(t, fw), sc.RunID)

	validDoc(t, "decision_guidance", []byte(scalar(t, `SELECT guidance::text FROM decision_guidance WHERE agent_run_id = $1::uuid`, sc.RunID)))
	for _, doc := range col(t, `SELECT jsonb_build_object('agent_run_id', agent_run_id, 'draft_index', draft_index, 'source', source, 'output', output,
 'decision', decision, 'revision_feedback', revision_feedback, 'model', model, 'created_at', created_at)::text FROM agent_run_drafts
 WHERE agent_run_id = $1::uuid ORDER BY draft_index`, sc.RunID) {
		validDoc(t, "agent_run_draft", []byte(doc))
	}
	for _, c := range fw.threeCandidates() {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		validDoc(t, "strategy_candidate", raw)
	}
	validEpisode(t, out.EpisodeID, sc)
	for _, doc := range col(t, `SELECT jsonb_build_object('id', id, 'agent_run_id', agent_run_id, 'draft_index', draft_index,
 'strategy_candidate_id', (SELECT id FROM strategy_candidates WHERE eval_bundle_id = b.id), 'items', items, 'generated_at', generated_at,
 'selected_eval_suite', selected_eval_suite, 'candidate_policy', candidate_policy)::text FROM eval_bundles b
 WHERE agent_run_id = $1::uuid AND EXISTS (SELECT 1 FROM strategy_candidates WHERE eval_bundle_id = b.id)`, sc.RunID) {
		validDoc(t, "eval_bundle", []byte(doc))
	}
}
