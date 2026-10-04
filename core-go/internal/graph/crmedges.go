package graph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// crmRoleRel maps a CRM Contact Role__c value to the edge it asserts into the opportunity, or ""
// for a role WP5 does not map. CRM states champions explicitly, so "Champion" is crm_explicit
// (WP6 adds AI-derived champions at first_party_ai; standing decides).
func crmRoleRel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "economic buyer":
		return RelEconomicBuyer
	case "champion":
		return RelChampionFor
	case "security approver", "technical approver", "evaluation owner":
		return RelInfluences
	}
	return ""
}

// roleRels lists every relationship type a CRM role can assert.
func roleRels() []string { return []string{RelEconomicBuyer, RelInfluences, RelChampionFor} }

// crmEdges writes what a CRM change event states explicitly (standing crm_explicit):
// works_at and role edges for contacts, belongs_to and owns for opportunities.
func (f *finalizer) crmEdges(ctx context.Context) error {
	c, ok, err := decodeCRM(f.in.Event)
	if err != nil || !ok {
		return err
	}
	switch c.ObjectType {
	case "Contact":
		return f.contactEdges(ctx, c)
	case "Opportunity":
		return f.opportunityEdges(ctx, c)
	}
	return nil
}

func (f *finalizer) crmSpec(src EntityRef, rel string, dst EntityRef) EdgeSpec {
	return EdgeSpec{Src: src, Rel: rel, Dst: dst, Standing: StandingCRMExplicit, Confidence: exactConfidence,
		SourceActivityID: f.in.ActivityID, ValidFrom: f.at}
}

func (f *finalizer) contactEdges(ctx context.Context, c crmEvent) error {
	person, err := entityByCRM(ctx, f.tx, c.RecordID)
	if err != nil || person == "" {
		return err
	}
	acct, err := entityByCRM(ctx, f.tx, c.accountRecord())
	if err != nil || acct == "" {
		return err
	}
	me, account := EntityRef{EntityPerson, person}, EntityRef{EntityAccount, acct}
	if c.has("Title") && !c.Created {
		if _, err := f.tx.ExecContext(ctx, `UPDATE people SET title = $2, updated_at = now() WHERE id = $1::uuid`, person, c.field("Title")); err != nil {
			return fmt.Errorf("graph: update title: %w", err)
		}
	}
	if c.Created {
		// CRM names the contact's account: it outranks a rule-derived works_at elsewhere.
		if _, err := CloseEdges(ctx, f.tx, EdgeFilter{Src: &me, NotDst: &account, Rels: []string{RelWorksAt},
			Standings: standingsAtMost(StandingCRMExplicit)}, f.at); err != nil {
			return err
		}
		if _, err := UpsertEdge(ctx, f.tx, f.crmSpec(me, RelWorksAt, account)); err != nil {
			return err
		}
	}
	if !c.has("Role__c") {
		return nil
	}
	return f.roleEdge(ctx, me, acct, c.field("Role__c"))
}

// roleEdge makes the contact's CRM role the only crm_explicit role edge into the account's
// opportunity. With zero or several opportunities nothing is written: guessing which
// opportunity a role belongs to would be worse than waiting for a claim.
func (f *finalizer) roleEdge(ctx context.Context, me EntityRef, acct, role string) error {
	opp, err := soleOpportunity(ctx, f.tx, acct)
	if err != nil || opp == "" {
		return err
	}
	dst := EntityRef{EntityOpportunity, opp}
	want := crmRoleRel(role)
	var others []string
	for _, r := range roleRels() {
		if r != want {
			others = append(others, r)
		}
	}
	if _, err := CloseEdges(ctx, f.tx, EdgeFilter{Src: &me, Dst: &dst, Rels: others, Standings: []string{StandingCRMExplicit}}, f.at); err != nil {
		return err
	}
	if want == "" {
		return nil
	}
	spec := f.crmSpec(me, want, dst)
	spec.Exclusive = want == RelChampionFor || want == RelEconomicBuyer // one holder each
	_, err = UpsertEdge(ctx, f.tx, spec)
	return err
}

// soleOpportunity returns the account's only opportunity, or "" when it has none or several.
func soleOpportunity(ctx context.Context, q DBTX, acct string) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text FROM opportunities WHERE account_id = $1::uuid LIMIT 2`, acct)
	if err != nil {
		return "", fmt.Errorf("graph: list opportunities: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("graph: scan opportunity: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("graph: read opportunities: %w", err)
	}
	if len(ids) != 1 {
		return "", nil
	}
	return ids[0], nil
}

func (f *finalizer) opportunityEdges(ctx context.Context, c crmEvent) error {
	opp, err := entityByCRM(ctx, f.tx, c.RecordID)
	if err != nil || opp == "" {
		return err
	}
	dst := EntityRef{EntityOpportunity, opp}
	if c.Created {
		var acct sql.NullString
		if err := f.tx.QueryRowContext(ctx, `SELECT account_id::text FROM opportunities WHERE id = $1::uuid`, opp).Scan(&acct); err != nil {
			return fmt.Errorf("graph: opportunity account: %w", err)
		}
		if _, err := UpsertEdge(ctx, f.tx, f.crmSpec(dst, RelBelongsTo, EntityRef{EntityAccount, acct.String})); err != nil {
			return err
		}
	}
	if !c.has("OwnerEmail") {
		return nil
	}
	owner, err := employeeByEmail(ctx, f.tx, c.field("OwnerEmail"))
	if err != nil || owner == "" {
		return err
	}
	if _, err := f.tx.ExecContext(ctx, `UPDATE opportunities SET owner_person_id = $2::uuid, updated_at = now()
		WHERE id = $1::uuid AND owner_person_id IS DISTINCT FROM $2::uuid`, opp, owner); err != nil {
		return fmt.Errorf("graph: set opportunity owner: %w", err)
	}
	me := EntityRef{EntityPerson, owner}
	spec := f.crmSpec(me, RelOwns, dst)
	spec.Exclusive = true // one owner; closes other owners at or below crm_explicit
	_, err = UpsertEdge(ctx, f.tx, spec)
	return err
}
