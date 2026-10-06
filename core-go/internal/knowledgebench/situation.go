package knowledgebench

import (
	"encoding/json"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// listItem is a legacy case list item.
type listItem struct {
	Text          string `json:"text"`
	Status        string `json:"status"`
	OwnerPersonID string `json:"owner_person_id"`
}

// situationOf converts a legacy case context into a matcher Situation.
//
// Signals: the cases list only the signal types of their own diff, without times, so the signals that
// fired earlier (prior) come from the mode: the account's gold checkpoints, nothing, or the persisted
// history the WP8 rules produce. The case's own diff signals are timed at `now`. Openness then follows
// ADR-0011 exactly as in production. Legacy cases predate ADR-0012: no relationship state, no transition.
func situationOf(c legacyCase, prior []knowledge.Signal) (knowledge.Situation, error) {
	st := c.Context.State
	s := knowledge.Situation{
		AccountID: st.AccountID, OpportunityID: st.OpportunityID, Now: c.Context.Now,
		Fields: map[string]knowledge.Value{}, Conflicts: map[string][]string{},
	}
	for name, raw := range st.Fields {
		v, err := valueOf(raw)
		if err != nil {
			return knowledge.Situation{}, fmt.Errorf("field %s: %w", name, err)
		}
		s.Fields[name] = v
	}
	gaps := make([]knowledge.Item, 0, len(st.CoverageGaps))
	for _, g := range st.CoverageGaps {
		gaps = append(gaps, knowledge.Item{Text: g})
	}
	s.Fields["coverage_gaps"] = knowledge.Value{Known: true, List: true, Items: gaps}
	for _, m := range st.BuyingGroup {
		s.BuyingGroup = append(s.BuyingGroup, knowledge.Member{PersonID: m.PersonID, Roles: m.Roles, Status: m.Status})
	}
	for i, cf := range st.Conflicts {
		s.Conflicts[cf.Field] = append(s.Conflicts[cf.Field], fmt.Sprintf("legacy-conflict-%d", i))
	}
	s.Signals = append(append([]knowledge.Signal(nil), prior...), ownDiffSignals(c)...)
	return s, nil
}

// goldSignals are the account's gold-checkpoint signals up to the case's time, each timed at its
// checkpoint's as_of (the latest time it can have fired).
func goldSignals(c legacyCase, history []checkpointSignals) []knowledge.Signal {
	var out []knowledge.Signal
	for _, cp := range history {
		if cp.AsOf.After(c.Context.Now) {
			continue
		}
		for _, t := range cp.Signals {
			out = append(out, knowledge.Signal{ID: "gold-" + t, Type: t, CreatedAt: cp.AsOf})
		}
	}
	return out
}

// ownDiffSignals are the signals the case's own diff lists, timed at now.
func ownDiffSignals(c legacyCase) []knowledge.Signal {
	var out []knowledge.Signal
	for _, t := range c.Context.RecentChanges.Signals {
		out = append(out, knowledge.Signal{ID: "case-" + t, Type: t, CreatedAt: c.Context.Now})
	}
	return out
}

// valueOf maps a legacy field value: "unknown" -> unknown, null -> known absent, a list -> items.
func valueOf(raw json.RawMessage) (knowledge.Value, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return knowledge.Value{}, err
	}
	switch x := v.(type) {
	case string:
		if x == "unknown" {
			return knowledge.Value{}, nil
		}
		return knowledge.Value{Known: true, Scalar: x}, nil
	case []any:
		var items []listItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return knowledge.Value{}, err
		}
		out := knowledge.Value{Known: true, List: true}
		for _, it := range items {
			out.Items = append(out.Items, knowledge.Item{Text: it.Text, Status: it.Status, OwnerPersonID: it.OwnerPersonID})
		}
		return out, nil
	}
	return knowledge.Value{Known: true, Scalar: v}, nil
}
