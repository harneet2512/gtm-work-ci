package demosmoke

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// replayJudge stands in for the worker's /v1/decision-judge in the real-core smoke (no worker, no network): it
// answers every judge request from the evidence ids it was offered. It cites only real ids from the request and
// asserts nothing the request does not show, so the smoke exercises the production gate path (runner, ranking
// persistence, R1 resolution, storage) while the worker's own tests cover the model side. Its model name says so.
type replayJudge struct{}

var replayDimensions = map[string][]string{
	"intent_fit":        {"intent_follows_state", "timing", "relationship_supports", "uncertainty_handled", "knowledge_used_correctly", "quiet_move_considered"},
	"set_quality":       {"materially_different", "coverage", "no_dominated"},
	"candidate_quality": {"fit", "grounding", "intent_coherence", "recipients", "timing", "cta", "factual_integrity", "knowledge_use", "unsupported_assumptions", "risk"},
	"ranking":           {"supported_by_evidence_state", "supported_by_knowledge", "uncertainty_reflected", "no_blocked_preferred", "rationale_matches_basis"},
	"final_artifact":    {"intent_preserved", "claims_grounded", "cta_timing_correct"},
}

// DecisionJudge implements bucket2run.Judge.
func (replayJudge) DecisionJudge(_ context.Context, req workerclient.DecisionJudgeRequest) (workerclient.DecisionJudgeResponse, error) {
	resp := workerclient.DecisionJudgeResponse{Kind: req.Kind, SubjectID: req.SubjectID, Model: "smoke-replay", PromptVersion: "smoke:v1"}
	if len(req.EvidenceIDs) == 0 {
		return resp, fmt.Errorf("replay judge: %s offered no evidence ids", req.Kind)
	}
	cite := req.EvidenceIDs[:1]
	if req.Kind == "rank_rationale" {
		var p struct {
			Order []string `json:"order"`
		}
		if err := json.Unmarshal(req.Payload, &p); err != nil {
			return resp, err
		}
		for i := 0; i+1 < len(p.Order); i++ {
			resp.PairwiseReasons = append(resp.PairwiseReasons, workerclient.PairReason{RankedHigherID: p.Order[i], RankedLowerID: p.Order[i+1],
				Reason: "ranked by the worker's order after the policy tiers", EvidenceRefs: cite, KnowledgeRefs: []string{}})
		}
		return resp, nil
	}
	for _, d := range replayDimensions[req.Kind] {
		resp.Dimensions = append(resp.Dimensions, workerclient.DimensionVerdict{Dimension: d, Verdict: "pass",
			Why: "smoke replay: judged from the offered evidence", EvidenceRefs: cite})
	}
	if req.Kind == "intent_fit" {
		reaction, quiet := "ACT", false
		resp.RightReaction, resp.QuietMoveConsidered = &reaction, &quiet
	}
	return resp, nil
}

// checkBucket2 proves the gates ran inside the real path: after the whole Slack flow every Bucket 2 gate D1 to D10
// has a stored result, every verdict other than unknown rests on evidence that resolves, and every unknown says why.
func (r *RealCore) checkBucket2() error {
	r.strategies.WaitGates()
	stored, err := bucket2.Load(context.Background(), r.db, r.Seed.EpisodeID)
	if err != nil {
		return fmt.Errorf("demosmoke: read the Bucket 2 results: %w", err)
	}
	seen := map[string]int{}
	for _, s := range stored {
		seen[s.Gate]++
		if s.Verdict != bucket2.Unknown && len(s.EvidenceRefs) == 0 {
			return fmt.Errorf("demosmoke: %s %s says %s with no evidence", s.Gate, s.SubGate, s.Verdict)
		}
		if s.Verdict == bucket2.Unknown && s.Why == "" {
			return fmt.Errorf("demosmoke: %s %s is unknown without a reason", s.Gate, s.SubGate)
		}
		resolved, err := bucket2.ResolveRefs(context.Background(), r.db, s.EvidenceRefs)
		if err != nil {
			return err
		}
		if len(resolved) != len(s.EvidenceRefs) {
			return fmt.Errorf("demosmoke: %s %s cites refs that do not resolve: %v", s.Gate, s.SubGate, s.EvidenceRefs)
		}
	}
	for _, g := range bucket2.Gates {
		if seen[g] == 0 {
			return fmt.Errorf("demosmoke: gate %s has no stored result after the real flow (not measured)", g)
		}
	}
	return nil
}
