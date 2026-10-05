// Package recompute derives what a human edit of the chosen action invalidated, recomputed and preserved
// (HAR-97 E11 edit propagation; HAR-145 "Human edit / recomputation visualization";
// contracts/schemas/dependency_invalidation.v1.json). Everything is read from stored facts at request time: the
// saved edits or the HumanDelta's literal changes, the candidate's EvalBundle (the evals on the OLD artifact), the
// send-time re-evaluation batch of the FINAL artifact (send_eval_results, with the state version and digest the
// evaluation read) and the state the run read.
// Nothing is predicted and nothing is fabricated: what no row proves was recomputed is reported as not
// recomputed.
package recompute

import "time"

// Status is the invalidation's state.
type Status string

// The statuses (dependency_invalidation.v1.json status).
const (
	NotDecided Status = "not_decided"
	Unedited   Status = "unedited"
	EditsPend  Status = "edits_pending"
	// Reevaluated: sent with edits and the final artifact was re-evaluated at send time. It does not claim that
	// everything was recomputed: the ranking and knowledge use are never re-derived (see not_recomputed).
	Reevaluated Status = "reevaluated"
	Discarded   Status = "discarded"
	Unavailable Status = "recomputation_unavailable"
)

// Field is the part of the action an edit changes.
type Field string

// The fields.
const (
	Recipients  Field = "recipients"
	Subject     Field = "subject"
	Body        Field = "body"
	Channel     Field = "channel"
	Attachments Field = "attachments"
)

// allFields is every edit field; an eval the dependency table does not know depends on all of them.
var allFields = []Field{Recipients, Subject, Body, Channel, Attachments}

// RefKind is what a span reference points at.
type RefKind string

// The reference kinds.
const (
	ArtifactField RefKind = "artifact_field"
	RankingRat    RefKind = "ranking_rationale"
	KnowledgeUse  RefKind = "knowledge_use_claim"
	EvalResult    RefKind = "eval_result"
	FinalArtifact RefKind = "final_artifact"
	AccountState  RefKind = "account_state"
)

// SpanRef is one object an edit touched or left alone, with the reason.
type SpanRef struct {
	Kind     RefKind `json:"kind"`
	RefID    *string `json:"ref_id"`
	Field    *Field  `json:"field"`
	Label    string  `json:"label"`
	Reason   string  `json:"reason"`
	Verdict  *string `json:"verdict"`
	Replaces *string `json:"replaces"`
}

// Edit is one literal change with its field and coarse class.
type Edit struct {
	Field         Field  `json:"field"`
	Kind          string `json:"kind"`
	Before        any    `json:"before"`
	After         any    `json:"after"`
	SemanticClass string `json:"semantic_class"`
}

// Entry is one edit and what it did to the trace.
type Entry struct {
	Index         int       `json:"index"`
	Edit          Edit      `json:"edit"`
	Invalidated   []SpanRef `json:"invalidated"`
	Recomputed    []SpanRef `json:"recomputed"`
	NotRecomputed []SpanRef `json:"not_recomputed"`
	Preserved     []SpanRef `json:"preserved"`
}

// StateRef reports the account state across the edit. An edit never writes account state, but "preserved" is
// proven, not assumed: it compares the version and the content digest the send-time evaluation stored with those
// of the state the run read. Preserved is true when both match, false when either differs, and nil (unknown) when
// either side cannot be established (before the send, or a state that cannot be read).
type StateRef struct {
	AccountID     string `json:"account_id"`
	VersionBefore *int   `json:"version_before"`
	VersionAfter  *int   `json:"version_after"`
	Preserved     *bool  `json:"preserved"`
}

// Invalidation is dependency_invalidation.v1.json.
type Invalidation struct {
	RunID             string    `json:"run_id"`
	DecisionEpisodeID string    `json:"decision_episode_id"`
	HumanDeltaID      *string   `json:"human_delta_id"`
	Status            Status    `json:"status"`
	Edited            bool      `json:"edited"`
	SemanticLabels    []string  `json:"semantic_labels"`
	AccountState      StateRef  `json:"account_state"`
	Entries           []Entry   `json:"entries"`
	PreservedOverall  []SpanRef `json:"preserved_overall"`
	GeneratedAt       time.Time `json:"generated_at"`
}
