// Package visibility holds the one definition of which activities the agent may not see (PR #29's
// withholding rule), shared by the context tools and the graph neighborhood so they cannot disagree.
package visibility

import (
	"context"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// HiddenActivities are the account's activities the agent may not see: everything whose visibility is
// not exactly 'org' (a missing key counts as hidden; team and person allow-lists need an agent identity
// the run does not carry). Values with such evidence are withheld, their ids are dropped from activity
// lists, and the evidence and activity reads never select them.
func HiddenActivities(ctx context.Context, db claimstore.DB, accountID string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id::text FROM activities WHERE account_id = $1::uuid
 AND COALESCE(permissions ->> 'visibility', '') <> 'org'`, accountID)
	if err != nil {
		return nil, fmt.Errorf("visibility: read hidden activities: %w", err)
	}
	defer rows.Close()
	hidden := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		hidden[id] = true
	}
	return hidden, rows.Err()
}
