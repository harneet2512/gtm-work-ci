package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// restrictedVisibilities keep an activity from waking a shared workflow (common.v1.json permissions).
var restrictedVisibilities = map[string]bool{"owner_only": true, "restricted": true}

// loadFacts reads the activities folded into a recompute as signal facts, and whether any of them is
// too restricted to trigger the workflow. Who an activity came from is read off its `from` participants:
// a resolved contact, or an address outside ownDomain, is the customer; a resolved employee, or an
// address inside it, is the rep.
func loadFacts(ctx context.Context, db claimstore.DB, ids []string, ownDomain string) (facts []signals.ActivityFact, denied bool, err error) {
	rows, err := db.QueryContext(ctx, `
SELECT id::text, activity_type, occurred_at, COALESCE(permissions->>'visibility', 'org')
  FROM activities WHERE id = ANY($1::uuid[]) ORDER BY occurred_at, id`, signalstore.UUIDArray(ids))
	if err != nil {
		return nil, false, fmt.Errorf("pipeline: read folded activities: %w", err)
	}
	defer rows.Close()
	index := map[string]int{}
	for rows.Next() {
		var f signals.ActivityFact
		var visibility string
		if err := rows.Scan(&f.ID, &f.Type, &f.OccurredAt, &visibility); err != nil {
			return nil, false, fmt.Errorf("pipeline: scan activity: %w", err)
		}
		f.OccurredAt = f.OccurredAt.UTC()
		denied = denied || restrictedVisibilities[visibility]
		index[f.ID] = len(facts)
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("pipeline: read folded activities: %w", err)
	}
	if err := attachSenders(ctx, db, ids, ownDomain, facts, index); err != nil {
		return nil, false, err
	}
	return facts, denied, nil
}

func attachSenders(ctx context.Context, db claimstore.DB, ids []string, ownDomain string, facts []signals.ActivityFact, index map[string]int) error {
	rows, err := db.QueryContext(ctx, `
SELECT ap.activity_id::text, ap.raw_identity, COALESCE(p.kind, '')
  FROM activity_participants ap LEFT JOIN people p ON p.id = ap.person_id
 WHERE ap.activity_id = ANY($1::uuid[]) AND ap.role = 'from'`, signalstore.UUIDArray(ids))
	if err != nil {
		return fmt.Errorf("pipeline: read senders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw, kind string
		if err := rows.Scan(&id, &raw, &kind); err != nil {
			return fmt.Errorf("pipeline: scan sender: %w", err)
		}
		i, ok := index[id]
		if !ok {
			continue
		}
		customer := kind == "contact" || (kind == "" && signals.IsCustomerIdentity(raw, ownDomain))
		rep := kind == "employee" || (kind == "" && !customer && strings.Contains(raw, "@"))
		facts[i].FromCustomer = facts[i].FromCustomer || customer
		facts[i].FromRep = facts[i].FromRep || rep
	}
	return rows.Err()
}
