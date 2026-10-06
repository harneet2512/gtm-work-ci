// Package reducer folds an account's adjudicated claims and activities into the AccountState
// projection (contracts/schemas/account_state.v1.json). The fold is pure: the same inputs always
// give the same state, and every known field points back to evidence ("unknown" is legal).
package reducer

import (
	"encoding/json"
	"log/slog"
	"time"
)

// EvidenceRef points at the activity (and claim, quote, speaker) that backs a value.
type EvidenceRef struct {
	ActivityID      string    `json:"activity_id"`
	ClaimID         string    `json:"claim_id,omitempty"`
	Quote           string    `json:"quote,omitempty"`
	SpeakerPersonID string    `json:"speaker_person_id,omitempty"`
	OccurredAt      time.Time `json:"occurred_at"`
}

// FieldConflict is a lower-standing claim that is newer than the field's winner and contradicts
// it (ADR-0008); the winner stands and the rep is asked to confirm.
type FieldConflict struct {
	ClaimID  string `json:"claim_id"`
	Standing string `json:"standing"`
	Reason   string `json:"reason"`
}

// Field is one state field with its provenance (account_state.v1.json fieldMeta).
type Field struct {
	Value             any             `json:"value"`
	Known             bool            `json:"known"`
	WinningClaimID    *string         `json:"winning_claim_id"`
	Standing          *string         `json:"standing"`
	Confidence        *float64        `json:"confidence"`
	AsOf              *time.Time      `json:"as_of"`
	EvidenceRefs      []EvidenceRef   `json:"evidence_refs"`
	CompetingClaimIDs []string        `json:"competing_claim_ids"`
	SuggestedClaimIDs []string        `json:"suggested_claim_ids"`
	Conflicts         []FieldConflict `json:"conflicts,omitempty"`
	Derived           bool            `json:"derived,omitempty"`
	// OpportunityID names the deal an account-level headline value was taken from; empty on a deal's own
	// fields and on values that are account-scoped (ADR-0016).
	OpportunityID *string `json:"opportunity_id,omitempty"`
}

// ListItems returns the items of a known list field. A state read back from JSON (account_state,
// state_history) holds them as generic maps, so this decodes both that and the typed form the fold
// produces; anything that is not a list gives nil.
func (f Field) ListItems() []Item {
	if !f.Known {
		return nil
	}
	switch v := f.Value.(type) {
	case []Item:
		return v
	case nil, string:
		return nil
	}
	out, err := decodeItems(f.Value)
	if err != nil {
		slog.Warn("reducer: list field value is not a list of items", "error", err)
		return nil
	}
	return out
}

