package signalbench

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

type goldItem struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
	Due    string `json:"due"`
}

// stateOf converts a gold checkpoint into the AccountState the reducer would produce from perfect
// extraction: "unknown" is an unknown field, null a known-absent one, a list its items (a missing status
// is open). People are identified by their stable gold keys.
func stateOf(g goldFile) (reducer.AccountState, error) {
	st := reducer.AccountState{AccountID: g.Account, Version: g.Checkpoint, AsOf: g.AsOf.UTC(), ComputedAt: g.AsOf.UTC()}
	for _, name := range reducer.FieldNames() {
		f, err := fieldOf(g.Expected.StateFields[name])
		if err != nil {
			return st, fmt.Errorf("field %s: %w", name, err)
		}
		*st.Fields.Field(name) = f
	}
	for _, m := range g.Expected.BuyingGroup {
		st.BuyingGroup = append(st.BuyingGroup, reducer.Member{PersonID: m.PersonKey, Roles: m.Roles, Status: m.Status})
	}
	st.CoverageGaps = append([]string(nil), g.Expected.CoverageGaps...)
	return st, nil
}

func fieldOf(raw json.RawMessage) (reducer.Field, error) {
	unknown := reducer.Field{Value: "unknown"}
	if len(raw) == 0 {
		return unknown, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return unknown, err
	}
	switch x := v.(type) {
	case string:
		if x == "unknown" {
			return unknown, nil
		}
		return reducer.Field{Known: true, Value: x}, nil
	case []any:
		var items []goldItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return unknown, err
		}
		return listField(items)
	}
	return reducer.Field{Known: true, Value: v}, nil
}

func listField(items []goldItem) (reducer.Field, error) {
	out := make([]reducer.Item, 0, len(items))
	for i, it := range items {
		item := reducer.Item{Text: it.Text, Status: it.Status, OwnerPersonID: it.Owner, ClaimID: fmt.Sprintf("gold-item-%d", i)}
		if item.Status == "" {
			item.Status = "open"
		}
		if it.Due != "" {
			due, err := time.Parse("2006-01-02", it.Due)
			if err != nil {
				return reducer.Field{}, fmt.Errorf("due %q: %w", it.Due, err)
			}
			item.DueAt = &due
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return reducer.Field{Value: "unknown"}, nil
	}
	return reducer.Field{Known: true, Value: out}, nil
}
