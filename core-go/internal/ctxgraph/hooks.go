package ctxgraph

import (
	"context"
	"database/sql"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// IngestHook returns the ingest.Options.AfterAttribute hook: an attributed activity enqueues the
// account's graph projection in the ingest transaction, so the Activity, its participants and its
// edges reach Neo4j without waiting for claim extraction.
func IngestHook() ingest.AttributeHook {
	return func(ctx context.Context, tx *sql.Tx, accountID, activityID string, now time.Time) error {
		return Enqueue(ctx, tx, accountID, []string{activityID}, ReasonIngest, now)
	}
}

// WithProjection decorates the recompute hook (the WP8 pipeline): after the inner hook has written the
// state diff, signals, trigger evaluation and any AgentRun, the account's graph projection is enqueued
// in the same transaction. A run therefore never exists without an unfinished projection job, which
// is what the Barrier checks before an agent may read the graph.
func WithProjection(inner coalesce.Hook, clk clock.Clock) coalesce.Hook {
	if clk == nil {
		clk = clock.Real{}
	}
	return projectionHook{inner: inner, clk: clk}
}

type projectionHook struct {
	inner coalesce.Hook
	clk   clock.Clock
}

// AfterRecompute implements coalesce.Hook.
func (h projectionHook) AfterRecompute(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
	activityIDs []string, conflicts []claims.Conflict) error {
	if h.inner != nil {
		if err := h.inner.AfterRecompute(ctx, tx, prev, next, activityIDs, conflicts); err != nil {
			return err
		}
	}
	return Enqueue(ctx, tx, next.AccountID, activityIDs, ReasonRecompute, h.clk.Now())
}
