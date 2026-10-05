package biwriter

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Limits of business_intelligence_update.v1.json.
const (
	maxStatement = 500
	maxSummary   = 600
	maxWhy       = 1500
	maxValue     = 140 // one rendered value inside a statement
)

// Dimension names (common.v1.json#changeDimension).
const (
	dimStakeholders = "stakeholder_structure"
	dimOwnership    = "relationship_ownership"
	dimIntent       = "buyer_intent"
	dimBlockers     = "blockers_risk"
	dimNextStep     = "next_step_commitment"
)

// Names of the diff entries that are not AccountState fields (statediff).
const (
	fieldBuyingGroup       = "buying_group"
	fieldCoverageGaps      = "coverage_gaps"
	fieldRelationshipState = "relationship_state"
	fieldOpenTransition    = "open_transition"
	fieldPrimaryChanged    = "primary_opportunity_changed"
	opSet, opRemoved       = "set", "removed"
	opChanged, opAdded     = "changed", "added"
	opBecameUnknown        = "became_unknown"
)

// field is how one diff entry is labelled and classified.
type field struct{ label, dimension string }

// fields covers every AccountState field and every structure the state diff reports; a test checks it
// against the reducer's field list, so a new field cannot reach Build unclassified.
var fields = map[string]field{
	"stage":                     {"Stage", dimIntent},
	"health":                    {"Account health", dimBlockers},
	"owner":                     {"Relationship owner", dimOwnership},
	"motion":                    {"Sales motion", dimIntent},
	"champion":                  {"Champion", dimOwnership},
	"champion_status":           {"Champion status", dimOwnership},
	"economic_buyer":            {"Economic buyer", dimStakeholders},
	"blockers":                  {"Blockers", dimBlockers},
	"objections":                {"Objections", dimBlockers},
	"decision_criteria":         {"Decision criteria", dimIntent},
	"decision_process":          {"Decision process", dimIntent},
	"current_commitments":       {"Open commitments", dimNextStep},
	"next_milestone":            {"Next milestone", dimNextStep},
	"next_meeting":              {"Next meeting", dimNextStep},
	"relationship_risk":         {"Relationship risk", dimBlockers},
	"product_use_case":          {"Product use case", dimIntent},
	"commercial_issue":          {"Commercial issue", dimBlockers},
	"last_customer_interaction": {"Last customer interaction", dimIntent},
	"last_meaningful_change":    {"Last meaningful change", dimIntent},
	"summary":                   {"Account summary", dimIntent},
	fieldBuyingGroup:            {"Buying group", dimStakeholders},
	fieldCoverageGaps:           {"Coverage gaps", dimStakeholders},
	fieldRelationshipState:      {"Relationship state", dimIntent},
	fieldOpenTransition:         {"Open relationship transition", dimIntent},
	fieldPrimaryChanged:         {"Primary opportunity", dimIntent},
}

var dimensionLabels = map[string]string{
	dimStakeholders: "stakeholder structure", dimOwnership: "relationship ownership", dimIntent: "buyer intent",
	dimBlockers: "blockers and risk", dimNextStep: "next-step commitment", "recommended_action": "recommended action",
}

// DimensionOf is the change dimension a diff field belongs to; "" for a field Build does not know.
func DimensionOf(fieldName string) string { return fields[fieldName].dimension }

