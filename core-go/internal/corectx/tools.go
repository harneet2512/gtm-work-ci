package corectx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// claimFieldPaths is claim.v1.json $defs/fieldPath (the claims.field_path CHECK).
var claimFieldPaths = map[string]bool{
	"stage": true, "health": true, "owner": true, "motion": true, "champion": true, "champion_status": true,
	"economic_buyer": true, "buying_group.member": true, "stakeholder_role": true, "blockers": true,
	"objections": true, "decision_criteria": true, "decision_process": true, "commitment": true,
	"next_milestone": true, "next_meeting": true, "relationship_risk": true, "product_use_case": true,
	"commercial_issue": true, "delegation": true, "summary": true,
}

// stateField maps a claim field path to the AccountState field it folds into ("" for the
// buying-group paths, which fold into buying_group).
func stateField(fieldPath string) string {
	switch fieldPath {
	case "commitment":
		return "current_commitments"
	case "buying_group.member", "stakeholder_role", "delegation":
		return ""
	}
	return fieldPath
}

func isBuyingGroupPath(fieldPath string) bool {
	return fieldPath != "" && stateField(fieldPath) == ""
}

// fetch returns up to p.Limit items of the tool for the run's account and whether more existed.
func fetch(ctx context.Context, db claimstore.DB, sc runScope, tool Tool, p Params) ([]any, bool, error) {
	switch tool {
	case ToolState:
		return stateItems(ctx, db, sc, p)
	case ToolPeople:
		return peopleItems(ctx, db, sc, p)
	case ToolCommitments:
		return commitmentItems(ctx, db, sc, p)
	case ToolRecentDiffs:
		return diffItems(ctx, db, sc, p)
	case ToolEvidence:
		return evidenceAsOf(ctx, db, sc, p)
	case ToolActivities:
		return activityItems(ctx, db, sc, p)
	}
	return nil, false, ErrUnknownTool
}

type fieldItem struct {
	FieldPath string `json:"field_path"`
	reducer.Field
}

func stateItems(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	st, ok, err := loadState(ctx, db, sc)
	if err != nil || !ok {
		return nil, false, err
	}
	if isBuyingGroupPath(p.FieldPath) {
		return members(st, p.Limit)
	}
	if p.FieldPath != "" {
		f := st.Fields.Field(stateField(p.FieldPath))
		return []any{fieldItem{FieldPath: p.FieldPath, Field: *f}}, false, nil
	}
	all := []any{map[string]any{
		"kind": "state_header", "account_id": st.AccountID, "account_name": st.AccountName,
		"opportunity_id": st.OpportunityID, "version": st.Version, "as_of": st.AsOf,
		"computed_at": st.ComputedAt, "coverage_gaps": st.CoverageGaps,
	}}
	names := reducer.FieldNames()
	sort.SliceStable(names, func(i, j int) bool { // known fields first, schema order within each group
		return st.Fields.Field(names[i]).Known && !st.Fields.Field(names[j]).Known
	})
	for _, n := range names {
		all = append(all, fieldItem{FieldPath: n, Field: *st.Fields.Field(n)})
	}
	return cut(all, p.Limit)
}

func members(st reducer.AccountState, limit int) ([]any, bool, error) {
	all := make([]any, 0, len(st.BuyingGroup))
	for _, m := range st.BuyingGroup {
		all = append(all, m)
	}
	return cut(all, limit)
}

func peopleItems(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	st, ok, err := loadState(ctx, db, sc)
	if err != nil || !ok {
		return nil, false, err
	}
	return members(st, p.Limit)
}

func commitmentItems(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	st, ok, err := loadState(ctx, db, sc)
	if err != nil || !ok {
		return nil, false, err
	}
	raw, err := json.Marshal(st.Fields.CurrentCommitments.Value)
	if err != nil {
		return nil, false, fmt.Errorf("corectx: encode commitments: %w", err)
	}
	var list []reducer.Item
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false, nil // an unknown commitments field has no list value
	}
	all := make([]any, len(list))
	for i, it := range list {
		all[i] = it
	}
	return cut(all, p.Limit)
}

// cut keeps the first limit items and reports whether any were left.
func cut(all []any, limit int) ([]any, bool, error) {
	if len(all) > limit {
		return all[:limit], true, nil
	}
	return all, false, nil
}

func diffItems(ctx context.Context, db claimstore.DB, sc runScope, p Params) ([]any, bool, error) {
	field := ""
	if p.FieldPath != "" {
		field = stateField(p.FieldPath)
		if field == "" {
			field = "buying_group"
		}
	}
	diffs, err := readmodel.QueryDiffsBefore(ctx, db, sc.accountID, p.Limit+1, true, field, &sc.cutoff, pinnedVersion(sc))
	if err != nil {
		return nil, false, err
	}
	all := make([]any, len(diffs))
	for i, d := range diffs {
		all[i] = d
	}
	return cut(all, p.Limit)
}
