package claims

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Statuses a list item may carry (account_state.v1.json#listField).
const (
	ItemOpen      = "open"
	ItemResolved  = "resolved"
	ItemOverdue   = "overdue"
	ItemFulfilled = "fulfilled"
)

var itemStatuses = map[string]bool{ItemOpen: true, ItemResolved: true, ItemOverdue: true, ItemFulfilled: true}

// MustJSON marshals v and panics on failure; it is for values built from known-good literals.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("claims: marshal %T: %v", v, err))
	}
	return b
}

// IsNull reports whether raw is JSON null (or empty): "known absent".
func IsNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// IsUnknown reports whether raw is the legal string "unknown".
func IsUnknown(raw json.RawMessage) bool {
	s, ok := StringValue(raw)
	return ok && strings.EqualFold(strings.TrimSpace(s), Unknown)
}

// StringValue returns the string held by raw.
func StringValue(raw json.RawMessage) (string, bool) {
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '"' { // json.Unmarshal would accept null as ""
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// CanonicalValue re-encodes raw with sorted object keys and no insignificant whitespace.
// Input that is not valid JSON is returned unchanged.
func CanonicalValue(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// SameValue reports whether two claim values assert the same thing: strings compare
// case-insensitively with collapsed whitespace, everything else canonically.
func SameValue(a, b json.RawMessage) bool {
	sa, oka := StringValue(a)
	sb, okb := StringValue(b)
	if oka && okb {
		return ItemKey(sa) == ItemKey(sb)
	}
	return CanonicalValue(a) == CanonicalValue(b)
}

// ItemKey normalizes list-item text so that the same item asserted twice folds together.
func ItemKey(text string) string {
	t := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimRight(t, ".;,")
}

// ListItem is one entry of a list field (blockers, objections, decision criteria, commitments).
type ListItem struct {
	Text          string
	Status        string
	DueAt         *time.Time
	OwnerPersonID string
}

// ParseListItem accepts either an "open:" / "resolved:" / "overdue:" / "fulfilled:" prefixed
// string (no prefix means open) or an object {text, status, due_at, owner_person_id}.
func ParseListItem(raw json.RawMessage) (ListItem, error) {
	if s, ok := StringValue(raw); ok {
		return parseListString(s)
	}
	var obj struct {
		Text          string  `json:"text"`
		Status        string  `json:"status"`
		DueAt         *string `json:"due_at"`
		OwnerPersonID string  `json:"owner_person_id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ListItem{}, fmt.Errorf("claims: list item must be a string or an object: %w", err)
	}
	item := ListItem{Text: strings.TrimSpace(obj.Text), Status: strings.ToLower(strings.TrimSpace(obj.Status)), OwnerPersonID: obj.OwnerPersonID}
	if item.Text == "" {
		return ListItem{}, errors.New("claims: list item has no text")
	}
	if item.Status == "" {
		item.Status = ItemOpen
	}
	if !itemStatuses[item.Status] {
		return ListItem{}, fmt.Errorf("claims: list item status %q is not one of open, resolved, overdue, fulfilled", obj.Status)
	}
	if obj.DueAt != nil && *obj.DueAt != "" {
		due, err := time.Parse(time.RFC3339, *obj.DueAt)
		if err != nil {
			return ListItem{}, fmt.Errorf("claims: list item due_at: %w", err)
		}
		due = due.UTC()
		item.DueAt = &due
	}
	return item, nil
}

func parseListString(s string) (ListItem, error) {
	text, status := strings.TrimSpace(s), ItemOpen
	if head, rest, ok := strings.Cut(text, ":"); ok {
		if candidate := strings.ToLower(strings.TrimSpace(head)); itemStatuses[candidate] {
			status, text = candidate, strings.TrimSpace(rest)
		}
	}
	if text == "" {
		return ListItem{}, errors.New("claims: list item has no text")
	}
	return ListItem{Text: text, Status: status}, nil
}

// Member is the attribute payload of a buying_group.member claim.
type Member struct {
	Role           string `json:"role,omitempty"`
	Title          string `json:"title,omitempty"`
	EmployerDomain string `json:"employer_domain,omitempty"`
}

// ParseMember reads a member value; anything that is not an object carries no attributes
// (the subject person alone says who the claim is about).
func ParseMember(raw json.RawMessage) Member {
	var m Member
	if err := json.Unmarshal(raw, &m); err != nil {
		return Member{}
	}
	m.Role, m.Title, m.EmployerDomain = strings.TrimSpace(m.Role), strings.TrimSpace(m.Title), strings.TrimSpace(m.EmployerDomain)
	return m
}

// Delegation is the value of a delegation claim: ownership of the evaluation moves from one
// buying-group member to another.
type Delegation struct {
	FromPersonID string `json:"from_person_id"`
	ToPersonID   string `json:"to_person_id"`
	Scope        string `json:"scope,omitempty"`
}

// ParseDelegation reads a delegation value and requires both ends.
func ParseDelegation(raw json.RawMessage) (Delegation, error) {
	var d Delegation
	if err := json.Unmarshal(raw, &d); err != nil {
		return Delegation{}, fmt.Errorf("claims: delegation must be an object: %w", err)
	}
	if d.FromPersonID == "" || d.ToPersonID == "" {
		return Delegation{}, errors.New("claims: delegation needs from_person_id and to_person_id")
	}
	return d, nil
}
