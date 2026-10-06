package bucket1

import (
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// Episode is everything the Bucket 1 gates read about one real episode (MedTech Event 13, the later EcoLite
// episode): the raw activities, the claims extracted from them, the resolutions, the claim graph before and
// after, the state diff beside the graph-diff, precedents, knowledge with the real knowledge_attribution
// record, the beliefs of the synthesis and the knowledge revisions the episode caused. It is assembled by a
// loader and graded by Run; nothing here reads a database or a clock.
type Episode struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	At            time.Time `json:"at"` // the episode's world time: the replay clock, never the wall clock
	AccountID     string    `json:"account_id"`
	OpportunityID string    `json:"opportunity_id"`

	InternalDomains []string      `json:"internal_domains"`
	Activities      []Activity    `json:"activities"`
	Claims          []Claim       `json:"claims"`         // claims extracted from this episode's activities
	PriorClaims     []Claim       `json:"prior_claims"`   // the claim graph before the event
	CriticalFacts   []string      `json:"critical_facts"` // gold: facts an extraction must not omit (empty: not measured)
	People          []Person      `json:"people"`
	Opportunities   []Opportunity `json:"opportunities"`
	Resolutions     []Resolution  `json:"resolutions"`

	StateDiff       []FieldChange `json:"state_diff"`
	GraphDiffFields []string      `json:"graph_diff_fields"` // fields /events/{id}/graph-diff reports as changed
	GraphDiffKnown  bool          `json:"graph_diff_known"`
	// GraphProjected and GraphChanges are the graph-diff by presence (node and edge changes attributed to the event):
	// it counts graph elements, not state fields, so it is compared with the state diff by whether either is empty.
	GraphProjected bool              `json:"graph_projected"`
	GraphChanges   int               `json:"graph_changes"`
	Transition     *Transition       `json:"transition,omitempty"`
	Unresolved     []string          `json:"unresolved"` // fields the evidence cannot settle
	StateAfter     map[string]string `json:"state_after"`
	NewClaimFields []string          `json:"new_claim_fields"` // fields at least one new claim speaks about
	// WinningClaims is the winning claim id of each state field after the event (nil: not read). A prior claim only
	// "carries a stale fact forward" while it still wins its field.
	WinningClaims map[string]string `json:"winning_claims"`
	// StateUnread is true when the account state after the event could not be read, so winners are unknown.
	StateUnread bool `json:"state_unread"`
	// PriorStatusAfter is each prior claim's status after the event (nil: not read).
	PriorStatusAfter map[string]string `json:"prior_status_after"`

	Precedents []Precedent `json:"precedents"`

	Knowledge   []knowledge.Knowledge `json:"knowledge"`
	Situation   *knowledge.Situation  `json:"situation"`
	Attribution *Attribution          `json:"attribution,omitempty"`

	Beliefs []Belief `json:"beliefs"`

	Revisions []Revision     `json:"revisions"`
	Trace     *RevisionTrace `json:"trace,omitempty"`
}

// Activity is one source event.
type Activity struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	ExternalID string    `json:"external_id"`
	SpeakerID  string    `json:"speaker_id"`
	Text       string    `json:"text"`
	OccurredAt time.Time `json:"occurred_at"`
	AccountID  string    `json:"account_id"`
}

// Claim is one extracted statement. Kind is "fact" (stated in the source) or "inference" (concluded from it).
type Claim struct {
	ID                 string    `json:"id"`
	ActivityID         string    `json:"activity_id"`
	AccountID          string    `json:"account_id"`
	Kind               string    `json:"kind"`
	Field              string    `json:"field"`
	Value              string    `json:"value"`
	Quote              string    `json:"quote"`
	SpeakerID          string    `json:"speaker_id"`
	OccurredAt         time.Time `json:"occurred_at"`
	Status             string    `json:"status"` // active | superseded | conflicted | rejected
	ConflictsWith      []string  `json:"conflicts_with"`
	Supersedes         string    `json:"supersedes"`
	SupersessionReason string    `json:"supersession_reason"`
	SupportsClaims     []string  `json:"supports_claims"`
}

// Person is a directory entry. Internal marks the workspace's own people.
type Person struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"` // "" for an internal person
	Email     string `json:"email"`
	Internal  bool   `json:"internal"`
}

// Opportunity belongs to exactly one account.
type Opportunity struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
}

// Resolution is what core attached one activity to.
type Resolution struct {
	ActivityID    string   `json:"activity_id"`
	PersonID      string   `json:"person_id"` // "" when core abstained
	AccountID     string   `json:"account_id"`
	OpportunityID string   `json:"opportunity_id"`
	Internal      bool     `json:"internal"`
	Candidates    []string `json:"candidates"` // person ids that matched; more than one means ambiguous
	Mapping       string   `json:"mapping"`    // the source mapping/provenance (e.g. the matched email address)
}

// FieldChange is one line of the state diff.
type FieldChange struct {
	Field    string `json:"field"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Material bool   `json:"material"`
	Refs     []Ref  `json:"evidence_refs"`
}

// Transition is the open StateTransition the episode produced.
type Transition struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // candidate | confirmed | unresolved | rejected
	ToState string `json:"to_state"`
	Support []Ref  `json:"support"`
}

// Precedent is a prior case the retrieval returned, with what actually happened in it.
type Precedent struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	SharedFeatures   []string  `json:"shared_features"` // situational features it shares with this episode (transition, stage, role...)
	TextOnlyMatch    bool      `json:"text_only_match"`
	HumanChoice      string    `json:"human_choice"`
	CustomerResponse string    `json:"customer_response"` // what the customer or world did; "" when unobserved
	Differences      []string  `json:"differences"`
	Lesson           string    `json:"lesson"`
	LessonCites      string    `json:"lesson_cites"`   // human_choice | customer_response | both | none
	ClaimsSimilar    string    `json:"claims_similar"` // same | similar | analogous
	Refs             []Ref     `json:"evidence_refs"`
}

// Belief is one consequential statement of the current-intelligence synthesis.
type Belief struct {
	Kind      string `json:"kind"` // summary | blocker | commitment | stakeholder | relationship | next_decision
	Statement string `json:"statement"`
	Refs      []Ref  `json:"evidence_refs"`
}

// Attribution is the real knowledge_attribution record of the run's build_context step
// (contracts/schemas/knowledge_attribution.v1.json).
type Attribution struct {
	StepID           string    `json:"step_id"`
	AsOf             time.Time `json:"as_of"`
	Retrieved        []string  `json:"retrieved"`
	Applicable       []string  `json:"applicable"`
	ExceptionBlocked []string  `json:"exception_blocked"`
	Used             []string  `json:"used"`
}
