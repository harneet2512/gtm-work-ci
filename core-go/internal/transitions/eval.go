package transitions

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Condition operators of the rule grammar.
const (
	opEq       = "eq"
	opNeq      = "neq"
	opIn       = "in"
	opKnown    = "known"
	opUnknown  = "is_unknown"
	opHasItem  = "has_item"
	opAgeGTE   = "age_days_gte"
	opAsserted = "asserted_within_days"
	opFired    = "fired_within_days"
	opExists   = "exists"
	opNotExist = "not_exists"
)

// openItemStatuses are the commitment statuses that count as open (a missing status is open).
var openItemStatuses = []string{"", "open", "overdue"}

// match is the result of one condition: whether it holds, the evidence it rests on and the signals it matched.
type match struct {
	held    bool
	refs    []Ref
	signals []string
}

// evaluator evaluates conditions and facts of one rule set against one Input.
type evaluator struct {
	rs       RuleSet
	in       Input
	anchor   *time.Time
	champion string
}

func newEvaluator(rs RuleSet, in Input) *evaluator {
	e := &evaluator{rs: rs, in: in, anchor: in.State.RelationshipState.ConfirmedAt}
	if e.anchor == nil && (in.State.RelationshipState.Value == "" || in.State.RelationshipState.Value == "unknown") {
		var beginning time.Time // an account that never confirmed a state has no anchor and no lower bound
		e.anchor = &beginning
	}
	if f, ok := in.State.Fields["champion"]; ok {
		_ = json.Unmarshal(f.Value, &e.champion) // a non-string value leaves champion empty: nobody matches
	}
	return e
}

func (e *evaluator) window(name string) time.Duration {
	return time.Duration(e.rs.days(name)) * 24 * time.Hour
}

// inWindow: the moment is not in the future, within the threshold for *_within_days ops, and
// strictly after the anchor when the condition has a "since".
func (e *evaluator) inWindow(at time.Time, c Condition) bool {
	if at.After(e.in.Now) {
		return false
	}
	if (c.Op == opFired || c.Op == opAsserted) && e.in.Now.Sub(at) > e.window(c.Threshold) {
		return false
	}
	return c.Since == "" || (e.anchor != nil && at.After(*e.anchor))
}

// about checks a claim's or signal's subject against the current champion.
func (e *evaluator) about(c Condition, subject string) bool {
	if c.About == "" {
		return true
	}
	isChampion := subject != "" && subject == e.champion
	if c.About == "champion" {
		return isChampion
	}
	return !isChampion
}

func compare(c Condition, value any) bool {
	var want any
	if len(c.Value) > 0 {
		_ = json.Unmarshal(c.Value, &want)
	}
	switch c.Op {
	case opEq:
		return reflect.DeepEqual(value, want)
	case opNeq:
		return !reflect.DeepEqual(value, want)
	case opIn:
		list, _ := want.([]any)
		return slices.ContainsFunc(list, func(w any) bool { return reflect.DeepEqual(value, w) })
	}
	return false
}

func (e *evaluator) state(c Condition, name string, earning bool) match {
	f, ok := e.in.State.Fields[name]
	if !ok {
		f = FieldView{}
	}
	if strings.HasSuffix(c.Path, ".standing") {
		var standing any
		if f.Standing != nil {
			standing = *f.Standing
		}
		return match{held: f.Known && compare(c, standing), refs: f.EvidenceRefs}
	}
	if earning && f.Known && f.Standing != nil && !slices.Contains(e.rs.ClaimStandings, *f.Standing) {
		return match{} // a field won by third-party enrichment never earns a state
	}
	if c.Op == opUnknown {
		return match{held: !f.Known}
	}
	if !f.Known {
		return match{}
	}
	switch c.Op {
	case opHasItem:
		return e.hasItem(c, f)
	case opAgeGTE:
		var at time.Time
		if err := json.Unmarshal(f.Value, &at); err != nil {
			return match{}
		}
		return match{held: e.in.Now.Sub(at) >= e.window(c.Threshold), refs: f.EvidenceRefs}
	}
	if (c.Since != "" || c.Op == opAsserted) && !(f.AsOf != nil && e.inWindow(*f.AsOf, c)) {
		return match{}
	}
	if c.Op == opKnown || c.Op == opAsserted {
		return match{held: true, refs: f.EvidenceRefs}
	}
	var value any
	_ = json.Unmarshal(f.Value, &value)
	return match{held: compare(c, value), refs: f.EvidenceRefs}
}

func (e *evaluator) hasItem(c Condition, f FieldView) match {
	var items []ItemView
	if err := json.Unmarshal(f.Value, &items); err != nil {
		return match{}
	}
	var want string
	_ = json.Unmarshal(c.Value, &want)
	var out match
	for _, it := range items {
		if it.Kind != want || !slices.Contains(openItemStatuses, it.Status) {
			continue
		}
		if c.Since != "" && !slices.ContainsFunc(it.EvidenceRefs, func(r Ref) bool { return r.OccurredAt != nil && e.inWindow(*r.OccurredAt, c) }) {
			continue
		}
		out.held = true
		out.refs = append(out.refs, it.EvidenceRefs...)
	}
	return out
}

