// Package slacksurface is the Slack Socket Mode operating surface for the HAR-129 demo.
//
// It is a thin, transport-neutral adapter (HAR-129 section 15, contracts/slack/actions.md): it renders
// contract objects as Block Kit, opens modals, and forwards every action to the core API. It holds no
// business logic and no durable state; message coordinates come from each interaction payload.
//
// model.go holds the wire types. Their JSON property names are exactly those of contracts/schemas
// (business_intelligence_update, strategy_set, strategy_candidate, eval_bundle, human_strategy_decision,
// judgment_inference) and of the request bodies in contracts/openapi/core.yaml; model_test.go validates
// the fixtures built from them against those schemas. Only the properties this surface renders are decoded.
package slacksurface

import (
	"encoding/json"
	"time"
)

// Verdict is one eval verdict (eval_bundle.items[].verdict).
type Verdict string

const (
	VerdictPass        Verdict = "pass"
	VerdictWarn        Verdict = "warn"
	VerdictFail        Verdict = "fail"
	VerdictAbstain     Verdict = "abstain"
	VerdictNotRelevant Verdict = "not_relevant"
)

// UnmarshalJSON reads the current word for "the evidence cannot settle it", unknown, as the abstain this surface has
// always worded ("Unsure"): there is one such verdict on screen, whichever spelling the core sends.
func (v *Verdict) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "unknown" {
		s = string(VerdictAbstain)
	}
	*v = Verdict(s)
	return nil
}

// EvidenceRef is common.evidenceRef: a quote from an activity.
type EvidenceRef struct {
	ActivityID      string    `json:"activity_id"`
	ClaimID         string    `json:"claim_id,omitempty"`
	Quote           string    `json:"quote,omitempty"`
	SpeakerPersonID string    `json:"speaker_person_id,omitempty"`
	OccurredAt      time.Time `json:"occurred_at,omitempty"`
}

// Claim is one business-intelligence change claim with its trace.
type Claim struct {
	Statement      string        `json:"statement"`
	Dimension      string        `json:"dimension"`
	StateDiffField *string       `json:"state_diff_field,omitempty"`
	EvidenceRefs   []EvidenceRef `json:"evidence_refs"`
}

// AccountMapRef is what View account map opens.
type AccountMapRef struct {
	StateRef     json.RawMessage `json:"state_ref"`
	GraphDiffRef json.RawMessage `json:"graph_diff_ref"`
}

// MissingFact is one fact a relationship-state transition still lacks (business_intelligence_update transition.missing_facts).
type MissingFact struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Transition is the relationship-state transition Message 1 shows (business_intelligence_update.transition):
// the one the event touched, or the account's open one labelled unchanged (TouchedByEvent false).
type Transition struct {
	StateTransitionID string        `json:"state_transition_id"`
	Status            string        `json:"status"`
	FromState         string        `json:"from_state"`
	ToStateCandidate  *string       `json:"to_state_candidate"`
	MissingFacts      []MissingFact `json:"missing_facts"`
	TouchedByEvent    bool          `json:"touched_by_event"`
}

// BusinessIntelligenceUpdate is Message 1.
type BusinessIntelligenceUpdate struct {
	ID              string        `json:"id"`
	AccountID       string        `json:"account_id"`
	AccountChangeID string        `json:"account_change_id"`
	Summary         string        `json:"summary"`
	Claims          []Claim       `json:"claims"`
	WhyItMatters    string        `json:"why_it_matters"`
	KnowledgeRefs   []string      `json:"knowledge_refs"`
	AccountMapRef   AccountMapRef `json:"account_map_ref"`
	// Transition is required and nullable in the contract: nil means the account has no open transition.
	Transition *Transition `json:"transition"`
	CreatedAt  time.Time   `json:"created_at"`
}

// Recipient is agent_run_output.recipients[]: a person, not an address.
type Recipient struct {
	PersonID string `json:"person_id"`
	Role     string `json:"role"`
	Why      string `json:"why,omitempty"`
}

// Artifact is agent_run_output.finished_artifact.
type Artifact struct {
	Channel     string   `json:"channel"`
	Subject     *string  `json:"subject,omitempty"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments,omitempty"`
}

// SubjectText is the subject, or "" when there is none.
func (a Artifact) SubjectText() string {
	if a.Subject == nil {
		return ""
	}
	return *a.Subject
}

