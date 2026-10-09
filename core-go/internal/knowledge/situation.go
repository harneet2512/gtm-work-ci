package knowledge

import (
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Situation is everything the matcher reads about one account at one moment.
type Situation struct {
	AccountID     string
	OpportunityID string // "" when the account has none
	Now           time.Time
	Fields        map[string]Value // state field name (and "coverage_gaps") -> value; absent = unknown
	BuyingGroup   []Member
	// Conflicts lists, per state field, the contradicting claim ids still standing (ADR-0008).
	Conflicts map[string][]string
	// RelationshipState is the newest CONFIRMED relationship state (ADR-0012); "" means unknown.
	RelationshipState string
	// Transition is the account's one open StateTransition, or nil.
	Transition *Transition
	// Signals are the account's recent signals; openness is decided by ADR-0011 at Now.
	Signals []Signal
	// Topics are the change dimensions (common.v1.json#changeDimension) of the state diff that triggered the case, sorted
	// and unique: what the interpretation is about. nil = unknown (no diff, or none of its fields has a dimension).
	Topics []string
}

// Value is one state field as the matcher sees it.
type Value struct {
	Known        bool
	List         bool
	Scalar       any // string, float64, bool or nil (known absent); ignored for lists
	Items        []Item
	EvidenceRefs []EvidenceRef
}

// Item is one entry of a list field.
type Item struct {
	Text          string
	Status        string
	ClaimID       string
	OwnerPersonID string
}

// Member is one buying-group entry.
type Member struct {
	PersonID string
	Roles    []string
	Status   string
}

// Signal is the part of a signal (signal.v1.json) that openness depends on.
type Signal struct {
	ID             string
	Type           string
	OpportunityID  *string
	SubjectClaimID *string
	// SubjectItemKey is claims.ItemKey of the reported item's text, when the caller resolved it.
	SubjectItemKey string
	Details        map[string]any
	EvidenceRefs   []EvidenceRef
	CreatedAt      time.Time
}

// Transition is the ADR-0012 open-transition summary the matcher reads.
type Transition struct {
	ID        string
	FromState string
	ToState   string // to_state_candidate; "" when null (UNRESOLVED)
	Status    string // CANDIDATE or UNRESOLVED
}

// listFieldNames are the AccountState list fields, plus coverage_gaps (top level in the state).
var listFieldNames = map[string]bool{
	"blockers": true, "objections": true, "decision_criteria": true, "current_commitments": true, "coverage_gaps": true,
}

// stateFieldNames returns every field name a condition may use without a namespace prefix.
func stateFieldNames() map[string]bool {
	names := map[string]bool{"coverage_gaps": true}
	for _, n := range reducer.FieldNames() {
		names[n] = true
	}
	return names
}

// FromAccountState builds a Situation from a reduced AccountState and the account's signals. Its deal-scoped
// fields are the primary opportunity's headline (ADR-0016), while its buying group and coverage gaps span all the
// account's OPEN deals, so the two are not one deal's view; use FromOpportunityState to match one named deal
// (signalstore.SituationAt does so whenever the account has a primary deal).
func FromAccountState(st reducer.AccountState, now time.Time, signals []Signal, relationshipState string, open *Transition) Situation {
	opportunity := ""
	if st.OpportunityID != nil {
		opportunity = *st.OpportunityID
	}
	if relationshipState == "" && open == nil {
		relationshipState, open = RelationshipOf(st)
	}
	return situationOf(st.AccountID, opportunity, &st.Fields, st.CoverageGaps, st.BuyingGroup, now, signals, relationshipState, open)
}

// RelationshipOf reads the account's earned relationship state and its open transition from the AccountState
// (ADR-0012); "" and nil when the state is unknown or none is open.
func RelationshipOf(st reducer.AccountState) (string, *Transition) {
	rel := ""
	if st.RelationshipState != nil && st.RelationshipState.Value != "unknown" {
		rel = st.RelationshipState.Value
	}
	var open *Transition
	if st.OpenTransition != nil {
		open = &Transition{ID: st.OpenTransition.TransitionID, FromState: st.OpenTransition.FromState, Status: st.OpenTransition.Status}
		if st.OpenTransition.ToStateCandidate != nil {
			open.ToState = *st.OpenTransition.ToStateCandidate
		}
	}
	return rel, open
}

// FromOpportunityState builds a Situation from one deal's state: its own fields, buying group and coverage
// gaps, so a lesson is matched against the deal it would be applied to and never against a mixture of deals.
// Of the account's signals it keeps those about this deal or about no deal in particular. RelationshipState
// and the open transition are account-level (ADR-0012) and are passed through.
func FromOpportunityState(st reducer.OpportunityState, now time.Time, signals []Signal, relationshipState string, open *Transition) Situation {
	var own []Signal
	for _, sig := range signals {
		if sig.OpportunityID == nil || *sig.OpportunityID == st.OpportunityID {
			own = append(own, sig)
		}
	}
	return situationOf(st.AccountID, st.OpportunityID, &st.Fields.Fields, st.CoverageGaps, st.BuyingGroup, now, own, relationshipState, open)
}

func situationOf(accountID, opportunityID string, fields *reducer.Fields, gapNames []string, group []reducer.Member,
	now time.Time, signals []Signal, relationshipState string, open *Transition) Situation {
	s := Situation{
		AccountID: accountID, OpportunityID: opportunityID, Now: now, Fields: map[string]Value{}, Conflicts: map[string][]string{},
		RelationshipState: relationshipState, Transition: open, Signals: append([]Signal(nil), signals...),
	}
	for _, name := range reducer.FieldNames() {
		f := fields.Field(name)
		s.Fields[name] = valueOf(name, *f)
		for _, c := range f.Conflicts {
			s.Conflicts[name] = append(s.Conflicts[name], c.ClaimID)
		}
	}
	gaps := make([]Item, 0, len(gapNames))
	for _, g := range gapNames {
		gaps = append(gaps, Item{Text: g})
	}
	s.Fields["coverage_gaps"] = Value{Known: true, List: true, Items: gaps}
	for _, m := range group {
		s.BuyingGroup = append(s.BuyingGroup, Member{PersonID: m.PersonID, Roles: append([]string(nil), m.Roles...), Status: m.Status})
	}
	return s
}

func valueOf(name string, f reducer.Field) Value {
	v := Value{Known: f.Known, List: listFieldNames[name], EvidenceRefs: refsOf(f.EvidenceRefs)}
	if listFieldNames[name] {
		for _, it := range f.ListItems() {
			v.Items = append(v.Items, Item{Text: it.Text, Status: it.Status, ClaimID: it.ClaimID, OwnerPersonID: it.OwnerPersonID})
		}
		return v
	}
	if s, ok := f.Value.(string); ok && s == claims.Unknown {
		v.Known = false
		return v
	}
	v.Scalar = f.Value
	return v
}

func refsOf(in []reducer.EvidenceRef) []EvidenceRef {
	out := make([]EvidenceRef, 0, len(in))
	for _, r := range in {
		if r.ActivityID != "" {
			out = append(out, EvidenceRef{ActivityID: r.ActivityID, ClaimID: r.ClaimID})
		}
	}
	return out
}
