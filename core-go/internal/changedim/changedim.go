// Package changedim is the one place that says which change dimension (common.v1.json#changeDimension) a state-diff field
// belongs to, and what an episode's interpretation is therefore about. The field table and TopicsOf are pure; DiffTopics reads
// one stored state diff through claimstore.DB (one SELECT on state_diffs). The business-intelligence writer, the learning loop
// and the knowledge matcher all read the same table.
package changedim

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// byField maps every state-diff field to its dimension (biwriter checks it against the reducer's field list).
var byField = map[string]string{
	"stage":                       "buyer_intent",
	"health":                      "blockers_risk",
	"owner":                       "relationship_ownership",
	"motion":                      "buyer_intent",
	"champion":                    "relationship_ownership",
	"champion_status":             "relationship_ownership",
	"economic_buyer":              "stakeholder_structure",
	"blockers":                    "blockers_risk",
	"objections":                  "blockers_risk",
	"decision_criteria":           "buyer_intent",
	"decision_process":            "buyer_intent",
	"current_commitments":         "next_step_commitment",
	"next_milestone":              "next_step_commitment",
	"next_meeting":                "next_step_commitment",
	"relationship_risk":           "blockers_risk",
	"product_use_case":            "buyer_intent",
	"commercial_issue":            "blockers_risk",
	"last_customer_interaction":   "buyer_intent",
	"last_meaningful_change":      "buyer_intent",
	"summary":                     "buyer_intent",
	"buying_group":                "stakeholder_structure",
	"coverage_gaps":               "stakeholder_structure",
	"relationship_state":          "buyer_intent",
	"open_transition":             "buyer_intent",
	"primary_opportunity_changed": "buyer_intent",
}

// Of is the change dimension a diff field belongs to; "" for an unknown field.
func Of(field string) string { return byField[field] }

// TopicsOf is the sorted, unique dimensions of the given diff fields: what the interpretation of that diff is about,
// derived only from structured field names, never from prose. A field without a dimension contributes nothing; nil
// means no topic is known.
func TopicsOf(fieldNames []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fieldNames {
		if d := Of(f); d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// DiffTopics reads the topics of one stored state diff: the dimensions of its MATERIAL changes, which are the changes the
// business-intelligence update restates as claims. An empty diffID (a run without a diff) has no topic.
func DiffTopics(ctx context.Context, q claimstore.DB, diffID string) ([]string, error) {
	if diffID == "" {
		return nil, nil
	}
	var raw []byte
	if err := q.QueryRowContext(ctx, `SELECT changes FROM state_diffs WHERE id = $1::uuid`, diffID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("changedim: changes of state diff %s: %w", diffID, err)
	}
	var changes []struct {
		Field    string `json:"field"`
		Material bool   `json:"material"`
	}
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil, fmt.Errorf("changedim: decode changes of state diff %s: %w", diffID, err)
	}
	var names []string
	for _, c := range changes {
		if c.Material {
			names = append(names, c.Field)
		}
	}
	return TopicsOf(names), nil
}
