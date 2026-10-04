// Every eval is a pure function of Input (contracts/schemas/deterministic_eval_input.v1.json): no
// LLM, no clock and no I/O, so the same input always yields the same EvalResults. Database lookups
// live outside the package; results are written by Save.
package deterministic

import (
	"encoding/json"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// EvidenceRef is common.v1.json#/$defs/evidenceRef.
type EvidenceRef struct {
	ActivityID      string     `json:"activity_id"`
	ClaimID         string     `json:"claim_id,omitempty"`
	Quote           string     `json:"quote,omitempty"`
	SpeakerPersonID string     `json:"speaker_person_id,omitempty"`
	OccurredAt      *time.Time `json:"occurred_at,omitempty"`
}

// Recipient is one addressee of a draft (agent_run_output recipients).
type Recipient struct {
	PersonID string `json:"person_id"`
	Role     string `json:"role"`
	Why      string `json:"why,omitempty"`
}

// Artifact is the draft's finished artifact.
type Artifact struct {
	Channel     string   `json:"channel"`
	Subject     *string  `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments,omitempty"`
}

// CRMIntent is the draft's CRM next-step intent.
type CRMIntent struct {
	NextStep    string     `json:"next_step"`
	DueAt       *time.Time `json:"due_at"`
	StageChange *string    `json:"stage_change"`
}

// Output is agent_run_output.v1.json: the draft under evaluation.
type Output struct {
	ProposedActionType string        `json:"proposed_action_type"`
	Recipients         []Recipient   `json:"recipients"`
	FinishedArtifact   Artifact      `json:"finished_artifact"`
	CRMNextStepIntent  CRMIntent     `json:"crm_next_step_intent"`
	Reason             string        `json:"reason"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs"`
	KnowledgeRefsUsed  []string      `json:"knowledge_refs_used,omitempty"`
	WaitUntil          *time.Time    `json:"wait_until"`
}

// RunStep is one agent_run.v1.json step.
type RunStep struct {
	Seq              int             `json:"seq"`
	Step             string          `json:"step"`
	Status           string          `json:"status"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	ExternalEffectID *string         `json:"external_effect_id,omitempty"`
	Detail           json.RawMessage `json:"detail,omitempty"`
}

// Person is one row of the people directory.
type Person struct {
	PersonID     string  `json:"person_id"`
	DisplayName  string  `json:"display_name"`
	Kind         string  `json:"kind"`
	AccountID    *string `json:"account_id"`
	MergedInto   *string `json:"merged_into"`
	HasEmail     bool    `json:"has_email"`
	InternalOnly bool    `json:"internal_only"`
}

// Opportunity is one opportunity of the directory.
type Opportunity struct {
	OpportunityID string  `json:"opportunity_id"`
	AccountID     string  `json:"account_id"`
	OwnerPersonID *string `json:"owner_person_id"`
}

// Activity is an activity the draft may cite, with its normalized text.
type Activity struct {
	ActivityID   string    `json:"activity_id"`
	AccountID    *string   `json:"account_id"`
	ActivityType string    `json:"activity_type"`
	OccurredAt   time.Time `json:"occurred_at"`
	Text         string    `json:"text"`
}

// PriorAction is an outbound email, meeting, document share or CRM write already made.
type PriorAction struct {
	RefKind            string     `json:"ref_kind"`
	RefID              string     `json:"ref_id"`
	Action             string     `json:"action"`
	Status             string     `json:"status"`
	OccurredAt         time.Time  `json:"occurred_at"`
	RecipientPersonIDs []string   `json:"recipient_person_ids"`
	Subject            *string    `json:"subject"`
	BodyText           *string    `json:"body_text"`
	Attachments        []string   `json:"attachments"`
	MeetingStart       *time.Time `json:"meeting_start"`
	StageChange        *string    `json:"stage_change"`
	NextStep           *string    `json:"next_step"`
}

// Asset is a sendable asset of the workspace library.
type Asset struct {
	Name              string   `json:"name"`
	Aliases           []string `json:"aliases"`
	Available         bool     `json:"available"`
	Confidential      bool     `json:"confidential"`
	ShareCondition    *string  `json:"share_condition"`
	ShareConditionMet bool     `json:"share_condition_met"`
}

// CatalogItem is one product of the price book with its plans and regions.
type CatalogItem struct {
	Product string   `json:"product"`
	Plans   []string `json:"plans"`
	Regions []string `json:"regions"`
}

// QuotedLine is one line the account has been quoted.
type QuotedLine struct {
	Product   string   `json:"product"`
	Plan      *string  `json:"plan"`
	Region    *string  `json:"region"`
	Quantity  *float64 `json:"quantity"`
	Unit      *string  `json:"unit"`
	UnitPrice *float64 `json:"unit_price"`
	Total     *float64 `json:"total"`
}

// Commercial is the price book and the account's quoted lines.
type Commercial struct {
	Currency                 *string       `json:"currency"`
	Catalog                  []CatalogItem `json:"catalog"`
	Quoted                   []QuotedLine  `json:"quoted"`
	ApprovedDiscountPercents []float64     `json:"approved_discount_percents"`
}

// CRMRules are the workspace's stage rules (ADR-0014).
type CRMRules struct {
	Stages          []string `json:"stages"`
	TerminalStages  []string `json:"terminal_stages"`
	MaxForwardSteps int      `json:"max_forward_steps"`
}

// Policy is the workspace's tool and autonomy policy (ADR-0014).
type Policy struct {
	WorkspaceID   string   `json:"workspace_id"`
	AccountIDs    []string `json:"account_ids"`
	AutonomyLevel string   `json:"autonomy_level"`
	AllowedTools  []string `json:"allowed_tools"`
}

// Input is deterministic_eval_input.v1.json: everything the evals read for one draft.
type Input struct {
	AgentRunID    string                `json:"agent_run_id"`
	DraftIndex    int                   `json:"draft_index"`
	WorkspaceID   string                `json:"workspace_id"`
	AccountID     string                `json:"account_id"`
	OpportunityID *string               `json:"opportunity_id"`
	RunMode       string                `json:"run_mode"`
	ExecuteMode   string                `json:"execute_mode,omitempty"`
	EvaluatedAt   time.Time             `json:"evaluated_at"`
	Draft         Output                `json:"draft"`
	State         reducer.AccountState  `json:"state"`
	LatestState   *reducer.AccountState `json:"latest_state"`
	RunSteps      []RunStep             `json:"run_steps"`
	People        []Person              `json:"people"`
	Opportunities []Opportunity         `json:"opportunities"`
	Activities    []Activity            `json:"activities"`
	PriorActions  []PriorAction         `json:"prior_actions"`
	Assets        []Asset               `json:"assets"`
	Commercial    Commercial            `json:"commercial"`
	CRM           CRMRules              `json:"crm"`
	Policy        Policy                `json:"policy"`
}
