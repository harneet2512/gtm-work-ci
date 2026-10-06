package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Standing values (common.v1.json#standing), strongest first.
const (
	StandingHumanApproved    = "human_approved"
	StandingCRMExplicit      = "crm_explicit"
	StandingFirstPartyRecord = "first_party_record" // deterministic facts: calendar, headers, org file, rules
	StandingFirstPartyAI     = "first_party_ai"
	StandingThirdParty       = "third_party"
)

// Relationship types (common.v1.json#relType) that WP5 writes or exposes.
const (
	RelWorksAt        = "works_at"
	RelParticipatedIn = "participated_in"
	RelBelongsTo      = "belongs_to"
	RelOwns           = "owns"
	RelAbout          = "about"
	RelInvolves       = "involves"
	RelSharedWith     = "shared_with"
	RelReportsTo      = "reports_to"
	RelSupports       = "supports"
	RelEconomicBuyer  = "economic_buyer_for"
	RelInfluences     = "influences"
	RelChampionFor    = "champion_for"
)

// standings lists every standing, strongest first.
func standings() []string {
	return []string{StandingHumanApproved, StandingCRMExplicit, StandingFirstPartyRecord, StandingFirstPartyAI, StandingThirdParty}
}

// standingRank orders standings (higher outranks); 0 means unknown. It mirrors claims.standing_rank.
func standingRank(s string) int {
	switch s {
	case StandingHumanApproved:
		return 5
	case StandingCRMExplicit:
		return 4
	case StandingFirstPartyRecord:
		return 3
	case StandingFirstPartyAI:
		return 2
	case StandingThirdParty:
		return 1
	}
	return 0
}

// EntityRef addresses a node of the graph: an entity table row, an activity, or a document
// (documents have no table; see DocumentID).
type EntityRef struct {
	Type EntityType
	ID   string
}

// Edge is one row of relationships.
type Edge struct {
	ID               string
	Src              EntityRef
	Rel              string
	Dst              EntityRef
	Standing         string
	Confidence       float64
	SourceActivityID string
	Evidence         json.RawMessage
	ValidFrom        time.Time
	ValidTo          *time.Time
}

// EdgeSpec describes an edge to assert. Edges are never deleted: replacing one closes it.
type EdgeSpec struct {
	Src              EntityRef
	Rel              string
	Dst              EntityRef
	Standing         string
	Confidence       float64
	SourceActivityID string
	// Evidence records why the edge exists (the rule, the source file).
	Evidence  json.RawMessage
	ValidFrom time.Time
	// Exclusive makes the edge the only open one of its type into Dst, for relationships that
	// have one holder (champion, economic buyer): open edges of the same type from other
	// sources, whose standing does not outrank this one, are closed. Concurrent exclusive
	// upserts into the same (type, Dst) are serialized.
	Exclusive bool
}

func (s EdgeSpec) validate() error {
	switch {
	case s.Rel == "":
		return errors.New("graph: edge needs a relationship type")
	case standingRank(s.Standing) == 0:
		return fmt.Errorf("graph: unknown standing %q", s.Standing)
	case s.Confidence < 0 || s.Confidence > 1:
		return fmt.Errorf("graph: confidence %v outside [0,1]", s.Confidence)
	case s.Src == s.Dst:
		return errors.New("graph: an edge cannot connect a node to itself")
	case s.ValidFrom.IsZero():
		return errors.New("graph: edge needs valid_from")
	}
	return nil
}

// UpsertEdge asserts an edge and reports whether a new row was written. Per (src, type, dst):
//   - an open edge at the same standing makes this a no-op (idempotent);
//   - an open edge at a lower standing is closed at ValidFrom and replaced, so the stronger
//     source supersedes it;
//   - an open edge at a higher standing does not block this one: the two coexist and the
//     standing decides which one counts. A claim-derived edge is never silently dropped
//     because a rule-derived one of a different meaning exists.
//
// Claim-derived edges (WP6) use this with standing first_party_ai, and Exclusive for
// single-holder relationships.
func UpsertEdge(ctx context.Context, q Txn, s EdgeSpec) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if s.Exclusive {
		// Taken before the triple lock by every caller, so two claimants cannot deadlock.
		if err := lock(ctx, q, "edge-exclusive", s.Rel+"|"+string(s.Dst.Type)+"|"+s.Dst.ID); err != nil {
			return false, err
		}
		if err := closeOthers(ctx, q, s); err != nil {
			return false, err
		}
	}
	if err := lock(ctx, q, "edge", string(s.Src.Type)+s.Src.ID+s.Rel+string(s.Dst.Type)+s.Dst.ID); err != nil {
		return false, err
	}
	same, err := hasOpenEdge(ctx, q, s)
	if err != nil || same {
		return false, err
	}
	if lower := standingsBelow(s.Standing); len(lower) > 0 { // an empty list would mean "any standing"
		src, dst := s.Src, s.Dst
		if _, err := CloseEdges(ctx, q, EdgeFilter{Src: &src, Dst: &dst, Rels: []string{s.Rel}, Standings: lower}, s.ValidFrom); err != nil {
			return false, err
		}
	}
	res, err := q.ExecContext(ctx, `
INSERT INTO relationships (src_type, src_id, rel_type, dst_type, dst_id, standing, confidence, source_activity_id, evidence, valid_from)
VALUES ($1, $2::uuid, $3, $4, $5::uuid, $6, $7, $8::uuid, $9::jsonb, $10)
ON CONFLICT DO NOTHING`,
		string(s.Src.Type), s.Src.ID, s.Rel, string(s.Dst.Type), s.Dst.ID, s.Standing, s.Confidence,
		optional(s.SourceActivityID), optionalJSON(s.Evidence), s.ValidFrom)
	if err != nil {
		return false, fmt.Errorf("graph: insert %s edge: %w", s.Rel, err)
	}
	n, err := rowsAffected(res, "insert "+s.Rel+" edge")
	return n > 0, err
}

