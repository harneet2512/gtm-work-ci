package ctxgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// NeighborhoodAsOf is Neighborhood as the world stood strictly before t (ADR-0019). The snapshot is built
// from Postgres at t with the builder the projector uses (BuildSnapshotAsOf) in one repeatable-read
// transaction, then read through the same viewBuilder as a current read, so the response has the same
// shape and ids. It reads nothing from Neo4j, so it works while the graph database is down and is the same
// before and after the projection is deleted and rebuilt: projection.complete is true, projected_at null.
func (r *Reader) NeighborhoodAsOf(ctx context.Context, accountID string, p Params, hidden map[string]bool, t time.Time) (View, error) {
	return r.neighborhoodAsOf(ctx, accountID, p, hidden, t, nil)
}

func (r *Reader) neighborhoodAsOf(ctx context.Context, accountID string, p Params, hidden map[string]bool, t time.Time, ownTrigger *time.Time) (View, error) {
	p, err := normalizeParams(p)
	if err != nil {
		return View{}, err
	}
	if !readmodel.ValidUUID(accountID) {
		return View{}, ErrAccountUnknown
	}
	if err := r.checkAccount(ctx, accountID); err != nil {
		return View{}, err
	}
	snap, err := r.snapshotAsOf(ctx, accountID, t, ownTrigger)
	if err != nil {
		return View{}, err
	}
	b := &viewBuilder{r: r, ctx: ctx, account: accountID, p: p, hidden: hidden, mem: newMemGraph(snap, t),
		view: View{AccountID: accountID, Sections: map[string][]string{}}}
	if err := b.build(); err != nil {
		return View{}, err
	}
	b.view.Projection = ProjectionStatus{Complete: true}
	return b.view, nil
}

func (r *Reader) snapshotAsOf(ctx context.Context, accountID string, t time.Time, ownTrigger *time.Time) (Snapshot, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Snapshot{}, fmt.Errorf("ctxgraph: begin world read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var snap Snapshot
	if ownTrigger != nil {
		snap, err = BuildSnapshotAsOfRun(ctx, tx, accountID, t, *ownTrigger)
	} else {
		snap, err = BuildSnapshotAsOf(ctx, tx, accountID, t)
	}
	if errors.Is(err, ErrAccountNotFound) {
		return Snapshot{}, ErrAccountUnknown
	}
	return snap, err
}

// NeighborhoodAsOf is the operator view of an account's graph as the world stood strictly before t.
func (s *Service) NeighborhoodAsOf(ctx context.Context, accountID string, p Params, t time.Time) (View, error) {
	return s.Reader.NeighborhoodAsOf(ctx, accountID, p, nil, t)
}

// NeighborhoodItemsAsOf is the graph_neighborhood ctx tool for a run whose world is cut at t
// (corectx.GraphReader): the same packet items as NeighborhoodItems, from the world graph. t is the run's
// cutoff and ownTrigger the occurred_at of its newest trigger activity: decision episodes on that trigger or
// later are not part of the run's world (BuildSnapshotAsOfRun).
func (r *Reader) NeighborhoodItemsAsOf(ctx context.Context, accountID string, limit int, hidden map[string]bool, t, ownTrigger time.Time) ([]any, bool, error) {
	view, err := r.neighborhoodAsOf(ctx, accountID, Params{SectionLimit: toolSectionLimit(limit)}, hidden, t, &ownTrigger)
	if err != nil {
		return nil, false, err
	}
	items, truncated := toolItems(view)
	return items, truncated, nil
}
