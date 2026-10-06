package fakecore

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

func recipientsOf(rs []slacksurface.Recipient) []strategystore.Recipient {
	out := []strategystore.Recipient{}
	for _, r := range rs {
		out = append(out, strategystore.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why})
	}
	return out
}

// draftOf is the candidate's own draft: who it goes to and what it says.
func draftOf(c slacksurface.StrategyCandidate) strategystore.Draft {
	return strategystore.Draft{
		To: recipientsOf(c.To), CC: recipientsOf(c.CC),
		Artifact: strategystore.Artifact{Channel: c.FullActionArtifact.Channel, Subject: c.FullActionArtifact.Subject,
			Body: c.FullActionArtifact.Body, Attachments: c.FullActionArtifact.Attachments},
	}
}

// currentDraft is the saved edit when the human has made one, else base.
func (s *Server) currentDraft(base strategystore.Draft) strategystore.Draft {
	art, edited := s.decision["final_artifact"].(strategystore.Artifact)
	if !edited {
		return base
	}
	to, _ := s.decision["final_to"].([]strategystore.Recipient)
	cc, _ := s.decision["final_cc"].([]strategystore.Recipient)
	return strategystore.Draft{To: to, CC: cc, Artifact: art}
}

// overlay replaces what the request carries and checks every recipient is a person of the fixture account.
func (s *Server) overlay(base strategystore.Draft, req strategystore.DecisionRequest) (strategystore.Draft, error) {
	out := base
	if req.FinalTo != nil {
		out.To = *req.FinalTo
	}
	if req.FinalCC != nil {
		out.CC = *req.FinalCC
	}
	if req.FinalArtifact != nil {
		out.Artifact = *req.FinalArtifact
	}
	for _, r := range append(append([]strategystore.Recipient{}, out.To...), out.CC...) {
		if _, known := s.fx.Directory[r.PersonID]; !known {
			return out, errors.New("a recipient is not a person of this account")
		}
	}
	return out, nil
}

func subjectOf(d strategystore.Draft) string {
	if d.Artifact.Subject == nil {
		return ""
	}
	return *d.Artifact.Subject
}

func personIDs(rs []strategystore.Recipient) []string {
	ids := []string{}
	for _, r := range rs {
		ids = append(ids, r.PersonID)
	}
	return ids
}

// currentInference builds the inference for the human's actual choice from the fixture's pending inference.
func (s *Server) currentInference() map[string]any {
	if s.inference != nil {
		return s.inference
	}
	var inf map[string]any
	_ = json.Unmarshal(s.docs["judgment_inference:pending"], &inf)
	chosen, _ := s.decision["selected_candidate_id"].(string)
	preferred, _ := s.decision["original_agent_preference"].(string)
	inf["human_strategy_decision_id"], inf["agent_preference"], inf["human_choice"] = s.decision["id"], preferred, chosen
	if chosen == preferred {
		inf["agreement"] = slacksurface.AgreementAgreed
		inf["inferred_semantic_delta"] = map[string]any{"statement": "The human chose the candidate gtm_ai preferred, so gtm_ai's judgment of this account state is confirmed.", "semantic_labels": []string{}}
		if ev, ok := inf["evidence"].(map[string]any); ok {
			ev["candidate_differences"], ev["eval_differences"] = []string{}, []any{}
		}
	} else {
		inf["agreement"] = slacksurface.AgreementOverrode
	}
	s.inference = inf
	return inf
}

func (s *Server) getInference(r *http.Request, _ []byte) answer {
	switch {
	case r.PathValue("episode_id") != slacksurface.FixtureEpisodeID:
		return notFound()
	case s.decision == nil || s.decision["send_decision"] != slacksurface.SendSend:
		return fail(http.StatusNotFound, strategystore.CodeInferenceWait, "the inference is generated after the send")
	}
	return ok(s.currentInference())
}

func (s *Server) postVerdict(r *http.Request, body []byte) answer {
	if r.PathValue("episode_id") != slacksurface.FixtureEpisodeID {
		return notFound()
	}
	var req strategystore.VerdictRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fail(http.StatusBadRequest, "invalid_request", "the request body is not a verdict request")
	}
	if err := req.Validate(); err != nil {
		return fail(http.StatusUnprocessableEntity, "invalid_request", err.Error())
	}
	if s.decision == nil || s.decision["send_decision"] != slacksurface.SendSend {
		return fail(http.StatusNotFound, strategystore.CodeInferenceWait, "the inference is generated after the send")
	}
	inf := s.currentInference()
	if req.Verdict == "confirmed" && inf["human_verdict"] == slacksurface.VerdictCorrected {
		return fail(http.StatusUnprocessableEntity, "verdict_locked", "a corrected inference cannot be confirmed; correct it again instead")
	}
	if cur := inf["human_verdict"]; (cur == slacksurface.VerdictNoLearning && (req.Verdict == "confirmed" || req.Verdict == "corrected")) ||
		(cur == slacksurface.VerdictCorrected && req.Verdict == slacksurface.VerdictNoLearning) {
		return fail(http.StatusUnprocessableEntity, "verdict_locked", "learning was declined for this episode, or a correction already fed it")
	}
	if req.Verdict != "" && !(req.Verdict == inf["human_verdict"] && req.Verdict != slacksurface.VerdictCorrected) {
		inf["human_verdict"], inf["verdict_surface"], inf["verdict_actor_label"] = req.Verdict, req.Surface, req.ActorLabel
		inf["verdict_at"] = s.now().UTC().Format("2006-01-02T15:04:05Z")
		inf["corrected_statement"] = nil
		if req.Verdict == slacksurface.VerdictCorrected {
			inf["corrected_statement"] = req.CorrectedStatement
		}
	}
	if req.Note != "" {
		inf["human_note"] = req.Note
	}
	return ok(inf)
}