func (e *evaluator) diff(c Condition, name string, earning bool) match {
	var hits []Signal
	anyOpen := false
	for _, s := range e.in.Signals {
		if s.SignalType != name {
			continue
		}
		anyOpen = anyOpen || s.Open
		if c.Op == opNotExist {
			continue
		}
		fits := false
		if c.Op == opExists {
			fits = s.Open && (c.Since == "" || e.inWindow(s.OccurredAt, c))
		} else {
			fits = e.inWindow(s.OccurredAt, c)
		}
		if fits && e.about(c, s.SubjectPersonID) && (!earning || s.FirstParty == nil || *s.FirstParty) {
			hits = append(hits, s)
		}
	}
	if c.Op == opNotExist {
		return match{held: !anyOpen}
	}
	var out match
	out.held = len(hits) > 0
	for _, s := range hits {
		out.refs = append(out.refs, s.EvidenceRefs...)
		out.signals = append(out.signals, s.ID)
	}
	return out
}

func (e *evaluator) claim(c Condition, name string) match {
	var out match
	for _, cl := range e.in.Claims {
		standing := cl.Standing
		if standing == "" {
			standing = "first_party_ai"
		}
		stance := c.Stance
		if stance == "" {
			stance = "stated"
		}
		if cl.FieldPath != name || !slices.Contains(e.rs.ClaimStatuses, cl.Status) || cl.Withdrawn != (stance == "withdrawn") || !slices.Contains(e.rs.ClaimStandings, standing) ||
			!e.inWindow(cl.OccurredAt, c) || !e.about(c, cl.SubjectPersonID) {
			continue
		}
		out.held = true
		out.refs = append(out.refs, Ref{ActivityID: cl.SourceActivityID, ClaimID: cl.ID})
	}
	return out
}

func (e *evaluator) members(c Condition, role string) match {
	var out match
	for _, m := range e.in.State.BuyingGroup {
		// a tenure test looks at every member holding the role except a departed one, whatever its engagement status
		usable := slices.Contains(e.rs.BuyingGroupMemberStatuses, m.Status)
		if _, tenure := c.Where["tenure"]; tenure {
			usable = m.Status != "departed"
		}
		if !slices.Contains(m.Roles, role) || !usable || !whereHolds(c.Where, m) {
			continue
		}
		out.held = true
		out.refs = append(out.refs, m.EvidenceRefs...)
	}
	if c.Op == opNotExist {
		return match{held: !out.held}
	}
	return out
}

func whereHolds(where map[string]string, m MemberView) bool {
	for k, v := range where {
		tenure := m.Tenure
		if tenure == "" {
			tenure = "unknown"
		}
		if k != "tenure" || tenure != v {
			return false
		}
	}
	return true
}

func (e *evaluator) condition(c Condition, earning bool) match {
	kind, rest, _ := strings.Cut(c.Path, ".")
	switch kind {
	case "state":
		name, _, _ := strings.Cut(rest, ".")
		return e.state(c, name, earning)
	case "diff":
		return e.diff(c, rest, earning)
	case "claim":
		return e.claim(c, rest)
	case "buying_group":
		return e.members(c, rest)
	}
	return match{}
}

// fact evaluates a fact the transition rests on: the first group whose conditions all hold satisfies it.
// Only first-party signals can satisfy it.
func (e *evaluator) fact(f Fact) FactResult { return e.evaluateFact(f, true) }

func (e *evaluator) evaluateFact(f Fact, earning bool) FactResult {
	res := FactResult{Key: f.Key, Description: f.Description, Required: f.Required, EvidenceRefs: []Ref{}, SignalIDs: []string{}}
	for _, g := range f.AnyOf {
		var refs []Ref
		var sigs []string
		held := true
		for _, c := range g.AllOf {
			m := e.condition(c, earning)
			if !m.held {
				held = false
				break
			}
			refs, sigs = append(refs, m.refs...), append(sigs, m.signals...)
		}
		if held && len(uniqueRefs(refs)) > 0 { // a group that cites no evidence does not satisfy the fact
			res.Satisfied = true
			res.EvidenceRefs = uniqueRefs(refs)
			res.SignalIDs = uniqueStrings(sigs)
			return res
		}
	}
	return res
}

// contradiction evaluates a contradiction: a fact that also says whether it is decisive.
func (e *evaluator) contradiction(f Fact) FactResult {
	res := e.evaluateFact(f, false) // every signal counts: third-party evidence can only hold a transition back
	rejects := f.Rejects
	res.Rejects = &rejects
	return res
}

func uniqueRefs(in []Ref) []Ref {
	out := []Ref{}
	for _, r := range in {
		if !slices.ContainsFunc(out, func(o Ref) bool { return refKey(o) == refKey(r) }) {
			out = append(out, r)
		}
	}
	return out
}

func refKey(r Ref) [4]string {
	at := ""
	if r.OccurredAt != nil {
		at = r.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return [4]string{r.ActivityID, r.ClaimID, r.Quote + "\x00" + r.SpeakerPersonID, at}
}

func uniqueStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
