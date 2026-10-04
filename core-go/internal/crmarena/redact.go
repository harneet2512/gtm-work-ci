package crmarena

import (
	"encoding/json"
	"fmt"
	"time"
)

// Redactions counts what KnowledgeInputsWithReport removed: date-valued fields at or after the cutoff
// (a stage's CloseDate, a quote's ExpirationDate, a contract's EndDate are future-dated values of
// records that themselves happened earlier).
type Redactions struct {
	Fields  int            `json:"fields"`   // date-valued fields removed
	Events  int            `json:"events"`   // events that lost at least one field
	ByField map[string]int `json:"by_field"` // "Object.Field" -> count
}

// dateLayouts are the formats a payload date can take: Salesforce dates and datetimes, RFC 3339.
var dateLayouts = []string{sfDate, sfDateTime, time.RFC3339, time.RFC3339Nano}

func parseAnyDate(s string) (time.Time, bool) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// redactLate returns the payload without any date-valued field at or after the cutoff, and the names of
// the fields removed. A field is a JSON key holding a date string or a {"new": <date>} change; text
// inside free-form strings is not parsed. The input bytes are not modified.
func redactLate(payload []byte, cutoff time.Time) ([]byte, []string, error) {
	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, nil, fmt.Errorf("crmarena: knowledge payload is not an object: %w", err)
	}
	prefix, _ := doc["object_type"].(string)
	var removed []string
	redactMap(doc, prefix, cutoff, &removed)
	if len(removed) == 0 {
		return payload, nil, nil
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("crmarena: encode redacted payload: %w", err)
	}
	return out, removed, nil
}

func isLate(v any, cutoff time.Time) bool {
	if m, ok := v.(map[string]any); ok && len(m) == 1 { // a field change: {"new": value}
		v = m["new"]
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	at, ok := parseAnyDate(s)
	return ok && !at.Before(cutoff)
}

func redactMap(m map[string]any, prefix string, cutoff time.Time, removed *[]string) {
	for k, v := range m {
		switch child := v.(type) {
		case map[string]any:
			if isLate(child, cutoff) {
				delete(m, k)
				*removed = append(*removed, qualified(prefix, k))
				continue
			}
			redactMap(child, prefix, cutoff, removed)
		case []any:
			redactList(child, prefix, cutoff, removed)
		default:
			if isLate(v, cutoff) {
				delete(m, k)
				*removed = append(*removed, qualified(prefix, k))
			}
		}
	}
}

func redactList(list []any, prefix string, cutoff time.Time, removed *[]string) {
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			redactMap(m, prefix, cutoff, removed)
		}
	}
}

func qualified(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
