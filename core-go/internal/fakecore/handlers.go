package fakecore

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

// fixtureHumanDecisionID is the id of the HumanDecision the record-only executor "writes" at send.
const fixtureHumanDecisionID = "0dec0000-0000-4000-8000-000000000701"

func notFound() answer { return fail(http.StatusNotFound, "not_found", "not found") }

func (s *Server) getAccount(r *http.Request, _ []byte) answer {
	if r.PathValue("account_id") != slacksurface.FixtureAccountID {
		return notFound()
	}
	return ok(map[string]string{"id": slacksurface.FixtureAccountID, "name": s.fx.AccountName})
}

// getGraph serves the fixture's people as person nodes, which is all the Slack surface reads of the graph.
func (s *Server) getGraph(r *http.Request, _ []byte) answer {
	if r.PathValue("account_id") != slacksurface.FixtureAccountID {
		return notFound()
	}
	ids := make([]string, 0, len(s.fx.Directory))
	for id := range s.fx.Directory {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	nodes := []map[string]any{}
	for _, id := range ids {
		p := s.fx.Directory[id]
		nodes = append(nodes, map[string]any{"id": p.ID, "type": "person", "label": p.Name, "data": map[string]any{"email": p.Email}})
	}
	return ok(map[string]any{"nodes": nodes, "edges": []any{}})
}

// getState serves the contract account state example for the fixture's account: People reads the buying group of
// GET /accounts/{id}/state beside the graph, so a core without the route made every Slack action fail to refresh its message.
func (s *Server) getState(r *http.Request, _ []byte) answer {
	if r.PathValue("account_id") != slacksurface.FixtureAccountID {
		return notFound()
	}
	st, err := slacksurface.FixtureAccountState()
	if err != nil {
		return fail(http.StatusInternalServerError, "internal", "the fixture account state cannot be loaded")
	}
	return ok(st)
}

func (s *Server) getBI(r *http.Request, _ []byte) answer {
	if r.PathValue("account_id") != slacksurface.FixtureAccountID {
		return notFound()
	}
	return ok(json.RawMessage(s.docs["business_intelligence_update"]))
}

func (s *Server) getStrategies(r *http.Request, _ []byte) answer {
	if r.PathValue("run_id") != slacksurface.FixtureRunID {
		return notFound()
	}
	bundles := []json.RawMessage{}
	for _, c := range s.fx.Strategies.StrategySet.Candidates {
		bundles = append(bundles, s.docs["eval_bundle:"+c.CandidateID])
	}
	return ok(map[string]any{"strategy_set": json.RawMessage(s.docs["strategy_set"]), "eval_bundles": bundles})
}

func (s *Server) getDecision(r *http.Request, _ []byte) answer {
	switch {
	case r.PathValue("run_id") != slacksurface.FixtureRunID:
		return notFound()
	case s.decision == nil:
		return fail(http.StatusNotFound, strategystore.CodeNoDecision, "no strategy has been chosen yet")
	}
	return ok(s.decision)
}

func (s *Server) postDecision(r *http.Request, body []byte) answer {
	if r.PathValue("run_id") != slacksurface.FixtureRunID {
		return notFound()
	}
	var req strategystore.DecisionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fail(http.StatusBadRequest, "invalid_request", "the request body is not a decision request")
	}
	if err := req.Validate(); err != nil {
		return fail(http.StatusUnprocessableEntity, "invalid_request", err.Error())
	}
	chosen, found := s.fx.Strategies.Candidate(req.SelectedCandidateID)
	if !found {
		return fail(http.StatusUnprocessableEntity, "unknown_candidate", "the candidate is not in this run's strategy set")
	}
	base := draftOf(chosen)
	if s.decision != nil {
		switch {
		case s.decision["send_decision"] != slacksurface.SendPending:
			return answer{status: http.StatusConflict, code: strategystore.CodeAlreadyDecided, body: s.decision}
		case s.decision["selected_candidate_id"] != chosen.CandidateID:
			return fail(http.StatusConflict, strategystore.CodeSelectionLocked, "a candidate was already chosen; the selection cannot change")
		case !req.HasEdits():
			return ok(s.decision) // the same choice again: unchanged
		}
		base = s.currentDraft(base)
	}
	final, err := s.overlay(base, req)
	if err != nil {
		return fail(http.StatusUnprocessableEntity, "unknown_recipient", err.Error())
	}
	created := s.decision == nil
	if created {
		s.decision = s.newDecision(chosen, req)
	}
	if req.HasEdits() {
		s.decision["final_to"], s.decision["final_cc"], s.decision["final_artifact"] = final.To, final.CC, final.Artifact
		s.decision["edits"] = strategystore.ComputeEdits(draftOf(chosen), final)
	}
	if created {
		return answer{status: http.StatusCreated, body: s.decision}
	}
	return ok(s.decision)
}

