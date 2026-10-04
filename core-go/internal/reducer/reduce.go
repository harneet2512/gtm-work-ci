package reducer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// Person is what the fold needs to know about a person.
type Person struct {
	ID          string
	DisplayName string
	Title       string
	Kind        string // "contact" (customer side) or "employee"
}

// Participant is one party of an activity.
type Participant struct {
	PersonID    string
	RawIdentity string
	Role        string // activity_participants.role
}

// Activity is the slice of an activity the fold needs (derived fields, buying-group engagement).
type Activity struct {
	ID            string
	Type          string
	OccurredAt    time.Time
	OpportunityID string // "" when the activity is not tied to a deal
	Participants  []Participant
}

// Input is everything Reduce depends on; nothing else (no clock, no database) influences the state.
type Input struct {
	AccountID    string
	AccountName  string
	Version      int
	ComputedAt   time.Time
	Adjudication claims.Adjudication
	Activities   []Activity
	People       map[string]Person
	OwnDomain    string // our email domain; any other domain is a customer's
}

// scalarFields maps state fields onto the claim field path that feeds them.
var scalarFields = []struct {
	name string
	path claims.FieldPath
}{
	{"stage", claims.FieldStage}, {"health", claims.FieldHealth}, {"owner", claims.FieldOwner},
	{"motion", claims.FieldMotion}, {"champion", claims.FieldChampion}, {"champion_status", claims.FieldChampionStatus},
	{"economic_buyer", claims.FieldEconomicBuyer}, {"decision_process", claims.FieldDecisionProcess},
	{"next_milestone", claims.FieldNextMilestone}, {"next_meeting", claims.FieldNextMeeting},
	{"relationship_risk", claims.FieldRelationshipRisk}, {"product_use_case", claims.FieldProductUseCase},
	{"commercial_issue", claims.FieldCommercialIssue}, {"summary", claims.FieldSummary},
}

// listFields maps list state fields onto the claim field path of their items.
var listFields = []struct {
	name string
	path claims.FieldPath
}{
	{"blockers", claims.FieldBlockers}, {"objections", claims.FieldObjections},
	{"decision_criteria", claims.FieldDecisionCriteria}, {"current_commitments", claims.FieldCommitment},
}

// run carries the per-fold context.
type run struct {
	in       Input
	asOf     time.Time
	warnings []string
}

