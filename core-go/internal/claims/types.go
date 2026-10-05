// Package claims produces and adjudicates Claims: sourced, competing assertions about one
// account-state field (HAR-96 §18, ADR-0002, ADR-0008). Rule extractors turn structured
// source changes (CRM, calendar, enrichment) into claims deterministically; an Extractor
// (the model worker) turns free text into candidates that are converted to claims here;
// Adjudicate picks one winner per slot by standing, then recency, then confidence.
package claims

import (
	"encoding/json"
	"time"
)

// Standing is the authority rank of a claim's source.
type Standing string

// Standings, highest authority first (contracts common.v1.json#standing; first_party_record is
// ADR-0009: deterministic first-party facts that are neither CRM nor AI inference, such as calendar
// attendance and scheduled meetings or email headers and timing).
const (
	HumanApproved    Standing = "human_approved"
	CRMExplicit      Standing = "crm_explicit"
	FirstPartyRecord Standing = "first_party_record"
	FirstPartyAI     Standing = "first_party_ai"
	ThirdParty       Standing = "third_party"
)

// standingRank is the single rank table (it mirrors the generated claims.standing_rank column).
// Adjudication only ever compares ranks, so a new standing slots in here and nowhere else.
var standingRank = map[Standing]int{
	HumanApproved: 5, CRMExplicit: 4, FirstPartyRecord: 3, FirstPartyAI: 2, ThirdParty: 1,
}

// Rank returns the authority rank of s (5 = strongest); an unknown standing ranks 0.
func (s Standing) Rank() int { return standingRank[s] }

// Valid reports whether s is a known standing.
func (s Standing) Valid() bool { _, ok := standingRank[s]; return ok }

// Status is the lifecycle state of a claim row.
type Status string

// Claim statuses (claims.status CHECK list).
const (
	StatusActive     Status = "active"
	StatusOutranked  Status = "outranked"
	StatusSuperseded Status = "superseded"
	StatusExpired    Status = "expired"
	StatusRejected   Status = "rejected"
)

// FieldPath is the account-state field a claim folds into (claim.v1.json#fieldPath).
type FieldPath string

// Field paths.
const (
	FieldStage             FieldPath = "stage"
	FieldHealth            FieldPath = "health"
	FieldOwner             FieldPath = "owner"
	FieldMotion            FieldPath = "motion"
	FieldChampion          FieldPath = "champion"
	FieldChampionStatus    FieldPath = "champion_status"
	FieldEconomicBuyer     FieldPath = "economic_buyer"
	FieldBuyingGroupMember FieldPath = "buying_group.member"
	FieldStakeholderRole   FieldPath = "stakeholder_role"
	FieldBlockers          FieldPath = "blockers"
	FieldObjections        FieldPath = "objections"
	FieldDecisionCriteria  FieldPath = "decision_criteria"
	FieldDecisionProcess   FieldPath = "decision_process"
	FieldCommitment        FieldPath = "commitment"
	FieldNextMilestone     FieldPath = "next_milestone"
	FieldNextMeeting       FieldPath = "next_meeting"
	FieldRelationshipRisk  FieldPath = "relationship_risk"
	FieldProductUseCase    FieldPath = "product_use_case"
	FieldCommercialIssue   FieldPath = "commercial_issue"
	FieldDelegation        FieldPath = "delegation"
	FieldSummary           FieldPath = "summary"
	FieldAmount            FieldPath = "amount"
)

var allFieldPaths = []FieldPath{
	FieldStage, FieldHealth, FieldOwner, FieldMotion, FieldChampion, FieldChampionStatus,
	FieldEconomicBuyer, FieldBuyingGroupMember, FieldStakeholderRole, FieldBlockers,
	FieldObjections, FieldDecisionCriteria, FieldDecisionProcess, FieldCommitment,
	FieldNextMilestone, FieldNextMeeting, FieldRelationshipRisk, FieldProductUseCase,
	FieldCommercialIssue, FieldDelegation, FieldSummary, FieldAmount,
}

// Valid reports whether f is a claim field path.
func (f FieldPath) Valid() bool {
	for _, p := range allFieldPaths {
		if p == f {
			return true
		}
	}
	return false
}

// IsList reports whether claims of f are single list items folded item-wise.
func (f FieldPath) IsList() bool {
	switch f {
	case FieldBlockers, FieldObjections, FieldDecisionCriteria, FieldCommitment:
		return true
	}
	return false
}

// Unknown is the legal "no evidence" value.
const Unknown = "unknown"

// MinAIConfidence is the floor below which a first-party AI claim may never win a field; it
// is surfaced as a suggestion instead.
const MinAIConfidence = 0.6

// Claim mirrors one claims row. Empty strings stand for SQL NULL on the optional ids.
type Claim struct {
	ID               string
	AccountID        string
	OpportunityID    string
	SubjectPersonID  string
	FieldPath        FieldPath
	Value            json.RawMessage
	Standing         Standing
	Confidence       float64
	SourceActivityID string
	SpeakerPersonID  string
	EvidenceQuote    string
	OccurredAt       time.Time
	Extractor        string
	Status           Status
	SupersededBy     string
	ExpiresAt        *time.Time
}

// Conflict records a lower-standing claim that is newer than, and contradicts, the winner of
// a field. The winner stands; the conflict is reported so the rep can confirm (ADR-0008) and
// WP8 can raise signal field_contradicted from it: both claims are carried whole so the signal
// can name their standings and cite the contradicting claim's evidence.
type Conflict struct {
	Field         FieldPath
	Winner        Claim
	Contradicting Claim
	Reason        string
}

// PersonFact reports whether the claim is a fact about a person: a delegation, or a buying-group member claim
// that asserts presence or a title but no role. Such facts fold account-wide whatever opportunity the claim
// names (ADR-0016). A role belongs to a deal (the person can be champion on one and a user on another), so a
// member claim carrying a role, like every other path, is scoped to the claim's opportunity or to the account.
func (c Claim) PersonFact() bool {
	switch c.FieldPath {
	case FieldDelegation:
		return true
	case FieldBuyingGroupMember:
		return ParseMember(c.Value).Role == ""
	}
	return false
}

// PersonFact reports whether the slot holds account-wide person facts; deal views admit those of the
// deal's people.
func (s Slot) PersonFact() bool {
	return s.Scope == "" && (s.Field == FieldDelegation || (s.Field == FieldBuyingGroupMember && (s.Key == "title" || s.Key == "member")))
}
