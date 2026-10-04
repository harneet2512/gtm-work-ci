package ctxgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

const skipRoleWithoutClaim = "opportunity_role_without_claim"

// stateRoleRel maps buying-group roles of an OpportunityState (ADR-0016) to ontology relationships.
var stateRoleRel = map[string]string{
	"champion": RelChampionFor, "economic_buyer": RelEconomicBuyerFor, "technical_evaluator": RelTechnicalEvaluator,
	"executive_sponsor": RelInfluences,
}

// closedMemberStatuses end a role: the person no longer holds it on the deal.
var closedMemberStatuses = map[string]bool{"inactive": true, "disengaged": true, "departed": true}

type claimBasis struct {
	standing   string
	confidence float64
}

// loadOpportunityRoles projects the roles of each deal's buying group (OpportunityState, ADR-0016) as
// role edges scoped to that deal. A role the relationships table already records as open for the same
// person and deal is not duplicated: the ledger row is the evidence. The standing and confidence of a
// state-derived edge are those of the strongest active claim about that person on that deal.
func (b *builder) loadOpportunityRoles(ctx context.Context) error {
	basis, err := b.roleClaimBasis(ctx)
	if err != nil {
		return err
	}
	query, args := b.dealStateQuery()
	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("ctxgraph: read opportunity state: %w", err)
	}
	defer rows.Close()
	type deal struct {
		id    string
		state reducer.OpportunityState
		asOf  time.Time
	}
	var deals []deal
	for rows.Next() {
		var d deal
		var raw string
		if err := rows.Scan(&d.id, &raw, &d.asOf); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), &d.state); err != nil {
			return fmt.Errorf("ctxgraph: decode opportunity state of %s: %w", d.id, err)
		}
		deals = append(deals, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	open := b.openRoleEdges()
	for _, d := range deals {
		for _, m := range d.state.BuyingGroup {
			b.projectMemberRoles(ctx, d.id, d.asOf, m, basis, open)
		}
	}
	return nil
}

// roleClaimBasis is the strongest active claim per (person, deal) about buying-group membership.
func (b *builder) roleClaimBasis(ctx context.Context) (map[string]claimBasis, error) {
	rows, err := b.db.QueryContext(ctx, `
SELECT DISTINCT ON (subject_person_id, opportunity_id) subject_person_id::text, opportunity_id::text, standing, confidence::float8
  FROM claims
 WHERE account_id = $1::uuid AND subject_person_id IS NOT NULL AND opportunity_id IS NOT NULL
   AND ($2::uuid[] IS NULL AND status = 'active' OR id = ANY($2::uuid[]))
   AND field_path IN ('buying_group.member', 'stakeholder_role', 'champion', 'economic_buyer')
 ORDER BY subject_person_id, opportunity_id, standing_rank DESC, confidence DESC, id`, b.accountID, b.activeClaimIDs())
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: read role claims: %w", err)
	}
	defer rows.Close()
	out := map[string]claimBasis{}
	for rows.Next() {
		var person, opp string
		var c claimBasis
		if err := rows.Scan(&person, &opp, &c.standing, &c.confidence); err != nil {
			return nil, err
		}
		out[person+"|"+opp] = c
	}
	return out, rows.Err()
}

// openRoleEdges indexes the open role edges the relationships table already produced.
func (b *builder) openRoleEdges() map[string]bool {
	out := map[string]bool{}
	for _, e := range b.pending {
		if _, ok := e.Props["valid_to"]; !ok && e.FromLabel == LabelPerson && e.ToLabel == LabelOpportunity {
			out[e.Type+"|"+e.FromID+"|"+e.ToID] = true
		}
	}
	return out
}

func (b *builder) projectMemberRoles(ctx context.Context, opp string, asOf time.Time, m reducer.Member, basis map[string]claimBasis, open map[string]bool) {
	cb, haveClaim := basis[m.PersonID+"|"+opp]
	for _, role := range m.Roles {
		typ, ok := stateRoleRel[role]
		if !ok || open[typ+"|"+m.PersonID+"|"+opp] {
			continue
		}
		if !haveClaim {
			b.skip(skipRoleWithoutClaim)
			continue
		}
		from, to, acts := roleWindow(asOf, m)
		validTo := (*time.Time)(nil)
		status := "open"
		if closedMemberStatuses[m.Status] {
			end := to
			validTo, status = &end, "closed"
		}
		a, events, err := b.evidence(ctx, acts...)
		if err != nil {
			b.skip(skipEndpointMissing)
			continue
		}
		props := edgeProps(from, validTo, status, a, events, from, to,
			map[string]any{"confidence": round3(cb.confidence), "standing": cb.standing, "member_status": m.Status, "role": role, "scope": "opportunity_state"})
		b.addEdge(newEdge(typ, "ostate:"+opp+":"+m.PersonID+":"+typ, LabelPerson, m.PersonID, LabelOpportunity, opp, b.accountID, props))
	}
}

// roleWindow is when the role was first and last evidenced and the activities behind it; a member without
// evidence falls back to the state's own time.
func roleWindow(asOf time.Time, m reducer.Member) (from, to time.Time, acts []string) {
	from, to = asOf, asOf
	for i, e := range m.EvidenceRefs {
		acts = append(acts, e.ActivityID)
		if i == 0 || e.OccurredAt.Before(from) {
			from = e.OccurredAt
		}
		if i == 0 || e.OccurredAt.After(to) {
			to = e.OccurredAt
		}
	}
	return from, to, acts
}