func hasOpenEdge(ctx context.Context, q DBTX, s EdgeSpec) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM relationships
 WHERE src_type = $1 AND src_id = $2::uuid AND rel_type = $3 AND dst_type = $4 AND dst_id = $5::uuid
   AND valid_to IS NULL AND standing = $6)`,
		string(s.Src.Type), s.Src.ID, s.Rel, string(s.Dst.Type), s.Dst.ID, s.Standing).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("graph: look up open edge: %w", err)
	}
	return ok, nil
}

func closeOthers(ctx context.Context, q Txn, s EdgeSpec) error {
	dst, other := s.Dst, s.Src
	_, err := CloseEdges(ctx, q, EdgeFilter{Dst: &dst, NotSrc: &other, Rels: []string{s.Rel}, Standings: standingsAtMost(s.Standing)}, s.ValidFrom)
	return err
}

// EdgeFilter selects open edges. Nil/empty fields match everything.
type EdgeFilter struct {
	Src, Dst, NotSrc, NotDst *EntityRef
	Rels                     []string
	Standings                []string
}

// CloseEdges closes (valid_to = at, or just after valid_from if at is not later) every open
// edge the filter matches and returns how many it closed.
func CloseEdges(ctx context.Context, q DBTX, f EdgeFilter, at time.Time) (int, error) {
	ref := func(r *EntityRef) (any, any) {
		if r == nil {
			return nil, nil
		}
		return string(r.Type), r.ID
	}
	srcT, srcID := ref(f.Src)
	dstT, dstID := ref(f.Dst)
	nsT, nsID := ref(f.NotSrc)
	ndT, ndID := ref(f.NotDst)
	rels, stands := f.Rels, f.Standings
	if rels == nil {
		rels = []string{}
	}
	if stands == nil {
		stands = []string{}
	}
	res, err := q.ExecContext(ctx, `
UPDATE relationships SET valid_to = `+closedAtSQL(1)+`
 WHERE valid_to IS NULL
   AND ($2::text IS NULL OR (src_type = $2 AND src_id = $3::uuid))
   AND ($4::text IS NULL OR (dst_type = $4 AND dst_id = $5::uuid))
   AND ($6::text IS NULL OR NOT (src_type = $6 AND src_id = $7::uuid))
   AND ($8::text IS NULL OR NOT (dst_type = $8 AND dst_id = $9::uuid))
   AND (cardinality($10::text[]) = 0 OR rel_type = ANY($10))
   AND (cardinality($11::text[]) = 0 OR standing = ANY($11))`,
		at, srcT, srcID, dstT, dstID, nsT, nsID, ndT, ndID, rels, stands)
	if err != nil {
		return 0, fmt.Errorf("graph: close edges: %w", err)
	}
	return rowsAffected(res, "close edges")
}

func standingsAtMost(s string) []string {
	return standingsWhere(func(r int) bool { return r <= standingRank(s) })
}
func standingsBelow(s string) []string {
	return standingsWhere(func(r int) bool { return r < standingRank(s) })
}

func standingsWhere(keep func(rank int) bool) []string {
	out := []string{}
	for _, name := range standings() {
		if keep(standingRank(name)) {
			out = append(out, name)
		}
	}
	return out
}