func (s *Server) newDecision(c slacksurface.StrategyCandidate, req strategystore.DecisionRequest) map[string]any {
	var d map[string]any
	_ = json.Unmarshal(s.docs["human_strategy_decision:chosen"], &d)
	d["selected_candidate_id"], d["original_agent_preference"] = c.CandidateID, s.preferredID()
	d["surface"], d["actor_label"], d["actor_person_id"] = req.Surface, req.ActorLabel, req.ActorPersonID
	d["chosen_at"] = s.now().UTC().Format("2006-01-02T15:04:05Z")
	return d
}

func (s *Server) preferredID() string {
	for _, c := range s.fx.Strategies.StrategySet.Candidates {
		if c.PreferredByAgent {
			return c.CandidateID
		}
	}
	return ""
}

func (s *Server) postSend(r *http.Request, body []byte) answer {
	if r.PathValue("run_id") != slacksurface.FixtureRunID {
		return notFound()
	}
	var req strategystore.SendRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fail(http.StatusBadRequest, "invalid_request", "the request body is not a send request")
	}
	if err := req.Validate(); err != nil {
		return fail(http.StatusUnprocessableEntity, "invalid_request", err.Error())
	}
	switch {
	case s.decision == nil:
		return fail(http.StatusConflict, strategystore.CodeNoChoice, "no strategy has been chosen yet")
	case s.decision["send_decision"] != slacksurface.SendPending:
		return answer{status: http.StatusConflict, code: strategystore.CodeAlreadyDecided, body: s.decision}
	}
	chosen, _ := s.fx.Strategies.Candidate(s.decision["selected_candidate_id"].(string))
	if req.Decision == "send" && s.blocked(chosen) {
		return fail(http.StatusUnprocessableEntity, "blocking_eval", "a blocking eval of this candidate failed; the action cannot be sent until it is resolved")
	}
	a := ok(nil)
	if req.Decision == "send" {
		final := s.currentDraft(draftOf(chosen))
		s.decision["final_to"], s.decision["final_cc"], s.decision["final_artifact"] = final.To, final.CC, final.Artifact
		a.effect = &Effect{Kind: chosen.ActionType, IdempotencyKey: slacksurface.FixtureRunID + ":execute", Subject: subjectOf(final), To: personIDs(final.To)}
	}
	s.decision["send_decision"] = req.Decision
	s.decision["send_decided_at"] = s.now().UTC().Format("2006-01-02T15:04:05Z")
	s.decision["human_decision_id"] = fixtureHumanDecisionID
	a.body = s.decision
	return a
}

// blocked reports a failed eval marked blocking in the candidate's bundle.
func (s *Server) blocked(c slacksurface.StrategyCandidate) bool {
	for _, b := range s.fx.Strategies.EvalBundles {
		if b.StrategyCandidateID != c.CandidateID {
			continue
		}
		for _, it := range b.Items {
			if it.Verdict == slacksurface.VerdictFail && it.Result != nil && it.Result.Blocking {
				return true
			}
		}
	}
	return false
}