func decodeItems(v any) ([]Item, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []Item
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Item is one entry of a list field's value.
type Item struct {
	Text          string        `json:"text"`
	ClaimID       string        `json:"claim_id"`
	Status        string        `json:"status,omitempty"`
	DueAt         *time.Time    `json:"due_at,omitempty"`
	OwnerPersonID string        `json:"owner_person_id,omitempty"`
	EvidenceRefs  []EvidenceRef `json:"evidence_refs,omitempty"`
}

// Fields holds the twenty state fields (account_state.v1.json properties.fields).
type Fields struct {
	Stage                   Field `json:"stage"`
	Health                  Field `json:"health"`
	Owner                   Field `json:"owner"`
	Motion                  Field `json:"motion"`
	Champion                Field `json:"champion"`
	ChampionStatus          Field `json:"champion_status"`
	EconomicBuyer           Field `json:"economic_buyer"`
	Blockers                Field `json:"blockers"`
	Objections              Field `json:"objections"`
	DecisionCriteria        Field `json:"decision_criteria"`
	DecisionProcess         Field `json:"decision_process"`
	CurrentCommitments      Field `json:"current_commitments"`
	NextMilestone           Field `json:"next_milestone"`
	NextMeeting             Field `json:"next_meeting"`
	RelationshipRisk        Field `json:"relationship_risk"`
	ProductUseCase          Field `json:"product_use_case"`
	CommercialIssue         Field `json:"commercial_issue"`
	LastCustomerInteraction Field `json:"last_customer_interaction"`
	LastMeaningfulChange    Field `json:"last_meaningful_change"`
	Summary                 Field `json:"summary"`
	// ChampionSince is optional (ADR-0012): when the current champion's unbroken run of claims began.
	// Absent without a known champion.
	ChampionSince *Field `json:"champion_since,omitempty"`
}

// fieldNames lists the state fields in schema order.
var fieldNames = []string{
	"stage", "health", "owner", "motion", "champion", "champion_status", "economic_buyer",
	"blockers", "objections", "decision_criteria", "decision_process", "current_commitments",
	"next_milestone", "next_meeting", "relationship_risk", "product_use_case", "commercial_issue",
	"last_customer_interaction", "last_meaningful_change", "summary",
}

// FieldNames returns the state field names in schema order. The caller gets its own copy.
func FieldNames() []string { return append([]string(nil), fieldNames...) }

// accountDerived are the two fields the AccountState derives from the whole account (ADR-0016); every other
// field is a deal's headline.
var accountDerived = map[string]bool{"last_customer_interaction": true, "last_meaningful_change": true}

// DealFieldNames returns, in schema order, the fields whose AccountState value is the primary deal's headline
// (all of FieldNames except the two account-derived ones).
func DealFieldNames() []string {
	var out []string
	for _, n := range fieldNames {
		if !accountDerived[n] {
			out = append(out, n)
		}
	}
	return out
}

// PrimaryChanged reports whether the primary opportunity differs between two versions of an account's state
// (prev nil = the account's first state, which has no previous primary). When it does, the deal-scoped fields
// of the two versions describe different deals, so comparing them gives false changes (ADR-0016).
func PrimaryChanged(prev *AccountState, next AccountState) bool {
	if prev == nil {
		return false
	}
	a, b := prev.OpportunityID, next.OpportunityID
	if a == nil || b == nil {
		return a != b
	}
	return *a != *b
}

// Field returns the field with the given schema name, or nil.
func (f *Fields) Field(name string) *Field {
	switch name {
	case "stage":
		return &f.Stage
	case "health":
		return &f.Health
	case "owner":
		return &f.Owner
	case "motion":
		return &f.Motion
	case "champion":
		return &f.Champion
	case "champion_status":
		return &f.ChampionStatus
	case "economic_buyer":
		return &f.EconomicBuyer
	case "blockers":
		return &f.Blockers
	case "objections":
		return &f.Objections
	case "decision_criteria":
		return &f.DecisionCriteria
	case "decision_process":
		return &f.DecisionProcess
	case "current_commitments":
		return &f.CurrentCommitments
	case "next_milestone":
		return &f.NextMilestone
	case "next_meeting":
		return &f.NextMeeting
	case "relationship_risk":
		return &f.RelationshipRisk
	case "product_use_case":
		return &f.ProductUseCase
	case "commercial_issue":
		return &f.CommercialIssue
	case "last_customer_interaction":
		return &f.LastCustomerInteraction
	case "last_meaningful_change":
		return &f.LastMeaningfulChange
	case "summary":
		return &f.Summary
	}
	return nil
}

// Member is one buying-group entry.
type Member struct {
	PersonID    string   `json:"person_id"`
	DisplayName string   `json:"display_name"`
	Title       *string  `json:"title"`
	Roles       []string `json:"roles"`
	// RoleSource says where Roles come from (RoleRecorded or RoleInferred); nil when the only role is unknown.
	RoleSource *string `json:"role_source"`
	// RoleBasis is a short note on the strongest claim behind Roles; nil exactly when RoleSource is nil.
	RoleBasis *string `json:"role_basis"`
	// RoleProvenance is the provenance of each role in Roles (not "unknown"), in the order of Roles; RoleSource and
	// RoleBasis summarize only the strongest claim among them.
	RoleProvenance []RoleProvenance `json:"role_provenance"`
	// Tenure is optional (ADR-0012): "interim" for an acting or interim holder, "permanent" for a titled
	// holder who is not, absent (unknown) without a title.
	Tenure            string        `json:"tenure,omitempty"`
	Status            string        `json:"status"`
	DelegatedToPerson *string       `json:"delegated_to_person_id"`
	LastEngagedAt     *time.Time    `json:"last_engaged_at"`
	EvidenceRefs      []EvidenceRef `json:"evidence_refs"`
}

// AccountState is the projection stored in account_state.state and state_history.state.
type AccountState struct {
	AccountID   string `json:"account_id"`
	AccountName string `json:"account_name,omitempty"`
	// OpportunityID is the primary opportunity: the open deal with the latest activity (null when none is open).
	OpportunityID *string `json:"opportunity_id"`
	// Opportunities summarizes every deal of the account, primary first.
	Opportunities  []OpportunitySummary `json:"opportunities"`
	Version        int                  `json:"version"`
	AsOf           time.Time            `json:"as_of"`
	ComputedAt     time.Time            `json:"computed_at"`
	LastActivityID *string              `json:"last_activity_id"`
	Fields         Fields               `json:"fields"`
	BuyingGroup    []Member             `json:"buying_group"`
	CoverageGaps   []string             `json:"coverage_gaps"`
	// RelationshipState and OpenTransition are optional (ADR-0012), account scope: the transition detector sets
	// them in the recompute transaction. The reducer itself never assigns a relationship state.
	RelationshipState *RelationshipState `json:"relationship_state,omitempty"`
	OpenTransition    *OpenTransition    `json:"open_transition,omitempty"`
}

// RelationshipState is the newest CONFIRMED relationship state of the account, or "unknown" with null ids.
type RelationshipState struct {
	Value        string     `json:"value"`
	TransitionID *string    `json:"transition_id"`
	ConfirmedAt  *time.Time `json:"confirmed_at"`
}

// MissingFact names an unmet fact of an open transition and whether CONFIRMED requires it.
type MissingFact struct {
	Key      string `json:"key"`
	Required bool   `json:"required"`
}

// OpenTransition summarises the account's one open (CANDIDATE or UNRESOLVED, not closed) transition.
type OpenTransition struct {
	TransitionID     string        `json:"transition_id"`
	FromState        string        `json:"from_state"`
	ToStateCandidate *string       `json:"to_state_candidate"`
	Status           string        `json:"status"`
	Confidence       float64       `json:"confidence"`
	MissingFacts     []MissingFact `json:"missing_facts"`
	LastUpdatedAt    time.Time     `json:"last_updated_at"`
}