// KnownFields lists the diff fields Build can report, sorted.
func KnownFields() []string {
	out := make([]string, 0, len(fields))
	for name := range fields {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// statement is the template of one claim: a sentence over the entry's before and after values.
func statement(e DiffEntry, people map[string]string) string {
	label := fields[e.Field].label
	switch e.Field {
	case fieldBuyingGroup:
		return clip(buyingGroupStatement(e, people), maxStatement)
	case fieldOpenTransition:
		return clip(transitionStatement(e), maxStatement)
	}
	before, after := render(e.Before), render(e.After)
	var s string
	switch e.Op {
	case opSet:
		s = fmt.Sprintf("%s set to %s.", label, after)
	case opAdded:
		s = fmt.Sprintf("%s gained %s.", label, after)
	case opRemoved:
		s = fmt.Sprintf("%s cleared (was %s).", label, before)
	case opBecameUnknown:
		s = fmt.Sprintf("%s is no longer known (was %s).", label, before)
	default:
		s = fmt.Sprintf("%s changed from %s to %s.", label, before, after)
	}
	return clip(s, maxStatement)
}

// transitionStatement reads the "STATUS:from>to" key the diff stores for the open transition.
func transitionStatement(e DiffEntry) string {
	describe := func(v any) string {
		key, _ := v.(string)
		status, rest, ok := strings.Cut(key, ":")
		if !ok {
			return "none"
		}
		from, to, _ := strings.Cut(rest, ">")
		if to == "" {
			to = "no clear target"
		}
		return fmt.Sprintf("%s (%s -> %s)", status, from, to)
	}
	switch e.Op {
	case opSet:
		return "Open relationship transition: " + describe(e.After) + " opened."
	case opRemoved:
		return "Open relationship transition: " + describe(e.Before) + " closed."
	}
	return "Open relationship transition changed from " + describe(e.Before) + " to " + describe(e.After) + "."
}

type memberView struct {
	id, status, delegated string
	roles                 []string
}

func decodeMembers(v any) map[string]memberView {
	out := map[string]memberView{}
	list, _ := v.([]any)
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["person_id"].(string)
		mv := memberView{id: id}
		mv.status, _ = m["status"].(string)
		mv.delegated, _ = m["delegated_to_person_id"].(string)
		if roles, ok := m["roles"].([]any); ok {
			for _, r := range roles {
				if s, ok := r.(string); ok {
					mv.roles = append(mv.roles, s)
				}
			}
		}
		sort.Strings(mv.roles)
		out[id] = mv
	}
	return out
}

func sortedIDs(m map[string]memberView) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func roleText(roles []string) string {
	if len(roles) == 0 {
		return "role unknown"
	}
	return strings.Join(roles, ", ")
}

func personName(people map[string]string, id string) string {
	if n := strings.TrimSpace(people[id]); n != "" {
		return n
	}
	return "person " + id[:min(8, len(id))]
}

func buyingGroupStatement(e DiffEntry, people map[string]string) string {
	before, after := decodeMembers(e.Before), decodeMembers(e.After)
	var added, removed, updated []string
	for _, id := range sortedIDs(after) {
		a := after[id]
		b, existed := before[id]
		switch {
		case !existed:
			added = append(added, fmt.Sprintf("%s (%s)", personName(people, id), roleText(a.roles)))
		case roleText(b.roles) != roleText(a.roles) || b.status != a.status || b.delegated != a.delegated:
			updated = append(updated, updatedMember(people, b, a))
		}
	}
	for _, id := range sortedIDs(before) {
		if _, kept := after[id]; !kept {
			removed = append(removed, fmt.Sprintf("%s (%s)", personName(people, id), roleText(before[id].roles)))
		}
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(updated) > 0 {
		parts = append(parts, "updated "+strings.Join(updated, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d member(s) before, %d after", len(before), len(after)))
	}
	return "Buying group changed: " + strings.Join(parts, "; ") + "."
}

func updatedMember(people map[string]string, b, a memberView) string {
	var what []string
	if roleText(b.roles) != roleText(a.roles) {
		what = append(what, fmt.Sprintf("roles %s -> %s", roleText(b.roles), roleText(a.roles)))
	}
	if b.status != a.status {
		what = append(what, fmt.Sprintf("status %s -> %s", orNone(b.status), orNone(a.status)))
	}
	if b.delegated != a.delegated {
		what = append(what, fmt.Sprintf("delegated to %s -> %s", delegate(people, b.delegated), delegate(people, a.delegated)))
	}
	return fmt.Sprintf("%s (%s)", personName(people, a.id), strings.Join(what, ", "))
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func delegate(people map[string]string, id string) string {
	if id == "" {
		return "no one"
	}
	return personName(people, id)
}

// render shows one diff value inside a sentence: text in quotes, list items by their text.
func render(v any) string {
	switch x := v.(type) {
	case nil:
		return "none"
	case string:
		return clip(`"`+x+`"`, maxValue)
	case []any:
		return clip(renderList(x), maxValue)
	case map[string]any:
		if text, ok := x["text"].(string); ok {
			return clip(`"`+text+`"`, maxValue)
		}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "(unrenderable)"
	}
	return clip(string(raw), maxValue)
}

func renderList(list []any) string {
	var parts []string
	sep := "; "
	for _, x := range list {
		switch e := x.(type) {
		case string:
			parts = append(parts, e)
			sep = ", "
		case map[string]any:
			if text, ok := e["text"].(string); ok {
				parts = append(parts, `"`+text+`"`)
				continue
			}
			raw, _ := json.Marshal(e)
			parts = append(parts, string(raw))
		default:
			raw, _ := json.Marshal(e)
			parts = append(parts, string(raw))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, sep)
}

// clip shortens s to n runes, ending in an ellipsis when it had to cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
