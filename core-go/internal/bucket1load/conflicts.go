package bucket1load

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1"
)

// loadConflicts links the claims the reducer recorded as competing or conflicting on a state field (the field's
// winning claim, its competing claims and its conflicts at the episode's state version) to each other, so a
// contradiction the system kept is seen as kept rather than silently resolved.
func loadConflicts(ctx context.Context, db *sql.DB, run runRow, ep *bucket1.Episode) error {
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT state -> 'fields' FROM state_history WHERE account_id = $1::uuid AND version <= $2
 ORDER BY version DESC LIMIT 1`, run.accountID, run.stateVersion).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || len(raw) == 0 {
		ep.StateUnread = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("bucket1load: state fields of account %s: %w", run.accountID, err)
	}
	var fields map[string]struct {
		Winning   *string  `json:"winning_claim_id"`
		Competing []string `json:"competing_claim_ids"`
		Conflicts []struct {
			ClaimID string `json:"claim_id"`
		} `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("bucket1load: decode state fields of account %s: %w", run.accountID, err)
	}
	link := map[string]map[string]bool{}
	ep.WinningClaims = map[string]string{}
	for name, f := range fields {
		if f.Winning != nil {
			ep.WinningClaims[name] = *f.Winning
		}
		var group []string
		if f.Winning != nil {
			group = append(group, *f.Winning)
		}
		group = append(group, f.Competing...)
		for _, c := range f.Conflicts {
			group = append(group, c.ClaimID)
		}
		for _, a := range group {
			for _, b := range group {
				if a != b {
					if link[a] == nil {
						link[a] = map[string]bool{}
					}
					link[a][b] = true
				}
			}
		}
	}
	apply := func(cs []bucket1.Claim) {
		for i := range cs {
			for other := range link[cs[i].ID] {
				cs[i].ConflictsWith = appendOnce(cs[i].ConflictsWith, other)
			}
		}
	}
	apply(ep.Claims)
	apply(ep.PriorClaims)
	return nil
}
