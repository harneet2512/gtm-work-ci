package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// ErrInvalidCondition is wrapped by every condition error: a malformed condition never evaluates to false.
var ErrInvalidCondition = errors.New("invalid knowledge condition")

// Condition ops (knowledge.v1.json#/$defs/condition).
const (
	OpEq        = "eq"
	OpNeq       = "neq"
	OpIn        = "in"
	OpContains  = "contains"
	OpExists    = "exists"
	OpNotExists = "not_exists"
	OpIsUnknown = "is_unknown"
)

// namespace kinds a field resolves to.
const (
	nsScalar     = "scalar"
	nsList       = "list"
	nsSignal     = "signal"
	nsRole       = "role"
	nsRelState   = "relationship_state"
	nsTransition = "transition"
)

// allowedOps lists the ops each namespace supports (ADR-0013).
var allowedOps = map[string][]string{
	nsScalar:     {OpEq, OpNeq, OpIn, OpContains, OpExists, OpNotExists, OpIsUnknown},
	nsList:       {OpContains, OpExists, OpNotExists, OpIsUnknown},
	nsSignal:     {OpExists, OpNotExists},
	nsRole:       {OpEq, OpIn, OpExists, OpNotExists},
	nsRelState:   {OpEq, OpNeq, OpIn, OpExists, OpNotExists, OpIsUnknown},
	nsTransition: {OpEq, OpNeq, OpIn, OpExists, OpNotExists},
}

var transitionAttrs = map[string]bool{"from_state": true, "to_state": true, "status": true}

// namespaceOf resolves a condition field to its namespace and the name inside it.
func namespaceOf(field string, stateFields map[string]bool) (ns, name string, err error) {
	switch {
	case strings.HasPrefix(field, "diff."):
		name = strings.TrimPrefix(field, "diff.")
		if !isSignalType(name) {
			return "", "", fmt.Errorf("%w: unknown signal type in %q", ErrInvalidCondition, field)
		}
		return nsSignal, name, nil
	case strings.HasPrefix(field, "buying_group."):
		name = strings.TrimPrefix(field, "buying_group.")
		if !buyingGroupRoles[name] {
			return "", "", fmt.Errorf("%w: unknown buying-group role in %q", ErrInvalidCondition, field)
		}
		return nsRole, name, nil
	case strings.HasPrefix(field, "transition."):
		name = strings.TrimPrefix(field, "transition.")
		if !transitionAttrs[name] {
			return "", "", fmt.Errorf("%w: unknown transition attribute in %q", ErrInvalidCondition, field)
		}
		return nsTransition, name, nil
	case field == "relationship_state":
		return nsRelState, field, nil
	case listFieldNames[field]:
		return nsList, field, nil
	case stateFields[field]:
		return nsScalar, field, nil
	}
	return "", "", fmt.Errorf("%w: unknown field %q", ErrInvalidCondition, field)
}

// validate checks the op against the namespace and the value's shape.
func validate(c Condition, ns string) error {
	if !hasString(allowedOps[ns], c.Op) {
		return fmt.Errorf("%w: op %q is not supported on %q", ErrInvalidCondition, c.Op, c.Field)
	}
	switch c.Op {
	case OpExists, OpNotExists, OpIsUnknown:
		if c.Value != nil {
			return fmt.Errorf("%w: %s on %q takes no value", ErrInvalidCondition, c.Op, c.Field)
		}
	case OpEq, OpNeq:
		if !isScalar(c.Value) {
			return fmt.Errorf("%w: %s on %q needs a scalar value", ErrInvalidCondition, c.Op, c.Field)
		}
	case OpIn:
		list, ok := c.Value.([]any)
		if !ok || len(list) == 0 || !allScalar(list) {
			return fmt.Errorf("%w: in on %q needs a non-empty list of scalars", ErrInvalidCondition, c.Field)
		}
	case OpContains:
		p, err := patternOf(c.Value)
		if err != nil {
			return fmt.Errorf("%w: contains on %q: %v", ErrInvalidCondition, c.Field, err)
		}
		if ns != nsList && (p.statuses != nil || p.text == "") {
			return fmt.Errorf("%w: contains on scalar %q takes text only (no item status)", ErrInvalidCondition, c.Field)
		}
	}
	return nil
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, int:
		return true
	}
	return false
}

func allScalar(list []any) bool {
	for _, v := range list {
		if !isScalar(v) {
			return false
		}
	}
	return true
}

// sameScalar compares two scalars: strings by claims.ItemKey (case and whitespace insensitive),
// numbers numerically, booleans exactly.
func sameScalar(a, b any) bool {
	sa, aok := a.(string)
	sb, bok := b.(string)
	if aok && bok {
		return claims.ItemKey(sa) == claims.ItemKey(sb)
	}
	fa, aok := number(a)
	fb, bok := number(b)
	if aok && bok {
		return fa == fb
	}
	ba, aok := a.(bool)
	bb, bok := b.(bool)
	return aok && bok && ba == bb
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func inList(v any, list []any) bool {
	for _, x := range list {
		if sameScalar(v, x) {
			return true
		}
	}
	return false
}

// itemPattern is a contains value: a text substring and/or a status set.
type itemPattern struct {
	text     string
	statuses []string
}

func patternOf(v any) (itemPattern, error) {
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return itemPattern{text: claims.ItemKey(s)}, nil
	}
	obj, ok := v.(map[string]any)
	if !ok || len(obj) == 0 {
		return itemPattern{}, errors.New("value must be a non-empty string or item pattern")
	}
	var p itemPattern
	for key, raw := range obj {
		switch key {
		case "text":
			s, ok := raw.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return itemPattern{}, errors.New("item pattern text must be a non-empty string")
			}
			p.text = claims.ItemKey(s)
		case "status":
			statuses, err := statusesOf(raw)
			if err != nil {
				return itemPattern{}, err
			}
			p.statuses = statuses
		default:
			return itemPattern{}, fmt.Errorf("unknown item pattern key %q", key)
		}
	}
	return p, nil
}

func statusesOf(raw any) ([]string, error) {
	if s, ok := raw.(string); ok {
		raw = []any{s}
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, errors.New("item pattern status must be a status or a non-empty list of statuses")
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, errors.New("item pattern status must be strings")
		}
		out = append(out, s)
	}
	for _, s := range out {
		if !itemStatuses[s] {
			return nil, fmt.Errorf("unknown item status %q", s)
		}
	}
	return out, nil
}

// itemStatuses mirrors account_state.v1.json list item status (parity-tested).
var itemStatuses = map[string]bool{"open": true, "resolved": true, "overdue": true, "fulfilled": true}

func (p itemPattern) matches(it Item) bool {
	if p.text != "" && !strings.Contains(claims.ItemKey(it.Text), p.text) {
		return false
	}
	return len(p.statuses) == 0 || hasString(p.statuses, it.Status)
}

// render is the human-readable form used in matched_conditions / unmatched_conditions.
func render(c Condition) string {
	if c.Value == nil {
		return c.Field + " " + c.Op
	}
	b, err := json.Marshal(c.Value)
	if err != nil {
		return fmt.Sprintf("%s %s %v", c.Field, c.Op, c.Value)
	}
	return c.Field + " " + c.Op + " " + string(b)
}
