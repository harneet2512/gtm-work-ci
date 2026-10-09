package knowledgestore

import (
	"context"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
)

// offered reports whether k (already replayed to the decision's moment) may be offered: the pure rules
// (knowledge.Rules.ApplicableKnowledge) and, for a narrow candidate only, two store facts the rules cannot see.
// A narrow candidate rests on one human correction, so a correction the same human replaced (a newer knowledge
// object seeded from the same episode by the same kind of correction, created by asOf) is not offered, and
// neither is one whose linked evaluator versions are all retired (the criterion it came with was withdrawn).
// Knowledge in an applicable status is judged by the rules alone. asOf nil means no time bound.
func offered(ctx context.Context, q Querier, rules knowledge.Rules, k knowledge.Knowledge, asOf *time.Time) (bool, error) {
	if !rules.ApplicableKnowledge(k) {
		return false, nil
	}
	if rules.Applicable(k.Status) {
		return true, nil
	}
	var bound any
	if asOf != nil {
		bound = asOf.UTC()
	}
	var superseded, retired bool
	if err := q.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM knowledge me, knowledge o WHERE me.id = $1 AND o.id <> me.id
   AND o.provenance->>'created_from' = me.provenance->>'created_from'
   AND o.provenance->>'source_decision_episode_id' = me.provenance->>'source_decision_episode_id'
   AND ($2::timestamptz IS NULL OR o.created_at <= $2::timestamptz)
   AND (o.created_at, length(o.key), o.key) > (me.created_at, length(me.key), me.key)),
 EXISTS (SELECT 1 FROM evaluator_versions WHERE knowledge_id = $1)
   AND NOT EXISTS (SELECT 1 FROM evaluator_versions WHERE knowledge_id = $1 AND status <> 'retired')`,
		k.ID, bound).Scan(&superseded, &retired); err != nil {
		return false, fmt.Errorf("check whether narrow candidate %s is still offered: %w", k.ID, err)
	}
	return !superseded && !retired, nil
}
