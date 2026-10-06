package evalinput

import "github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"

// Recipient is a person the proposed action reaches; caller-side recipient types map onto it.
type Recipient struct {
	PersonID, Role, Why string
}

// Artifact is the finished artifact under evaluation.
type Artifact struct {
	Channel     string
	Subject     *string
	Body        string
	Attachments []string
}

// OutputParts are the caller-side fields a deterministic Output is assembled from: one shared assembly
// serves candidate evaluation (the orchestrator's worker candidates) and send-time re-evaluation (the
// strategy store's human-edited final artifact), so the two judgments stay in step (HAR-139).
type OutputParts struct {
	ActionType string
	Artifact   Artifact
	// Intent is the CRM next-step the draft argues for.
	Intent string
	// Reason is the draft's stated rationale.
	Reason    string
	Knowledge []string
	// Recipients is to- and cc-recipients together; order is preserved.
	Recipients []Recipient
	Evidence   []deterministic.EvidenceRef
}

// Output assembles the deterministic Output of a draft under evaluation: the artifact, recipients,
// evidence, intent and knowledge, with the contract's empty-slice guards so a draft missing a section
// evaluates the same as one carrying an empty section.
func Output(p OutputParts) deterministic.Output {
	out := deterministic.Output{
		ProposedActionType: p.ActionType,
		FinishedArtifact: deterministic.Artifact{Channel: p.Artifact.Channel, Subject: p.Artifact.Subject,
			Body: p.Artifact.Body, Attachments: p.Artifact.Attachments},
		CRMNextStepIntent: deterministic.CRMIntent{NextStep: p.Intent}, Reason: p.Reason,
		KnowledgeRefsUsed: p.Knowledge,
	}
	for _, r := range p.Recipients {
		out.Recipients = append(out.Recipients, deterministic.Recipient{PersonID: r.PersonID, Role: r.Role, Why: r.Why})
	}
	out.EvidenceRefs = append(out.EvidenceRefs, p.Evidence...)
	if out.Recipients == nil {
		out.Recipients = []deterministic.Recipient{}
	}
	if out.EvidenceRefs == nil {
		out.EvidenceRefs = []deterministic.EvidenceRef{}
	}
	if out.KnowledgeRefsUsed == nil {
		out.KnowledgeRefsUsed = []string{}
	}
	return out
}
