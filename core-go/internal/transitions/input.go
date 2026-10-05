package transitions

import (
	"encoding/json"
	"time"
)

// Ref points at the activity (and claim) a fact rests on (common.v1.json#/$defs/evidenceRef).
type Ref struct {
	ActivityID      string     `json:"activity_id"`
	ClaimID         string     `json:"claim_id,omitempty"`
	Quote           string     `json:"quote,omitempty"`
	SpeakerPersonID string     `json:"speaker_person_id,omitempty"`
	OccurredAt      *time.Time `json:"occurred_at,omitempty"`
}

// FieldView is one AccountState field as the rules read it. Value is a JSON scalar or, for list
// fields, an array of ItemView.
type FieldView struct {
	Known        bool            `json:"known"`
	Value        json.RawMessage `json:"value"`
	Standing     *string         `json:"standing"`
	AsOf         *time.Time      `json:"as_of"`
	EvidenceRefs []Ref           `json:"evidence_refs"`
}

// ItemView is one entry of a list field.
type ItemView struct {
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	EvidenceRefs []Ref  `json:"evidence_refs"`
}

// MemberView is one buying-group entry.
type MemberView struct {
	Roles        []string `json:"roles"`
	Status       string   `json:"status"`
	Tenure       string   `json:"tenure"`
	EvidenceRefs []Ref    `json:"evidence_refs"`
}

// RelationshipState is AccountState.relationship_state: the newest CONFIRMED state and when it was confirmed.
type RelationshipState struct {
	Value       string     `json:"value"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
}

// State is the part of an AccountState the rules read.
type State struct {
	Fields            map[string]FieldView `json:"fields"`
	BuyingGroup       []MemberView         `json:"buying_group"`
	RelationshipState RelationshipState    `json:"relationship_state"`
}

// Signal is a signal row with its ADR-0011 openness already decided upstream. OccurredAt is when the change
// happened in the world (ADR-0015); it is the only clock the rules use. Input that carries created_at alone
// (older fixtures) is read as occurred_at.
type Signal struct {
	ID              string    `json:"id"`
	SignalType      string    `json:"signal_type"`
	Open            bool      `json:"open"`
	OccurredAt      time.Time `json:"occurred_at"`
	OpportunityID   string    `json:"opportunity_id"`
	SubjectPersonID string    `json:"subject_person_id"`
	EvidenceRefs    []Ref     `json:"evidence_refs"`
	// FirstParty says whether the signal's evidence is first-party (MarkFirstParty); nil means not assessed, which
	// counts as first-party. A signal that is not first-party never earns a fact; it can still hold a transition back.
	FirstParty *bool `json:"first_party"`
}

// UnmarshalJSON reads occurred_at, falling back to created_at for input written before ADR-0015.
func (s *Signal) UnmarshalJSON(raw []byte) error {
	type plain Signal
	aux := struct {
		*plain
		CreatedAt *time.Time `json:"created_at"`
	}{plain: (*plain)(s)}
	if err := json.Unmarshal(raw, &aux); err != nil {
		return err
	}
	if s.OccurredAt.IsZero() && aux.CreatedAt != nil {
		s.OccurredAt = *aux.CreatedAt
	}
	return nil
}

// Claim is a claim row as the rules read it. Withdrawn is true when its value says stance "withdrawn".
type Claim struct {
	ID               string    `json:"id"`
	OpportunityID    string    `json:"opportunity_id"`
	Withdrawn        bool      `json:"withdrawn"`
	FieldPath        string    `json:"field_path"`
	Status           string    `json:"status"`
	Standing         string    `json:"standing"`
	OccurredAt       time.Time `json:"occurred_at"`
	SubjectPersonID  string    `json:"subject_person_id"`
	SourceActivityID string    `json:"source_activity_id"`
}

// Deal is one deal's state as the rules read it (OpportunityState, ADR-0016).
type Deal struct {
	OpportunityID string               `json:"opportunity_id"`
	IsOpen        bool                 `json:"is_open"`
	Fields        map[string]FieldView `json:"fields"`
	BuyingGroup   []MemberView         `json:"buying_group"`
}

// Open is the account's one open transition, as far as the rules need it.
type Open struct {
	Status           string    `json:"status"`
	ToStateCandidate string    `json:"to_state_candidate"`
	LastUpdatedAt    time.Time `json:"last_updated_at"`
}

// Input is everything Evaluate depends on. Now is the AccountState version's as_of (never the wall
// clock); ComputedAt is that version's computed_at and becomes confirmed_at.
type Input struct {
	State      State      `json:"state"`
	Signals    []Signal   `json:"signals"`
	Claims     []Claim    `json:"claims"`
	Now        time.Time  `json:"now"`
	ComputedAt *time.Time `json:"computed_at"`
	Open       *Open      `json:"open_transition"`
	// Deals, when present, are the per-deal states: deal-scoped facts are read from every open deal, never from
	// the account headline. Without deals the headline in State is the only scope.
	Deals []Deal `json:"deals"`
}