func (r *run) warn(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// Reduce folds the adjudicated claims and the activities into the account-level AccountState (ADR-0016):
// the roll-up of ReduceAll. The second result lists claims that could not be folded (for example a
// list item that is not a string or an object); the state is still complete without them.
func Reduce(in Input) (AccountState, []string) {
	res := ReduceAll(in)
	return res.Account, res.Warnings
}

// newRun starts a fold over one scope: its adjudication view and the activities that belong to it. The
// state's as_of is the latest of those activities, else the fallback.
func newRun(in Input, view claims.Adjudication, acts []Activity, fallback time.Time) (*run, *Activity) {
	in.Adjudication, in.Activities = view, acts
	r := &run{in: in, asOf: fallback}
	var last *Activity
	for i := range acts {
		a := &acts[i]
		if last == nil || a.OccurredAt.After(last.OccurredAt) || (a.OccurredAt.Equal(last.OccurredAt) && a.ID < last.ID) {
			last = a
		}
	}
	if last != nil {
		r.asOf = last.OccurredAt
	}
	return r, last
}

// foldFields folds the twenty state fields of the run's scope.
func (r *run) foldFields() Fields {
	var f Fields
	for _, sf := range scalarFields {
		*f.Field(sf.name) = scalarField(r.in.Adjudication.Find(sf.path, "", ""))
	}
	for _, lf := range listFields {
		*f.Field(lf.name) = r.listField(r.in.Adjudication.Items(lf.path))
	}
	f.ChampionSince = r.championSince()
	f.LastCustomerInteraction = r.lastCustomerInteraction()
	f.LastMeaningfulChange = r.lastMeaningfulChange()
	return f
}

func ptr[T any](v T) *T { return &v }

// unknownField is the legal "no evidence" value of a scalar field.
func unknownField() Field {
	return Field{Value: claims.Unknown, EvidenceRefs: []EvidenceRef{}, CompetingClaimIDs: []string{}, SuggestedClaimIDs: []string{}}
}

func evidence(c claims.Claim) EvidenceRef {
	return EvidenceRef{ActivityID: c.SourceActivityID, ClaimID: c.ID, Quote: c.EvidenceQuote, SpeakerPersonID: c.SpeakerPersonID, OccurredAt: c.OccurredAt.UTC()}
}

func claimIDs(cs []claims.Claim) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func decodeValue(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

// scalarField projects the winner of a scalar slot (nil slot = no claims at all).
func scalarField(s *claims.Slot) Field {
	f := unknownField()
	if s == nil {
		return f
	}
	f.SuggestedClaimIDs = claimIDs(s.Suggested)
	if s.Winner == nil {
		return f
	}
	w := *s.Winner
	f.CompetingClaimIDs = claimIDs(s.Competing)
	value, mappable := fieldValue(w)
	if claims.IsUnknown(w.Value) || !mappable {
		// The claim that decided "unknown" (or whose free text maps to no enum value) stays inspectable, but a
		// field without a value has no winner. The claim keeps its original text and evidence.
		f.CompetingClaimIDs = append([]string{w.ID}, f.CompetingClaimIDs...)
		return f
	}
	f.Value, f.Known = value, true
	f.WinningClaimID, f.Standing = ptr(w.ID), ptr(string(w.Standing))
	f.Confidence, f.AsOf = ptr(w.Confidence), ptr(w.OccurredAt.UTC())
	f.EvidenceRefs = []EvidenceRef{evidence(w)}
	f.Conflicts = fieldConflicts(s.Conflicts)
	return f
}

// fieldValue is the value a winning claim shows in the state: the claim's JSON value, or for the
// contract's closed enums (champion_status, relationship_risk) the normalized enum value. mappable is
// false when free text maps to no enum value.
func fieldValue(w claims.Claim) (value any, mappable bool) {
	if claims.IsEnumField(w.FieldPath) {
		v, ok := claims.NormalizeEnum(w.FieldPath, w.Value)
		if v == claims.Unknown {
			return v, false
		}
		return v, ok
	}
	return decodeValue(w.Value), true
}

func fieldConflicts(cs []claims.Conflict) []FieldConflict {
	var out []FieldConflict
	for _, c := range cs {
		out = append(out, FieldConflict{ClaimID: c.Contradicting.ID, Standing: string(c.Contradicting.Standing), Reason: c.Reason})
	}
	return out
}

// listField folds item slots into a list field. Overdue is derived: an open item whose due date
// precedes the state's as_of.
func (r *run) listField(slots []claims.Slot) Field {
	f := unknownField()
	f.Value = []Item{}
	var out []Item
	var asOf time.Time
	for _, s := range slots {
		f.SuggestedClaimIDs = append(f.SuggestedClaimIDs, claimIDs(s.Suggested)...)
		if s.Winner == nil {
			continue
		}
		parsed, err := claims.ParseListItem(s.Winner.Value)
		if err != nil {
			r.warn("claim %s (%s) is not a list item: %v", s.Winner.ID, s.Winner.FieldPath, err)
			continue
		}
		ref := evidence(*s.Winner)
		item := Item{Text: parsed.Text, ClaimID: s.Winner.ID, Status: parsed.Status, OwnerPersonID: parsed.OwnerPersonID, EvidenceRefs: []EvidenceRef{ref}}
		if parsed.DueAt != nil {
			item.DueAt = ptr(parsed.DueAt.UTC())
			if parsed.Status == claims.ItemOpen && parsed.DueAt.Before(r.asOf) {
				item.Status = claims.ItemOverdue
			}
		}
		out = append(out, item)
		f.EvidenceRefs = append(f.EvidenceRefs, ref)
		f.CompetingClaimIDs = append(f.CompetingClaimIDs, claimIDs(s.Competing)...)
		f.Conflicts = append(f.Conflicts, fieldConflicts(s.Conflicts)...)
		if s.Winner.OccurredAt.After(asOf) {
			asOf = s.Winner.OccurredAt
		}
	}
	if len(out) == 0 {
		return f
	}
	f.Value, f.Known, f.AsOf = out, true, ptr(asOf.UTC())
	return f
}

func joinSorted(names map[string]bool) string {
	list := make([]string, 0, len(names))
	for n := range names {
		list = append(list, n)
	}
	sort.Strings(list)
	return strings.Join(list, ", ")
}
