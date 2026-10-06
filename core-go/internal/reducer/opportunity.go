package reducer

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// OpportunityFields are the deal-scoped fields: the twenty AccountState fields, each describing this deal,
// plus the deal's amount (opportunity_state.v1.json properties.fields).
type OpportunityFields struct {
	Fields
	Amount Field `json:"amount"`
}

// Field returns the field with the given schema name (including "amount"), or nil.
func (f *OpportunityFields) Field(name string) *Field {
	if name == "amount" {
		return &f.Amount
	}
	return f.Fields.Field(name)
}

// OpportunityState is the projection of one deal, stored in opportunity_state.state and
// opportunity_state_history.state (contracts/schemas/opportunity_state.v1.json, ADR-0016).
type OpportunityState struct {
	AccountID      string            `json:"account_id"`
	OpportunityID  string            `json:"opportunity_id"`
	Version        int               `json:"version"`
	AsOf           time.Time         `json:"as_of"`
	ComputedAt     time.Time         `json:"computed_at"`
	LastActivityID *string           `json:"last_activity_id"`
	IsOpen         bool              `json:"is_open"`
	Fields         OpportunityFields `json:"fields"`
	BuyingGroup    []Member          `json:"buying_group"`
	CoverageGaps   []string          `json:"coverage_gaps"`

	// evidenceAt is when the deal last had evidence (its latest activity, else its latest claim); it ranks the
	// deals. as_of equals it except for a deal without activities, whose as_of is computed_at like an
	// account's, so that overdue items are judged against now.
	evidenceAt time.Time
}

// OpportunitySummary is one entry of AccountState.opportunities.
type OpportunitySummary struct {
	OpportunityID  string    `json:"opportunity_id"`
	IsOpen         bool      `json:"is_open"`
	IsPrimary      bool      `json:"is_primary"`
	Stage          string    `json:"stage"`
	Owner          string    `json:"owner"`
	Amount         *float64  `json:"amount"`
	Health         string    `json:"health"`
	AsOf           time.Time `json:"as_of"`
	LastActivityID *string   `json:"last_activity_id"`

	evidenceAt time.Time // ordering key, see OpportunityState.evidenceAt
}

// Result is what one fold of an account yields: the account roll-up and one state per deal.
type Result struct {
	Account       AccountState
	Opportunities []OpportunityState // ordered by opportunity id
	Warnings      []string
}

// ReduceAll folds every deal of the account from its own claims and activities, then rolls them up into the
// AccountState. Deterministic: the same input always yields the same result, whatever the input order.
func ReduceAll(in Input) Result {
	var res Result
	for _, opp := range opportunityIDs(in) {
		deal, warns := reduceDeal(in, opp)
		res.Opportunities = append(res.Opportunities, deal)
		res.Warnings = append(res.Warnings, warns...)
	}
	account, warns := reduceAccount(in, res.Opportunities)
	res.Account = account
	res.Warnings = uniqueSorted(append(res.Warnings, warns...))
	return res
}

// ReduceOpportunity folds one deal; the second result lists claims that could not be folded.
func ReduceOpportunity(in Input, opportunityID string) (OpportunityState, []string) {
	return reduceDeal(in, opportunityID)
}

