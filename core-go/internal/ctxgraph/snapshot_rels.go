package ctxgraph

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// relTypeMap maps the relationships.rel_type vocabulary to ontology relationship types.
var relTypeMap = map[string]string{
	"works_at": RelWorksAt, "belongs_to": RelBelongsTo, "champion_for": RelChampionFor,
	"economic_buyer_for": RelEconomicBuyerFor, "evaluates": RelTechnicalEvaluator, "influences": RelInfluences,
	"participated_in": RelParticipatedIn, "involves": RelInvolves, "about": RelAbout, "owns": RelOwns,
	"supports": RelSupports, "reports_to": RelReportsTo, "delegated_to": RelDelegatedTo, "shared_with": RelSharedWith,
}

// scopePeople is NULL for the current projection (the account's contacts by people.account_id) and, at a
// cutoff, the people the account's evidence names (people.account_id is overwritten by re-attribution).
func (b *builder) scopePeople() any {
	if b.cut == nil {
		return nil
	}
	return signalstore.UUIDArray(b.evidencedIDs("person"))
}

// scopeCTE is every canonical node of the account that can be an endpoint of a relationships row.
const scopeCTE = `
WITH scope(t, id) AS (
  SELECT 'account', id FROM accounts WHERE id = $1::uuid
  UNION ALL SELECT 'opportunity', id FROM opportunities WHERE account_id = $1::uuid
  UNION ALL SELECT 'activity', id FROM activities WHERE account_id = $1::uuid
  UNION ALL SELECT 'person', id FROM people WHERE ($2::uuid[] IS NULL AND account_id = $1::uuid) OR id = ANY($2::uuid[]))`

type relRow struct {
	id, srcType, srcID, relType, dstType, dstID, standing string
	confidence                                            float64
	activity                                              sql.NullString
	from, created                                         time.Time
	to                                                    sql.NullTime
}

func (b *builder) loadRelationships(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, scopeCTE+`
SELECT r.id::text, r.src_type, r.src_id::text, r.rel_type, r.dst_type, r.dst_id::text, r.standing,
       r.confidence::float8, r.source_activity_id::text, r.valid_from, r.valid_to, r.created_at
  FROM relationships r
 WHERE EXISTS (SELECT 1 FROM scope s WHERE s.t = r.src_type AND s.id = r.src_id)
    OR EXISTS (SELECT 1 FROM scope s WHERE s.t = r.dst_type AND s.id = r.dst_id)`, b.accountID, b.scopePeople())
	if err != nil {
		return fmt.Errorf("ctxgraph: read relationships: %w", err)
	}
	defer rows.Close()
	var all []relRow
	for rows.Next() {
		var r relRow
		if err := rows.Scan(&r.id, &r.srcType, &r.srcID, &r.relType, &r.dstType, &r.dstID, &r.standing,
			&r.confidence, &r.activity, &r.from, &r.to, &r.created); err != nil {
			return err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if b.cut != nil {
		var err error
		if all, err = b.relsAtCut(ctx, all); err != nil {
			return err
		}
	}
	for _, r := range all {
		if err := b.projectRelationship(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// nodeLabelOf resolves a Postgres endpoint type to the label of its projected node.
func (b *builder) nodeLabelOf(entityType, id string) (string, bool) {
	switch entityType {
	case "account":
		return LabelAccount, true
	case "person":
		return LabelPerson, true
	case "opportunity":
		return LabelOpportunity, true
	case "document":
		return LabelDocument, true
	case "activity":
		if a, ok := b.acts[id]; ok {
			return a.label, true
		}
		return "", false // an activity of another account
	}
	return "", false // product
}

func (b *builder) projectRelationship(ctx context.Context, r relRow) error {
	typ, ok := relTypeMap[r.relType]
	if !ok {
		b.skip(skipRelNotInOntology)
		return nil
	}
	from, fromOK := b.nodeLabelOf(r.srcType, r.srcID)
	to, toOK := b.nodeLabelOf(r.dstType, r.dstID)
	if !fromOK || !toOK {
		b.skip(skipEndpointNotAllowed)
		return nil
	}
	srcID, dstID := r.srcID, r.dstID
	if typ == RelParticipatedIn && from == LabelPerson && to == LabelActivity {
		// A person took part in an activity that is not a conversation (a CRM change, a task): the ontology
		// holds that as Activity INVOLVES Person. Same ledger row, same id and provenance, reversed direction.
		typ, from, to, srcID, dstID = RelInvolves, to, from, dstID, srcID
	}
	spec := Relationships[typ]
	if !labelConforms(from, spec.From) || !labelConforms(to, spec.To) {
		b.skip(skipEndpointNotAllowed)
		return nil
	}
	var acts []string
	if r.activity.Valid {
		acts = []string{r.activity.String}
	}
	acts, events, err := b.evidence(ctx, acts...)
	if err != nil {
		return err
	}
	status, updated := "open", r.created
	var validTo *time.Time
	if r.to.Valid {
		t := r.to.Time
		validTo, status, updated = &t, "closed", t
	}
	props := edgeProps(r.from, validTo, status, acts, events, r.created, updated,
		map[string]any{"confidence": round3(r.confidence), "standing": r.standing})
	b.addEdge(newEdge(typ, "rel:"+r.id, from, srcID, to, dstID, b.accountID, props))
	return nil
}
