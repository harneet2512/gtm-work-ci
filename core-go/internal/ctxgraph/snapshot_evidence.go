package ctxgraph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// loadFirstEvidence reads, in cut mode, the first world time at which each person and opportunity is
// evidenced in this account (ADR-0019 section 2). An entity with no world-timed evidence before the cutoff is
// not part of the world at that time: whatever created it later (a new participant, a re-attribution) is
// not visible. There is no "timeless" exception.
func (b *builder) loadFirstEvidence(ctx context.Context) error {
	if b.cut == nil {
		return nil
	}
	b.firstSeen = map[string]time.Time{}
	rows, err := b.db.QueryContext(ctx, firstEvidenceSQL, b.accountID)
	if err != nil {
		return fmt.Errorf("ctxgraph: read first evidence: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		var first time.Time
		if err := rows.Scan(&kind, &id, &first); err != nil {
			return err
		}
		b.firstSeen[kind+":"+id] = first
	}
	return rows.Err()
}

// evidencedBeforeCut reports whether the person or opportunity has world-timed evidence before the cutoff.
// Without a cutoff (the current projection) everything is visible.
func (b *builder) evidencedBeforeCut(kind, id string) bool {
	if b.cut == nil {
		return true
	}
	first, ok := b.firstSeen[kind+":"+id]
	return ok && first.Before(*b.cut)
}

// evidencedIDs are the ids of the kind that are evidenced before the cutoff, in a stable order.
func (b *builder) evidencedIDs(kind string) []string {
	out := []string{}
	prefix := kind + ":"
	for key, first := range b.firstSeen {
		if strings.HasPrefix(key, prefix) && first.Before(*b.cut) {
			out = append(out, strings.TrimPrefix(key, prefix))
		}
	}
	sort.Strings(out)
	return out
}

// firstEvidenceSQL is the earliest world time of each person and opportunity named by this account's
// evidence: participations, claims, activities, and relationships whose other end is the account, one of its
// deals or one of its activities (a relationship counts from the later of its valid_from and its source
// activity's occurred_at).
const firstEvidenceSQL = `
WITH scope(t, id) AS (
  SELECT 'account', id FROM accounts WHERE id = $1::uuid
  UNION ALL SELECT 'opportunity', id FROM opportunities WHERE account_id = $1::uuid
  UNION ALL SELECT 'activity', id FROM activities WHERE account_id = $1::uuid),
rel AS (
  SELECT r.src_type::text AS src_type, r.src_id, r.dst_type::text AS dst_type, r.dst_id,
         GREATEST(r.valid_from, COALESCE((SELECT occurred_at FROM activities WHERE id = r.source_activity_id), r.valid_from)) AS at
    FROM relationships r
   WHERE EXISTS (SELECT 1 FROM scope s WHERE s.t = r.src_type::text AND s.id = r.src_id)
      OR EXISTS (SELECT 1 FROM scope s WHERE s.t = r.dst_type::text AND s.id = r.dst_id))
SELECT t, id::text, min(at) FROM (
  SELECT 'person' AS t, p.person_id AS id, a.occurred_at AS at FROM activity_participants p JOIN activities a ON a.id = p.activity_id
   WHERE a.account_id = $1::uuid AND p.person_id IS NOT NULL
  UNION ALL SELECT 'person', subject_person_id, occurred_at FROM claims WHERE account_id = $1::uuid AND subject_person_id IS NOT NULL
  UNION ALL SELECT 'person', speaker_person_id, occurred_at FROM claims WHERE account_id = $1::uuid AND speaker_person_id IS NOT NULL
  UNION ALL SELECT 'opportunity', opportunity_id, occurred_at FROM activities WHERE account_id = $1::uuid AND opportunity_id IS NOT NULL
  UNION ALL SELECT 'opportunity', opportunity_id, occurred_at FROM claims WHERE account_id = $1::uuid AND opportunity_id IS NOT NULL
  UNION ALL SELECT 'person', src_id, at FROM rel WHERE src_type = 'person'
  UNION ALL SELECT 'person', dst_id, at FROM rel WHERE dst_type = 'person'
  UNION ALL SELECT 'opportunity', src_id, at FROM rel WHERE src_type = 'opportunity'
  UNION ALL SELECT 'opportunity', dst_id, at FROM rel WHERE dst_type = 'opportunity'
) x GROUP BY t, id`
