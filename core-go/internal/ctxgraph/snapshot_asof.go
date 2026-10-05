package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/signalstore"
)

// BuildSnapshotAsOf derives the account's projection as the world stood strictly before t (ADR-0019): the
// builder the projector uses, with every loader restricted to what was derived from activities whose
// occurred_at is earlier than t. Ids, properties, hashes and ontology validation are the projector's;
// nothing is read from or written to Neo4j. Claim status, supersession, relationship closing and signal
// expiry are those at t; knowledge, rejected claims, episode verdicts and in-place attributes (a person's
// title and display name, a contact's account) have no world time and are left out, and so are the people
// and opportunities with no world-timed evidence before t.
func BuildSnapshotAsOf(ctx context.Context, db claimstore.DB, accountID string, t time.Time) (Snapshot, error) {
	cut := t.UTC()
	return buildSnapshot(ctx, db, accountID, &cut, nil)
}

// BuildSnapshotAsOfRun is BuildSnapshotAsOf for the run whose newest trigger activity occurred at ownTrigger
// (t is that plus one tick). Decision episodes whose newest trigger is not before ownTrigger are left out:
// an earlier arm of the A/B/C benchmark, or a re-run, on the same trigger is not part of that run's world.
func BuildSnapshotAsOfRun(ctx context.Context, db claimstore.DB, accountID string, t, ownTrigger time.Time) (Snapshot, error) {
	cut, own := t.UTC(), ownTrigger.UTC()
	return buildSnapshot(ctx, db, accountID, &cut, &own)
}

// cutArg is the cutoff as a query argument: NULL (no restriction) when building the current projection.
func (b *builder) cutArg() any {
	if b.cut == nil {
		return nil
	}
	return b.cut.UTC()
}

// loadClaimsAsOf computes the claims' standing at the cutoff (a no-op for the current projection).
func (b *builder) loadClaimsAsOf(ctx context.Context) error {
	if b.cut == nil {
		return nil
	}
	var err error
	b.claimsAt, err = claimstore.ClaimsAsOf(ctx, b.db, b.accountID, *b.cut)
	return err
}

// activeClaimIDs are the claims standing at the cutoff, as an array argument; nil for the current projection.
func (b *builder) activeClaimIDs() any {
	if b.cut == nil {
		return nil
	}
	ids := []string{}
	for id, c := range b.claimsAt {
		if c.Status == claims.StatusActive {
			ids = append(ids, id)
		}
	}
	return signalstore.UUIDArray(ids)
}

// claimsAtCut keeps the claims that existed before the cutoff, with the status and supersession they had.
func (b *builder) claimsAtCut(all []claimRow) []claimRow {
	out := make([]claimRow, 0, len(all))
	for _, c := range all {
		at, existed := b.claimsAt[c.id]
		if _, visible := b.acts[c.activityID]; !existed || !visible {
			continue // made at or after the cutoff, by a later activity, or rejected by a human
		}
		c.status, c.supersededAt = string(at.Status), sql.NullTime{}
		if at.SupersededAt != nil {
			c.supersededAt = sql.NullTime{Time: *at.SupersededAt, Valid: true}
		}
		out = append(out, c)
	}
	return out
}

// relsAtCut keeps the relationships that held before the cutoff: opened before it, on evidence from before
// it. One closed at or after the cutoff was still open then.
func (b *builder) relsAtCut(ctx context.Context, all []relRow) ([]relRow, error) {
	out := make([]relRow, 0, len(all))
	for _, r := range all {
		if !r.from.Before(*b.cut) {
			continue
		}
		if r.activity.Valid {
			ok, err := b.actVisible(ctx, r.activity.String)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		if r.to.Valid && !r.to.Time.Before(*b.cut) {
			r.to = sql.NullTime{}
		}
		out = append(out, r)
	}
	return out, nil
}

// actVisible reports whether the activity occurred before the cutoff. The account's own activities were
// loaded already (only the visible ones); any other id is looked up once.
func (b *builder) actVisible(ctx context.Context, id string) (bool, error) {
	if _, ok := b.acts[id]; ok {
		return true, nil
	}
	if v, ok := b.actSeen[id]; ok {
		return v, nil
	}
	var visible bool
	err := b.db.QueryRowContext(ctx, `SELECT occurred_at < $2 FROM activities WHERE id = $1::uuid`, id, b.cut.UTC()).Scan(&visible)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("ctxgraph: look up activity %s: %w", id, err)
	}
	b.actSeen[id] = visible // a missing row is not visible
	return visible, nil
}

// allVisible reports whether every activity occurred before the cutoff.
func (b *builder) allVisible(ctx context.Context, ids []string) (bool, error) {
	for _, id := range ids {
		ok, err := b.actVisible(ctx, id)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// dealStateQuery reads each deal's buying-group state: the current one, or the latest version before the cutoff.
func (b *builder) dealStateQuery() (string, []any) {
	if b.cut == nil {
		return `SELECT opportunity_id::text, state::text, as_of FROM opportunity_state WHERE account_id = $1::uuid ORDER BY opportunity_id`, []any{b.accountID}
	}
	return `SELECT DISTINCT ON (opportunity_id) opportunity_id::text, state::text, as_of FROM opportunity_state_history
 WHERE account_id = $1::uuid AND as_of < $2 ORDER BY opportunity_id, as_of DESC, version DESC`, []any{b.accountID, b.cut.UTC()}
}

// loadEpisodesAtCut projects the decision episodes whose trigger activities all occurred before the cutoff.
// An episode's world time is its latest trigger's (its wall-clock created_at is computed-at); one with no
// trigger cannot be placed and is left out. Neither the human's verdict nor the status is projected. Knowledge is not projected (no lifecycle history to read).
func (b *builder) loadEpisodesAtCut(ctx context.Context, eps []episodeRow) error {
	for _, e := range eps {
		latest, placed := b.latestTrigger(e.triggers)
		if !placed {
			continue
		}
		if b.episodesBefore != nil && !latest.Before(*b.episodesBefore) {
			continue
		}
		// The verdict and the status are written later, in place, with no world time: a world read carries neither.
		e.created, e.used, e.action = latest, nil, ""
		if err := b.projectEpisode(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// latestTrigger is the newest occurred_at of the trigger activities; placed is false when there is none or
// one of them is not visible at the cutoff.
func (b *builder) latestTrigger(triggers []string) (latest time.Time, placed bool) {
	if len(triggers) == 0 {
		return latest, false
	}
	for _, id := range triggers {
		info, ok := b.acts[id]
		if !ok {
			return latest, false
		}
		if info.occurred.After(latest) {
			latest = info.occurred
		}
	}
	return latest, true
}
