package knowledge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// env is one prepared situation: the situation plus its open signals, computed once.
type env struct {
	s           Situation
	open        map[string][]Signal
	stateFields map[string]bool
}

func newEnv(s Situation) env {
	return env{s: s, open: openSignals(s), stateFields: stateFieldNames()}
}

// holds evaluates one condition. It returns the evidence behind a true result.
func (e env) holds(c Condition) (bool, []EvidenceRef, error) {
	ns, name, err := namespaceOf(c.Field, e.stateFields)
	if err != nil {
		return false, nil, err
	}
	if err := validate(c, ns); err != nil {
		return false, nil, err
	}
	switch ns {
	case nsSignal:
		return e.signalHolds(c, name)
	case nsTopic:
		in := hasString(e.s.Topics, name)
		return in == (c.Op == OpExists), nil, nil
	case nsRole:
		return e.roleHolds(c, name), nil, nil
	case nsRelState:
		return scalarHolds(c, relationshipValue(e.s.RelationshipState)), nil, nil
	case nsTransition:
		return scalarHolds(c, transitionValue(e.s.Transition, name)), nil, nil
	}
	v := e.s.Fields[name]
	isList := ns == nsList
	if v.Known && v.List != isList {
		return false, nil, fmt.Errorf("%w: field %q is %s in the situation", ErrInvalidSituation, name, shapeName(v.List))
	}
	ok := scalarHolds(c, v)
	if isList {
		ok = listHolds(c, v)
	}
	if !ok {
		return false, nil, nil
	}
	return true, v.EvidenceRefs, nil
}

// ErrInvalidSituation is returned when the situation's data contradicts the field vocabulary.
var ErrInvalidSituation = errors.New("invalid situation")

func shapeName(list bool) string {
	if list {
		return "a list"
	}
	return "a scalar"
}

func (e env) signalHolds(c Condition, signalType string) (bool, []EvidenceRef, error) {
	open := e.open[signalType]
	if c.Op == OpNotExists {
		return len(open) == 0, nil, nil
	}
	var refs []EvidenceRef
	for _, sig := range open {
		refs = append(refs, sig.EvidenceRefs...)
	}
	return len(open) > 0, refs, nil
}

func (e env) roleHolds(c Condition, role string) bool {
	switch c.Op {
	case OpExists, OpNotExists:
		engaged := false
		for _, m := range e.s.BuyingGroup {
			if hasString(m.Roles, role) && !disengagedStatuses[m.Status] {
				engaged = true
			}
		}
		return engaged == (c.Op == OpExists)
	}
	for _, m := range e.s.BuyingGroup {
		if !hasString(m.Roles, role) {
			continue
		}
		if (c.Op == OpEq && sameScalar(m.Status, c.Value)) || (c.Op == OpIn && inList(m.Status, c.Value.([]any))) {
			return true
		}
	}
	return false
}

func relationshipValue(state string) Value {
	if state == "" || state == claims.Unknown {
		return Value{}
	}
	return Value{Known: true, Scalar: state}
}

func transitionValue(t *Transition, attr string) Value {
	if t == nil {
		return Value{}
	}
	v := map[string]string{"from_state": t.FromState, "to_state": t.ToState, "status": t.Status}[attr]
	if v == "" {
		return Value{Known: true, Scalar: nil}
	}
	return Value{Known: true, Scalar: v}
}

// scalarHolds applies an op to a scalar value (ADR-0013 table).
func scalarHolds(c Condition, v Value) bool {
	switch c.Op {
	case OpIsUnknown:
		return !v.Known
	case OpExists:
		return exists(v)
	case OpNotExists:
		return !exists(v)
	}
	if !v.Known || v.Scalar == nil {
		return false
	}
	switch c.Op {
	case OpEq:
		return sameScalar(v.Scalar, c.Value)
	case OpNeq:
		return !sameScalar(v.Scalar, c.Value)
	case OpIn:
		return inList(v.Scalar, c.Value.([]any))
	case OpContains:
		s, ok := v.Scalar.(string)
		p, _ := patternOf(c.Value)
		return ok && p.statuses == nil && strings.Contains(claims.ItemKey(s), p.text)
	}
	return false
}

func listHolds(c Condition, v Value) bool {
	switch c.Op {
	case OpIsUnknown:
		return !v.Known
	case OpExists:
		return exists(v)
	case OpNotExists:
		return !exists(v)
	}
	if !v.Known {
		return false
	}
	p, _ := patternOf(c.Value)
	for _, it := range v.Items {
		if p.matches(it) {
			return true
		}
	}
	return false
}

// exists: known, non-null and, for lists and strings, non-empty.
func exists(v Value) bool {
	if !v.Known {
		return false
	}
	if v.List {
		return len(v.Items) > 0
	}
	if s, ok := v.Scalar.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return v.Scalar != nil
}
