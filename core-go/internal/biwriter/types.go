// Package biwriter is WP26 (HAR-124): it restates what one released replay event changed as an AccountChange
// and, when the change is material, a BusinessIntelligenceUpdate (HAR-129 section 5, 14).
//
// Everything here is deterministic and model-free. The prose of the update (summary, claim statements,
// why_it_matters) is a template over facts that are already stored: the state diff, the graph diff of the
// event, the signals and trigger evaluation of the recompute and the relationship-state transition. Every
// claim names the diff entry it restates and cites the activities (and graph-diff items) it rests on;
// Validate enforces that and Build refuses to return anything Validate rejects.
//
//	Facts (loaded)  -->  Build  -->  AccountChange + BusinessIntelligenceUpdate  -->  Write (one transaction)
package biwriter

import "time"

// EvidenceRef is common.v1.json#evidenceRef.
type EvidenceRef struct {
	ActivityID      string     `json:"activity_id"`
	ClaimID         string     `json:"claim_id,omitempty"`
	Quote           string     `json:"quote,omitempty"`
	SpeakerPersonID string     `json:"speaker_person_id,omitempty"`
	OccurredAt      *time.Time `json:"occurred_at,omitempty"`
}

// DiffEntry is one element of state_diffs.changes (state_diff.v1.json). Before and After hold the generic
// JSON values the diff was stored with.
type DiffEntry struct {
	Field        string        `json:"field"`
	Op           string        `json:"op"`
	Before       any           `json:"before,omitempty"`
	After        any           `json:"after,omitempty"`
	Material     bool          `json:"material"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// StateDiff is the stored state diff the event's recompute wrote.
type StateDiff struct {
	ID          string
	AccountID   string
	FromVersion int
	ToVersion   int
	IsMaterial  bool
	Entries     []DiffEntry
	ActivityIDs []string
}

// GraphItem is one node or edge of the event's graph diff (ctxgraph.EventChange).
type GraphItem struct {
	Kind        string // node | edge
	Type        string // Neo4j label or relationship type
	ID          string
	Op          string
	Attributed  bool     // the event itself is evidence for the element
	ActivityIDs []string // the activities the element is evidence-linked to, when known
}

// GraphDiff is the event's graph diff as the writer needs it. SourceEventID is the source_events id that
// GET /events/{id}/graph-diff takes; JobIDs are the account's projection jobs that touched the event.
type GraphDiff struct {
	SourceEventID string
	JobIDs        []int64
	// PrevJobID is the account's own projection job before the first one that touched the event (0 if none).
	// Job ids are one global counter, so "first job minus one" could be another account's job.
	PrevJobID int64
	Items     []GraphItem
}

// Signal is a signal the recompute emitted for the diff.
type Signal struct{ ID, Type string }

// Trigger is the trigger evaluation of the diff.
type Trigger struct {
	Eligible    bool
	ReasonCodes []string
}

// Fact is one fact of a StateTransition.
type Fact struct {
	Key         string
	Description string
	Required    bool
	Evidence    []EvidenceRef
}

// Transition is the StateTransition the event touched, or the account's open one when it touched none (nil in
// Facts when the account has neither).
type Transition struct {
	ID         string
	Status     string // CONFIRMED | CANDIDATE | UNRESOLVED | REJECTED
	FromState  string
	ToState    *string
	Supporting []Fact
	Missing    []Fact
	// Touched is true when the event moved this transition; false when it is the account's open transition that the
	// event did not touch (shown labelled unchanged).
	Touched bool
}

// Activity is the activity the released event became.
type Activity struct {
	ID         string
	Type       string
	Summary    string
	OccurredAt time.Time
}

// Facts is everything Build reads. It holds only stored facts; there is no model, clock or network behind it.
type Facts struct {
	AccountID      string
	AccountName    string
	OpportunityID  string
	HeldOutEventID string
	Activity       Activity
	Diff           StateDiff
	Graph          GraphDiff
	Signals        []Signal
	Trigger        *Trigger
	Transition     *Transition
	People         map[string]string // person id -> display name
}

// IDs are the ids of the two objects Build creates.
type IDs struct{ Change, BI string }

// StateRef is common.v1.json#stateRef.
type StateRef struct {
	AccountID     string  `json:"account_id"`
	OpportunityID *string `json:"opportunity_id"`
	Version       int     `json:"version"`
}

// GraphDiffRef is common.v1.json#graphDiffRef.
type GraphDiffRef struct {
	ID                string `json:"id"`
	FromProjectionSeq int64  `json:"from_projection_seq"`
	ToProjectionSeq   int64  `json:"to_projection_seq"`
}

// AccountChange is account_change.v1.json.
type AccountChange struct {
	ID                 string        `json:"id"`
	AccountID          string        `json:"account_id"`
	OpportunityID      *string       `json:"opportunity_id"`
	HeldOutEventID     *string       `json:"held_out_event_id"`
	TriggerActivityIDs []string      `json:"trigger_activity_ids"`
	PreviousStateRef   StateRef      `json:"previous_state_ref"`
	CurrentStateRef    StateRef      `json:"current_state_ref"`
	StateDiffID        string        `json:"state_diff_id"`
	GraphDiffRef       GraphDiffRef  `json:"graph_diff_ref"`
	MaterialChange     bool          `json:"material_change"`
	EvidenceRefs       []EvidenceRef `json:"evidence_refs"`
	CreatedAt          time.Time     `json:"created_at"`
}

// GraphDiffItem is business_intelligence_update.v1.json claims[].graph_diff_items[].
type GraphDiffItem struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Claim is business_intelligence_update.v1.json claims[].
type Claim struct {
	Statement      string          `json:"statement"`
	Dimension      string          `json:"dimension"`
	StateDiffField *string         `json:"state_diff_field"`
	EvidenceRefs   []EvidenceRef   `json:"evidence_refs"`
	GraphDiffItems []GraphDiffItem `json:"graph_diff_items,omitempty"`
}

// MissingFact is business_intelligence_update.v1.json transition.missing_facts[].
type MissingFact struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// TransitionRef is business_intelligence_update.v1.json transition.
type TransitionRef struct {
	StateTransitionID string        `json:"state_transition_id"`
	Status            string        `json:"status"`
	FromState         string        `json:"from_state"`
	ToStateCandidate  *string       `json:"to_state_candidate"`
	MissingFacts      []MissingFact `json:"missing_facts"`
	// TouchedByEvent is false for the account's open transition that the event did not touch: "unchanged".
	TouchedByEvent bool `json:"touched_by_event"`
}

// AccountMapRef is business_intelligence_update.v1.json account_map_ref.
type AccountMapRef struct {
	StateRef     StateRef     `json:"state_ref"`
	GraphDiffRef GraphDiffRef `json:"graph_diff_ref"`
}

// BI is business_intelligence_update.v1.json.
type BI struct {
	ID              string         `json:"id"`
	AccountID       string         `json:"account_id"`
	OpportunityID   *string        `json:"opportunity_id"`
	AccountChangeID string         `json:"account_change_id"`
	Summary         string         `json:"summary"`
	Claims          []Claim        `json:"claims"`
	WhyItMatters    string         `json:"why_it_matters"`
	KnowledgeRefs   []string       `json:"knowledge_refs"`
	AccountMapRef   AccountMapRef  `json:"account_map_ref"`
	Transition      *TransitionRef `json:"transition"`
	Model           *string        `json:"model"`
	CreatedAt       time.Time      `json:"created_at"`
}

// Result is what Build returns: the change, and its update when the change is material.
type Result struct {
	Change AccountChange
	BI     *BI
}
