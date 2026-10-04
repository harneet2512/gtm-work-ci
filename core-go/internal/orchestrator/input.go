package orchestrator

import (
	"context"

	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/evalinput"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// evalInput assembles deterministic_eval_input.v1.json for one candidate version (ADR-0014) through the shared
// evalinput assembly — the same one the send-time re-evaluation uses (HAR-139) — at the run's replay clock
// (world.EventTime, never the wall clock, I3) against the state the world read.
func (s *Service) evalInput(ctx context.Context, run runRow, w world, c workerclient.Candidate, draftIndex int) (deterministic.Input, error) {
	in, err := evalinput.Assemble(ctx, s.db,
		evalinput.Run{ID: run.ID, AccountID: run.AccountID, Mode: run.Mode, OpportunityID: run.OpportunityID,
			TriggerIDs: run.TriggerIDs},
		draftOutput(c), draftIndex, w.EventTime, w.State,
		evalinput.Params{WorkspaceID: s.cfg.WorkspaceID, Policy: s.cfg.Policy, CRM: s.cfg.CRM})
	if err != nil {
		return in, transient("evaluate", err)
	}
	return in, nil
}

// draftOutput is the deterministic draft of a worker candidate: its artifact, recipients, evidence and intent,
// assembled through the shared evalinput.Output skeleton the send-time re-evaluation uses (HAR-139).
func draftOutput(c workerclient.Candidate) deterministic.Output {
	p := evalinput.OutputParts{
		ActionType: c.ActionType, Intent: c.Description, Reason: c.Rationale, Knowledge: c.KnowledgeRefs,
		Artifact: evalinput.Artifact{Channel: c.FullActionArtifact.Channel, Subject: c.FullActionArtifact.Subject,
			Body: c.FullActionArtifact.Body, Attachments: c.FullActionArtifact.Attachments},
	}
	for _, r := range append(append([]workerclient.Recipient{}, c.To...), c.CC...) {
		p.Recipients = append(p.Recipients, evalinput.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why})
	}
	for _, e := range c.EvidenceRefs {
		p.Evidence = append(p.Evidence, deterministic.EvidenceRef{ActivityID: e.ActivityID, ClaimID: e.ClaimID,
			Quote: e.Quote, SpeakerPersonID: e.SpeakerPersonID, OccurredAt: e.OccurredAt})
	}
	return evalinput.Output(p)
}
