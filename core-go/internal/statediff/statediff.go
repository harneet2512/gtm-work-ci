// Package statediff computes the StateDiff between two AccountState versions
// (contracts/schemas/state_diff.v1.json): what changed, which changes are material, and the evidence
// behind each. It is pure: the same two states always give the same diff. A diff with no material
// change is still a diff (is_material=false): "no material state change" is a recorded outcome.
package statediff

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Ops of a Change (state_diff.v1.json).
const (
	OpSet           = "set"
	OpAdded         = "added"
	OpRemoved       = "removed"
	OpChanged       = "changed"
	OpBecameUnknown = "became_unknown"
)

// Names of the diffed structures that are not AccountState fields.
const (
	FieldBuyingGroup  = "buying_group"
	FieldCoverageGaps = "coverage_gaps"
	// FieldRelationshipState and FieldOpenTransition are the account's earned relationship state and its open
	// transition (ADR-0012). A change of either is material: the agent must see that the state moved.
	FieldRelationshipState = "relationship_state"
	FieldOpenTransition    = "open_transition"
	// FieldPrimaryChanged is emitted when the primary opportunity differs between the two versions (ADR-0016).
	FieldPrimaryChanged = "primary_opportunity_changed"
)

// nonMaterialFields change on almost every activity or are free text, so they never make a diff
// material by themselves (gold: last_customer_interaction is excluded from material_diff_fields).
var nonMaterialFields = map[string]bool{
	"last_customer_interaction": true, "last_meaningful_change": true, "summary": true,
}

// Change is one field-level difference.
type Change struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	// OpportunityID is the deal a deal-scoped change belongs to (state_diff.v1.json); nil for account-scoped ones.
	OpportunityID *string               `json:"opportunity_id,omitempty"`
	Before        any                   `json:"before,omitempty"`
	After         any                   `json:"after,omitempty"`
	Material      bool                  `json:"material"`
	EvidenceRefs  []reducer.EvidenceRef `json:"evidence_refs,omitempty"`
}

// Diff is the contract object state_diff.v1.json (without id and created_at, which the store assigns).
type Diff struct {
	AccountID   string   `json:"account_id"`
	FromVersion int      `json:"from_version"`
	ToVersion   int      `json:"to_version"`
	IsMaterial  bool     `json:"is_material"`
	Changes     []Change `json:"changes"`
	ActivityIDs []string `json:"activity_ids"`
}

// MaterialFields lists the fields of the material changes, in diff order.
func (d Diff) MaterialFields() []string {
	var out []string
	for _, c := range d.Changes {
		if c.Material {
			out = append(out, c.Field)
		}
	}
	return out
}

// Change returns the change of one field.
func (d Diff) Change(field string) (Change, bool) {
	for _, c := range d.Changes {
		if c.Field == field {
			return c, true
		}
	}
	return Change{}, false
}

// Compute diffs prev (nil for an account's first state) against next. activityIDs are the
// activities folded into next since prev.
//
// The deal-scoped fields of an AccountState are the primary deal's headline. When the primary opportunity
// differs between prev and next, those fields describe two different deals and any difference would be a false
// stage, champion or blocker change (an email on another open deal would start a run). The diff then compares
// only the account-scoped fields, buying group and coverage gaps, and records a non-material
// primary_opportunity_changed change instead (state_diff.v1.json).
func Compute(prev *reducer.AccountState, next reducer.AccountState, activityIDs []string) Diff {
	empty := reducer.AccountState{}
	from, before := 0, &empty
	if prev != nil {
		from, before = prev.Version, prev
	}
	changes := []Change{}
	switched := reducer.PrimaryChanged(prev, next)
	if switched {
		changes = append(changes, primaryChange(before.OpportunityID, next.OpportunityID))
	}
	skip := map[string]bool{}
	if switched {
		for _, name := range reducer.DealFieldNames() {
			skip[name] = true
		}
	}
	for _, name := range reducer.FieldNames() {
		if skip[name] {
			continue
		}
		if c, ok := fieldChange(name, *before.Fields.Field(name), *next.Fields.Field(name)); ok {
			changes = append(changes, c)
		}
	}
	if c, ok := buyingGroupChange(before.BuyingGroup, next.BuyingGroup); ok {
		changes = append(changes, c)
	}
	if c, ok := gapChange(before.CoverageGaps, next.CoverageGaps); ok {
		changes = append(changes, c)
	}
	changes = append(changes, transitionChanges(before, &next)...)
	d := Diff{AccountID: next.AccountID, FromVersion: from, ToVersion: next.Version, Changes: changes,
		ActivityIDs: append([]string{}, activityIDs...)}
	for _, c := range changes {
		d.IsMaterial = d.IsMaterial || c.Material
	}
	return d
}