// opportunityIDs lists, sorted, the deals with evidence: a scoped claim or an activity tied to the deal.
func opportunityIDs(in Input) []string {
	set := map[string]bool{}
	for _, s := range in.Adjudication.Scopes() {
		set[s] = true
	}
	for _, a := range in.Activities {
		if a.OpportunityID != "" {
			set[a.OpportunityID] = true
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func reduceDeal(in Input, opp string) (OpportunityState, []string) {
	full := in.Adjudication
	acts := dealActivities(in.Activities, opp)
	people := dealPeople(full, opp, acts)
	view := full.View(opp, func(s claims.Slot) bool { return people[slotPerson(s)] })
	r, last := newRun(in, view, acts, in.ComputedAt)

	st := OpportunityState{AccountID: in.AccountID, OpportunityID: opp, ComputedAt: in.ComputedAt.UTC(), AsOf: r.asOf.UTC(), evidenceAt: r.asOf}
	if last == nil {
		st.evidenceAt = claimsAsOf(view, in.ComputedAt)
	}
	if last != nil {
		st.LastActivityID = ptr(last.ID)
	}
	st.Fields.Fields = r.foldFields()
	st.Fields.Amount = scalarField(view.Find(claims.FieldAmount, "", ""))
	surfaceUnattributed(&st.Fields.Fields, view, full)
	st.IsOpen = !isClosed(st.Fields.Stage)
	st.BuyingGroup, st.CoverageGaps = r.buyingGroup()
	return st, r.warnings
}

func dealActivities(all []Activity, opp string) []Activity {
	var out []Activity
	for _, a := range all {
		if a.OpportunityID == opp {
			out = append(out, a)
		}
	}
	return out
}

// dealPeople is who took part in the deal: participants of its activities and subjects of its scoped
// claims. Person-scoped facts (title, delegation, presence) enter a deal's view only for these people.
func dealPeople(full claims.Adjudication, opp string, acts []Activity) map[string]bool {
	set := map[string]bool{}
	for _, a := range acts {
		for _, p := range a.Participants {
			set[p.PersonID] = true
		}
	}
	for _, s := range full.Slots {
		if s.Scope == opp {
			set[s.Subject] = true
		}
	}
	delete(set, "")
	return set
}

// slotPerson is the person a person-scoped slot is about.
func slotPerson(s claims.Slot) string {
	if s.Subject == "" && s.Field == claims.FieldDelegation && s.Winner != nil {
		if d, err := claims.ParseDelegation(s.Winner.Value); err == nil {
			return d.FromPersonID
		}
	}
	return s.Subject
}

// claimsAsOf is the latest claim time in the view, or the fallback when it has no claims.
func claimsAsOf(view claims.Adjudication, fallback time.Time) time.Time {
	var latest time.Time
	for _, s := range view.Slots {
		if s.Scope != view.Scope { // account-wide person facts come from other deals' evidence too
			continue
		}
		all := append(append([]claims.Claim{}, s.Competing...), s.Suggested...)
		if s.Winner != nil {
			all = append(all, *s.Winner)
		}
		for _, c := range all {
			if c.OccurredAt.After(latest) {
				latest = c.OccurredAt
			}
		}
	}
	if latest.IsZero() {
		return fallback
	}
	return latest
}

// surfaceUnattributed lists, as conflicts of a deal's scalar field, the account-scoped winner (a claim
// that names no deal) when it is newer than the deal's value and says something different. It never
// wins: an unattributed claim could be about any deal. Standing does not matter here (ADR-0016).
func surfaceUnattributed(f *Fields, deal, full claims.Adjudication) {
	for _, sf := range scalarFields {
		field := f.Field(sf.name)
		ds, as := deal.Find(sf.path, "", ""), full.Find(sf.path, "", "")
		if !field.Known || field.AsOf == nil || ds == nil || ds.Winner == nil || as == nil || as.Winner == nil {
			continue
		}
		w := as.Winner
		if claims.IsNull(w.Value) || claims.IsUnknown(w.Value) || !w.OccurredAt.After(*field.AsOf) || claims.SameValue(w.Value, ds.Winner.Value) {
			continue
		}
		// Compare what the state would show: two spellings of one enum value agree, and a value that maps to
		// no enum value shows nothing to contradict.
		if shown, mappable := fieldValue(*w); !mappable || reflect.DeepEqual(shown, field.Value) {
			continue
		}
		reason := fmt.Sprintf("unattributed %s claim (%s) says %s; this deal's value stands until a rep confirms which deal it is about",
			w.Standing, w.OccurredAt.UTC().Format("2006-01-02"), claims.CanonicalValue(w.Value))
		field.Conflicts = append(field.Conflicts, FieldConflict{ClaimID: w.ID, Standing: string(w.Standing), Reason: truncate(reason, 500)})
		field.CompetingClaimIDs = append(field.CompetingClaimIDs, w.ID)
	}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// closedWords are the first words of a stage that ends a deal ("Closed Won", "Closed-Lost", "Won", "Lost",
// "Churned"). Other CRMs spell the end of a deal without the word "closed", so the prefix alone is not enough.
var closedWords = map[string]bool{"closed": true, "won": true, "lost": true, "churned": true}

// stageOrder ranks the pipeline stages of the world's stage vocabulary (account_state stage values;
// the same order signals.stageMoved evaluates). A stage outside the table has no rank: moves to or
// from it cannot be called an advance or a regression.
var stageOrder = map[string]int{
	"discovery": 1, "commercial review": 2, "technical evaluation": 3, "negotiation": 4, "closed won": 5,
}

// StageRank returns the stage's rank in the pipeline order (case-insensitive); ok is false for a stage
// outside the known vocabulary — callers must then not claim a direction.
func StageRank(stage string) (int, bool) {
	r, ok := stageOrder[strings.ToLower(strings.TrimSpace(stage))]
	return r, ok
}

// StageClosed reports whether a stage value ends a deal: its first word is one of closedWords, case-insensitive.
func StageClosed(stage string) bool {
	words := strings.FieldsFunc(strings.ToLower(stage), func(r rune) bool { return r == ' ' || r == '-' || r == '_' || r == ':' })
	return len(words) > 0 && closedWords[words[0]]
}

// StageWon reports whether a closed stage is a win ("Closed Won", "Won"); a closed deal that is not won is lost.
func StageWon(stage string) bool {
	return StageClosed(stage) && strings.Contains(strings.ToLower(stage), "won")
}

// isClosed: a known stage that ends the deal (StageClosed); an unknown stage is open.
func isClosed(stage Field) bool {
	s, ok := stage.Value.(string)
	return stage.Known && ok && StageClosed(s)
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