// StrategyCandidate is one complete candidate (strategy_candidate.v1.json). Everything needed for the
// card, the full-draft modal and the selected view exists before Message 2 is posted.
type StrategyCandidate struct {
	CandidateID        string        `json:"candidate_id"`
	StrategyType       string        `json:"strategy_type"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Ranking            int           `json:"ranking"`
	PreferredByAgent   bool          `json:"preferred_by_agent"`
	Rationale          string        `json:"rationale"`
	StateRefs          []string      `json:"state_refs"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs"`
	KnowledgeRefs      []string      `json:"knowledge_refs"`
	ActionType         string        `json:"action_type"`
	To                 []Recipient   `json:"to"`
	CC                 []Recipient   `json:"cc"`
	Subject            *string       `json:"subject"`
	FullActionArtifact Artifact      `json:"full_action_artifact"`
	Preview            string        `json:"preview"`
	DraftIndex         int           `json:"draft_index,omitempty"`
	EvalBundleRef      string        `json:"eval_bundle_ref,omitempty"`
}

// StrategySet is the three candidates of one decision episode (strategy_set.v1.json).
type StrategySet struct {
	ID                string    `json:"id"`
	DecisionEpisodeID string    `json:"decision_episode_id"`
	AgentRunID        string    `json:"agent_run_id"`
	AccountID         string    `json:"account_id"`
	GeneratedAt       time.Time `json:"generated_at"`
	// NoAcceptableCandidate: every candidate is blocked or restricted, so Ghost recommends none (HAR-128).
	NoAcceptableCandidate bool                `json:"no_acceptable_candidate"`
	Candidates            []StrategyCandidate `json:"candidates"`
}

// EvalResult is the part of eval_result.v1.json this surface shows.
type EvalResult struct {
	ID string `json:"id"`
	// EvidenceClass is how proven the eval is (deal_data, methodology, cs_ops, product_rule): a small tag.
	EvidenceClass       string  `json:"evidence_class"`
	Label               string  `json:"label"`
	Blocking            bool    `json:"blocking"`
	Reason              string  `json:"reason"`
	SuggestedCorrection *string `json:"suggested_correction,omitempty"`
}

// EvalItem is one selected eval of a bundle: why it is relevant, its verdict and result.
type EvalItem struct {
	EvalType        string      `json:"eval_type"`
	RelevanceReason string      `json:"relevance_reason"`
	Verdict         Verdict     `json:"verdict"`
	Result          *EvalResult `json:"result"`
}

// EvalBundle is the evals selected for one candidate (eval_bundle.v1.json).
type EvalBundle struct {
	ID                  string     `json:"id"`
	StrategyCandidateID string     `json:"strategy_candidate_id"`
	Items               []EvalItem `json:"items"`
}

// RunStrategies is the body of GET /runs/{run_id}/strategies.
type RunStrategies struct {
	StrategySet StrategySet  `json:"strategy_set"`
	EvalBundles []EvalBundle `json:"eval_bundles"`
}

// Candidate looks a candidate up by id.
func (r RunStrategies) Candidate(id string) (StrategyCandidate, bool) {
	for _, c := range r.StrategySet.Candidates {
		if c.CandidateID == id {
			return c, true
		}
	}
	return StrategyCandidate{}, false
}

// Bundle returns the eval bundle of a candidate (by eval_bundle_ref).
func (r RunStrategies) Bundle(c StrategyCandidate) EvalBundle {
	for _, b := range r.EvalBundles {
		if b.ID == c.EvalBundleRef || b.StrategyCandidateID == c.CandidateID {
			return b
		}
	}
	return EvalBundle{}
}

// Title returns a candidate's title, or the id when unknown.
func (r RunStrategies) Title(id string) string {
	if c, ok := r.Candidate(id); ok {
		return c.Title
	}
	return id
}

// Send decisions (human_strategy_decision.send_decision).
const (
	SendPending = "pending"
	SendSend    = "send"
	SendDiscard = "discard"
)