// primaryChange is the (non-material) record that the account's primary opportunity changed.
func primaryChange(before, after *string) Change {
	c := Change{Field: FieldPrimaryChanged, Op: OpChanged, Material: false}
	if before != nil {
		c.Before = *before
	}
	if after != nil {
		c.After = *after
	}
	return c
}

func fieldChange(name string, before, after reducer.Field) (Change, bool) {
	b, a := comparable(before), comparable(after)
	if reflect.DeepEqual(b, a) {
		return Change{}, false
	}
	c := Change{Field: name, Material: !nonMaterialFields[name], Before: shown(before), After: shown(after)}
	switch {
	case !before.Known || isUnknownText(before.Value):
		c.Op, c.Before = OpSet, nil
	case !after.Known || isUnknownText(after.Value):
		c.Op, c.After = OpBecameUnknown, nil
	default:
		c.Op = OpChanged
	}
	if c.OpportunityID = after.OpportunityID; c.OpportunityID == nil {
		c.OpportunityID = before.OpportunityID
	}
	c.EvidenceRefs = after.EvidenceRefs
	if len(c.EvidenceRefs) == 0 {
		c.EvidenceRefs = before.EvidenceRefs
	}
	return c, true
}

func isUnknownText(v any) bool { s, ok := v.(string); return ok && s == claims.Unknown }

// comparable is the value a field is compared on, with whether it is known (unknown and known-absent
// differ): scalars by value, list items by item key,
// status, owner and due date. The claim id and the evidence are provenance, not state, so a newer
// claim that re-asserts the same item is not a change.
func comparable(f reducer.Field) [2]any {
	if !f.Known || isUnknownText(f.Value) {
		return [2]any{false, nil}
	}
	items := f.ListItems()
	if items == nil {
		return [2]any{true, normalizedScalar(f.Value)}
	}
	keys := make([]string, 0, len(items))
	for _, it := range items {
		due := ""
		if it.DueAt != nil {
			due = it.DueAt.UTC().Format(time.RFC3339)
		}
		keys = append(keys, strings.Join([]string{claims.ItemKey(it.Text), it.Status, it.OwnerPersonID, due}, "\x1f"))
	}
	sort.Strings(keys)
	return [2]any{true, keys}
}

// normalizedScalar makes values that went through JSON compare equal to the originals.
func normalizedScalar(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return v
	}
	return out
}

// shown is the value as the diff reports it.
func shown(f reducer.Field) any {
	if !f.Known || isUnknownText(f.Value) {
		return nil
	}
	return f.Value
}

// transitionChanges reports a change of the account's relationship state or of its open transition (status,
// target). A new fact list or confidence on the same open transition is not a change of state.
func transitionChanges(before, after *reducer.AccountState) []Change {
	var out []Change
	b, a := relationshipValue(before), relationshipValue(after)
	if b != a {
		out = append(out, Change{Field: FieldRelationshipState, Op: OpChanged, Before: b, After: a, Material: true})
	}
	bo, ao := openKey(before), openKey(after)
	if bo != ao {
		c := Change{Field: FieldOpenTransition, Op: OpChanged, Before: bo, After: ao, Material: true}
		switch {
		case bo == "":
			c.Op, c.Before = OpSet, nil
		case ao == "":
			c.Op, c.After = OpRemoved, nil
		}
		out = append(out, c)
	}
	return out
}

func relationshipValue(st *reducer.AccountState) string {
	if st == nil || st.RelationshipState == nil || st.RelationshipState.Value == "" {
		return "unknown"
	}
	return st.RelationshipState.Value
}

// openKey names an open transition by its status, from-state and target ("" when none).
func openKey(st *reducer.AccountState) string {
	if st == nil || st.OpenTransition == nil {
		return ""
	}
	o := st.OpenTransition
	to := ""
	if o.ToStateCandidate != nil {
		to = *o.ToStateCandidate
	}
	return o.Status + ":" + o.FromState + ">" + to
}

// IsMaterialField reports whether a change to the named field is material under the same rule Compute
// applies (Bucket 1 B4 judges a recorded diff against it).
func IsMaterialField(name string) bool {
	return name != FieldPrimaryChanged && !nonMaterialFields[name]
}
