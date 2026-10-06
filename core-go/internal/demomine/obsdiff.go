package demomine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// ChangedFields lists, in sorted order, the deal-state fields whose value differs between two versions of an
// OpportunityState (JSON as stored in opportunity_state_history.state), plus "buying_group" and
// "coverage_gaps" when those moved (membership, roles and delegation; a status that only ages with the clock is not structure). Evidence, claim ids and times are provenance, not state a rep acts on,
// so they never make a change; a field turning unknown or known does.
func ChangedFields(prev, cur []byte) ([]string, error) {
	var a, b reducer.OpportunityState
	if err := json.Unmarshal(prev, &a); err != nil {
		return nil, fmt.Errorf("demomine: decode previous deal state: %w", err)
	}
	if err := json.Unmarshal(cur, &b); err != nil {
		return nil, fmt.Errorf("demomine: decode deal state: %w", err)
	}
	out := []string{}
	names := append(reducer.FieldNames(), "amount")
	for _, name := range names {
		if fieldView(a.Fields.Field(name)) != fieldView(b.Fields.Field(name)) {
			out = append(out, name)
		}
	}
	if groupView(a.BuyingGroup) != groupView(b.BuyingGroup) {
		out = append(out, "buying_group")
	}
	if strings.Join(sortedCopy(a.CoverageGaps), ",") != strings.Join(sortedCopy(b.CoverageGaps), ",") {
		out = append(out, "coverage_gaps")
	}
	sort.Strings(out)
	return out, nil
}

// fieldView is the comparable content of one field: whether it is known and its value, with list items
// reduced to text, status and due date.
func fieldView(f *reducer.Field) string {
	if f == nil || !f.Known {
		return "unknown"
	}
	if s, ok := f.Value.(string); ok && s == claims.Unknown {
		return "unknown"
	}
	if _, isList := f.Value.([]any); !isList {
		return fmt.Sprintf("known:%v", f.Value)
	}
	if items := f.ListItems(); items != nil {
		parts := make([]string, 0, len(items))
		for _, it := range items {
			due := ""
			if it.DueAt != nil {
				due = it.DueAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			parts = append(parts, it.Text+"|"+it.Status+"|"+due)
		}
		sort.Strings(parts)
		return "list:" + strings.Join(parts, ";")
	}
	return fmt.Sprintf("known:%v", f.Value)
}

func groupView(members []reducer.Member) string {
	parts := make([]string, 0, len(members))
	for _, m := range members {
		del := ""
		if m.DelegatedToPerson != nil {
			del = *m.DelegatedToPerson
		}
		parts = append(parts, m.PersonID+"|"+strings.Join(sortedCopy(m.Roles), ",")+"|"+del)
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