// HumanStrategyDecision is the human's choice, edits and send state (human_strategy_decision.v1.json).
type HumanStrategyDecision struct {
	ID                      string            `json:"id"`
	DecisionEpisodeID       string            `json:"decision_episode_id"`
	AgentRunID              string            `json:"agent_run_id"`
	StrategySetID           string            `json:"strategy_set_id"`
	SelectedCandidateID     string            `json:"selected_candidate_id"`
	OriginalAgentPreference string            `json:"original_agent_preference"`
	Surface                 string            `json:"surface"`
	ActorLabel              string            `json:"actor_label"`
	ChosenAt                time.Time         `json:"chosen_at"`
	FinalTo                 []Recipient       `json:"final_to,omitempty"`
	FinalCC                 []Recipient       `json:"final_cc,omitempty"`
	FinalArtifact           *Artifact         `json:"final_artifact,omitempty"`
	Edits                   []json.RawMessage `json:"edits"`
	SendDecision            string            `json:"send_decision"`
	SendDecidedAt           *time.Time        `json:"send_decided_at,omitempty"`
	HumanDecisionID         *string           `json:"human_decision_id,omitempty"`
}

// Surface values and the surface name this adapter reports.
const SurfaceSlack = "slack"

// StrategyDecisionRequest is the body of POST /runs/{run_id}/strategy-decision. The final_*
// fields are omitted for a plain choose and set when the human saves edits.
type StrategyDecisionRequest struct {
	SelectedCandidateID string       `json:"selected_candidate_id"`
	Surface             string       `json:"surface"`
	ActorLabel          string       `json:"actor_label"`
	FinalTo             *[]Recipient `json:"final_to,omitempty"`
	FinalCC             *[]Recipient `json:"final_cc,omitempty"`
	FinalArtifact       *Artifact    `json:"final_artifact,omitempty"`
}

// SendRequest is the body of POST /runs/{run_id}/send.
type SendRequest struct {
	Decision   string `json:"decision"`
	Surface    string `json:"surface"`
	ActorLabel string `json:"actor_label"`
}

// Inference verdicts and agreement values.
const (
	VerdictPending   = "pending"
	VerdictConfirmed = "confirmed"
	VerdictCorrected = "corrected"
	// VerdictNoLearning is the human's explicit opt-out ("Don't learn this"): core seeds nothing from the episode.
	VerdictNoLearning = "no_learning"
	AgreementAgreed   = "agreed"
	AgreementOverrode = "overrode"
)

// EvalDifference is how the two candidates' verdicts differed on one eval.
type EvalDifference struct {
	EvalType               string  `json:"eval_type"`
	AgentPreferenceVerdict Verdict `json:"agent_preference_verdict"`
	HumanChoiceVerdict     Verdict `json:"human_choice_verdict"`
	Note                   string  `json:"note,omitempty"`
}

// InferenceEvidence is "Why Ghost inferred this".
type InferenceEvidence struct {
	CandidateDifferences  []string         `json:"candidate_differences"`
	EvidenceRefs          []EvidenceRef    `json:"evidence_refs"`
	EvalDifferences       []EvalDifference `json:"eval_differences"`
	KnowledgeRefs         []string         `json:"knowledge_refs"`
	NoApplicableKnowledge bool             `json:"no_applicable_knowledge"`
}

// SemanticDelta is Ghost's inferred reason.
type SemanticDelta struct {
	Statement      string   `json:"statement"`
	SemanticLabels []string `json:"semantic_labels,omitempty"`
}

// JudgmentInference is Message 3 (judgment_inference.v1.json).
type JudgmentInference struct {
	ID                      string            `json:"id"`
	DecisionEpisodeID       string            `json:"decision_episode_id"`
	HumanStrategyDecisionID string            `json:"human_strategy_decision_id"`
	AgentPreference         string            `json:"agent_preference"`
	HumanChoice             string            `json:"human_choice"`
	Agreement               string            `json:"agreement"`
	InferredSemanticDelta   SemanticDelta     `json:"inferred_semantic_delta"`
	Evidence                InferenceEvidence `json:"evidence"`
	HumanVerdict            string            `json:"human_verdict"`
	CorrectedStatement      *string           `json:"corrected_statement,omitempty"`
	HumanNote               *string           `json:"human_note,omitempty"`
}

// VerdictRequest is the body of POST /episodes/{episode_id}/judgment-verdict. Verdict is empty for a
// note-only submission; CorrectedStatement is required when Verdict is "corrected"; "no_learning" is the opt-out.
type VerdictRequest struct {
	Verdict            string `json:"verdict,omitempty"`
	CorrectedStatement string `json:"corrected_statement,omitempty"`
	Note               string `json:"note,omitempty"`
	Surface            string `json:"surface"`
	ActorLabel         string `json:"actor_label"`
}

// Person is what the surface needs to show a recipient. Core's contract carries person ids only.
type Person struct {
	ID    string
	Name  string
	Email string
}

// Directory maps person ids to display data.
type Directory map[string]Person
