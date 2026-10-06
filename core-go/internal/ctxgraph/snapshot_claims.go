package ctxgraph

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const maxValueChars = 1000

// claimRow is one claims row with the time its superseding claim was made.
type claimRow struct {
	id, fieldPath, value, standing, status, activityID, extractor string
	opportunity, subject, speaker                                 sql.NullString
	confidence                                                    float64
	occurred, created                                             time.Time
	expires, supersededAt                                         sql.NullTime
}

func (b *builder) loadClaims(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `
SELECT c.id::text, c.field_path, c.value::text, c.standing, c.status, c.source_activity_id::text, c.extractor,
       c.opportunity_id::text, c.subject_person_id::text, c.speaker_person_id::text,
       c.confidence::float8, c.occurred_at, c.created_at, c.expires_at, sup.occurred_at
  FROM claims c LEFT JOIN claims sup ON sup.id = c.superseded_by
 WHERE c.account_id = $1::uuid`, b.accountID)
	if err != nil {
		return fmt.Errorf("ctxgraph: read claims: %w", err)
	}
	defer rows.Close()
	var all []claimRow
	for rows.Next() {
		var c claimRow
		if err := rows.Scan(&c.id, &c.fieldPath, &c.value, &c.standing, &c.status, &c.activityID, &c.extractor,
			&c.opportunity, &c.subject, &c.speaker, &c.confidence, &c.occurred, &c.created, &c.expires, &c.supersededAt); err != nil {
			return err
		}
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if b.cut != nil {
		all = b.claimsAtCut(all)
	}
	for _, c := range all {
		label := LabelClaim
		if c.fieldPath == "commitment" {
			label = LabelCommitment
		}
		b.claimLabels[c.id] = label
	}
	for _, c := range all {
		if err := b.projectClaim(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (b *builder) projectClaim(ctx context.Context, c claimRow) error {
	label := b.claimLabels[c.id]
	acts, events, err := b.evidence(ctx, c.activityID)
	if err != nil {
		return err
	}
	validTo := claimValidTo(c)
	extra := map[string]any{
		"field_path": c.fieldPath, "value": clip(c.value, maxValueChars), "standing": c.standing,
		"confidence": round3(c.confidence), "status": c.status, "valid_from": ts(c.occurred), "extractor": c.extractor,
	}
	if v, ok := tsPtr(validTo); ok {
		extra["valid_to"] = v
	}
	b.addNode(newNode([]string{label}, c.id, b.accountID, nodeProps("claims", acts, events, c.created, c.created, extra)))

	ep := func(typ string) map[string]any {
		return edgeProps(c.occurred, validTo, c.status, acts, events, c.created, c.created,
			map[string]any{"confidence": round3(c.confidence), "standing": c.standing})
	}
	edge := func(typ, toLabel, toID, suffix string) {
		b.addEdge(newEdge(typ, "claim:"+c.id+":"+typ+suffix, label, c.id, toLabel, toID, b.accountID, ep(typ)))
	}
	edge(RelAboutAccount, LabelAccount, b.accountID, "")
	if c.subject.Valid {
		edge(RelAboutPerson, LabelPerson, c.subject.String, "")
	}
	if c.opportunity.Valid {
		edge(RelAboutOpportunity, LabelOpportunity, c.opportunity.String, "")
	}
	b.linkClaimSource(c, label, edge)
	return nil
}

// linkClaimSource adds the evidence edges: SUPPORTED_BY the source activity (and its Document, when it
// is a docs activity) for a claim; MADE_IN and MADE_BY for a commitment.
func (b *builder) linkClaimSource(c claimRow, label string, edge func(typ, toLabel, toID, suffix string)) {
	info, known := b.acts[c.activityID]
	if !known {
		b.skip(skipEndpointMissing)
		return
	}
	if label == LabelCommitment {
		edge(RelMadeIn, info.label, c.activityID, "")
		who := c.speaker
		if !who.Valid {
			who = c.subject
		}
		if who.Valid {
			edge(RelMadeBy, LabelPerson, who.String, "")
		}
		return
	}
	edge(RelSupportedBy, info.label, c.activityID, "")
	if info.documentID != "" {
		edge(RelSupportedBy, LabelDocument, info.documentID, ":document")
	}
}

// claimValidTo is when a claim stopped being asserted: its expiry, else the moment a superseding
// claim was made; nil while it stands.
func claimValidTo(c claimRow) *time.Time {
	switch {
	case c.expires.Valid:
		t := c.expires.Time
		return &t
	case c.supersededAt.Valid:
		t := c.supersededAt.Time
		return &t
	}
	return nil
}
