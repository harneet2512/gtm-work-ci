package deterministic

import (
	"encoding/json"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// fieldText renders a state field's known value as text (strings, list items, or JSON).
func fieldText(f reducer.Field) string {
	if !f.Known || f.Value == nil {
		return ""
	}
	if s, ok := f.Value.(string); ok {
		return s
	}
	if items := listItems(f); len(items) > 0 {
		parts := make([]string, len(items))
		for i, it := range items {
			parts[i] = it.Text
		}
		return strings.Join(parts, "\n")
	}
	raw, err := json.Marshal(f.Value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// supportText is every text the draft's figures may be supported by: this account's activities
// the run read, the draft's own evidence quotes, and the commercial and product fields of the
// state the draft was written from and of the latest state.
func supportText(in Input) string {
	var parts []string
	for _, a := range in.Activities {
		if a.AccountID == nil || *a.AccountID == in.AccountID {
			parts = append(parts, a.Text)
		}
	}
	for _, r := range in.Draft.EvidenceRefs {
		parts = append(parts, r.Quote)
	}
	for _, s := range []reducer.AccountState{in.State, latestState(in)} {
		parts = append(parts, fieldText(s.Fields.CommercialIssue), fieldText(s.Fields.ProductUseCase),
			fieldText(s.Fields.DecisionCriteria), fieldText(s.Fields.CurrentCommitments))
	}
	return strings.Join(parts, "\n")
}
