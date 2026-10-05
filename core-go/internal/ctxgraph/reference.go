package ctxgraph

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// maxReferencing bounds how many hits Referencing returns: one is already a finding.
const maxReferencing = 100

// Hit is one node or relationship of the projection that rests on a given id.
type Hit struct {
	Kind string `json:"kind"` // node | edge
	Type string `json:"type"` // label or relationship type
	ID   string `json:"id"`
}

// nodeReferencing and edgeReferencing find the projected elements that are, or are evidence-linked to, one of
// the ids: the node or edge id itself, a source event it was derived from, or an activity it cites. Every
// projected node and edge carries source_event_ids and evidence_activity_ids (docs/neo4j.md).
const (
	nodeReferencing = `
MATCH (n) WHERE n.id IN $ids
   OR any(x IN coalesce(n.source_event_ids, []) WHERE x IN $ids)
   OR any(x IN coalesce(n.evidence_activity_ids, []) WHERE x IN $ids)
RETURN labels(n)[0] AS type, n.id AS id ORDER BY type, id LIMIT $limit`
	edgeReferencing = `
MATCH ()-[r]->() WHERE r.id IN $ids
   OR any(x IN coalesce(r.source_event_ids, []) WHERE x IN $ids)
   OR any(x IN coalesce(r.evidence_activity_ids, []) WHERE x IN $ids)
RETURN type(r) AS type, r.id AS id ORDER BY type, id LIMIT $limit`
)

// Referencing returns the nodes and relationships in Neo4j that are derived from, or cite, any of ids (source
// event ids, activity ids). It is how "nothing derived from this event exists yet" is checked against the graph
// itself and not only against Postgres. An unreachable graph is ErrGraphUnavailable: it is never an empty answer.
func (r *Reader) Referencing(ctx context.Context, ids []string) ([]Hit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	params := map[string]any{"ids": append([]string(nil), ids...), "limit": int64(maxReferencing)}
	var out []Hit
	for _, q := range []struct{ kind, cypher string }{{"node", nodeReferencing}, {"edge", edgeReferencing}} {
		recs, err := r.g.read(ctx, q.cypher, params)
		if err != nil {
			return nil, wrapNeo4j(fmt.Errorf("ctxgraph: find %ss referencing %d ids: %w", q.kind, len(ids), err))
		}
		for _, rec := range recs {
			typ, _ := rec["type"].(string)
			id, _ := rec["id"].(string)
			out = append(out, Hit{Kind: q.kind, Type: typ, ID: id})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	return out, nil
}

// activitiesFrom finds the Activity nodes of an account (or of a deal) that occurred at or after a cutoff.
// Timestamps are fixed-width UTC text (tsLayout), so the comparison is a string comparison.
const activitiesFrom = `
MATCH (n:Activity) WHERE (n.account_id = $account OR n.opportunity_id = $opportunity) AND n.occurred_at >= $cutoff
RETURN labels(n)[0] AS type, n.id AS id ORDER BY id LIMIT $limit`

// ActivitiesFrom returns the Activity (and Conversation) nodes of the account, or of its deal, that occurred at
// or after cutoff: the graph's side of "the world stops before event N". An unreachable graph is
// ErrGraphUnavailable, never an empty answer.
func (r *Reader) ActivitiesFrom(ctx context.Context, accountID, opportunityID string, cutoff time.Time) ([]Hit, error) {
	params := map[string]any{"account": accountID, "opportunity": opportunityID, "cutoff": ts(cutoff), "limit": int64(maxReferencing)}
	recs, err := r.g.read(ctx, activitiesFrom, params)
	if err != nil {
		return nil, wrapNeo4j(fmt.Errorf("ctxgraph: find activities from %s: %w", ts(cutoff), err))
	}
	out := make([]Hit, 0, len(recs))
	for _, rec := range recs {
		typ, _ := rec["type"].(string)
		id, _ := rec["id"].(string)
		out = append(out, Hit{Kind: "node", Type: typ, ID: id})
	}
	return out, nil
}
