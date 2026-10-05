package claimstore

import (
	"context"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// ClaimAt is a claim's standing as the world stood strictly before a time (ADR-0019).
type ClaimAt struct {
	// Status is what adjudication gives the claim among the claims of that time: active, outranked,
	// superseded or expired. It differs from the stored status whenever a later claim changed it.
	Status claims.Status
	// SupersededBy and SupersededAt name the claim that replaced this one and when it was made; empty
	// unless Status is superseded.
	SupersededBy string
	SupersededAt *time.Time
}

// ClaimsAsOf re-adjudicates the account's claims as the world stood strictly before t: only claims made
// by activities with occurred_at < t take part, at now = t (so an expiry at exactly t has not happened).
// The result maps each such claim id to its status then; claims made at or after t are absent.
//
// Stored statuses cannot answer this. A claim outranked later carries no superseded_by pointer, and a
// superseded one points at a claim that may not exist yet at t, so the pass is run again over the claims
// of that time with the same Adjudicate the recompute uses. A claim a human rejected is absent from every
// answer: the rejection has no world time, so neither "rejected" nor "active" is true at t.
func ClaimsAsOf(ctx context.Context, db DB, accountID string, t time.Time) (map[string]ClaimAt, error) {
	cs, err := queryClaims(ctx, db, `JOIN activities a ON a.id = c.source_activity_id
 WHERE c.account_id = $1::uuid AND c.status <> 'rejected' AND c.occurred_at < $2 AND a.occurred_at < $2`, accountID, t.UTC())
	if err != nil {
		return nil, err
	}
	occurred := make(map[string]time.Time, len(cs))
	for i := range cs {
		occurred[cs[i].ID] = cs[i].OccurredAt
		// A blank stored state makes Adjudicate report every claim, not only the ones that changed.
		cs[i].Status, cs[i].SupersededBy = "", ""
	}
	updates := claims.Adjudicate(cs, t.Add(-time.Microsecond)).Updates
	out := make(map[string]ClaimAt, len(cs))
	for id := range occurred {
		u := updates[id]
		if u.Status == "" { // not placed in any slot: standing, as nothing outranks it
			u.Status = claims.StatusActive
		}
		c := ClaimAt{Status: u.Status, SupersededBy: u.SupersededBy}
		if at, ok := occurred[u.SupersededBy]; ok && u.SupersededBy != "" {
			c.SupersededAt = &at
		}
		out[id] = c
	}
	return out, nil
}
