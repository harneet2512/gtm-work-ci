package ctxgraph

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

func pendingJobs(t *testing.T) int {
	t.Helper()
	var n int
	if err := pg.DB.QueryRow(`SELECT count(*) FROM graph_projection_jobs WHERE completed_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The outbox row commits with the Postgres change or not at all.
func TestTheIngestHookEnqueuesInTheCallersTransaction(t *testing.T) {
	s := seedWorld(t)
	hook := IngestHook()

	tx, err := pg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := hook(bg, tx, s["A"], s["ACT_MAIL"], time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := pendingJobs(t); n != 0 {
		t.Fatalf("a rolled-back ingest left %d jobs", n)
	}

	tx, err = pg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, act := range []string{s["ACT_MAIL"], s["ACT_CALL"]} {
		if err := hook(bg, tx, s["A"], act, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var acts int
	if err := pg.DB.QueryRow(`SELECT cardinality(activity_ids) FROM graph_projection_jobs WHERE completed_at IS NULL`).Scan(&acts); err != nil || acts != 2 || pendingJobs(t) != 1 {
		t.Fatalf("two activities of one account must coalesce into one job: %d activities, %v", acts, err)
	}
}

func TestTheRecomputeHookRunsTheInnerHookFirstAndEnqueuesAfterIt(t *testing.T) {
	s := seedWorld(t)
	var order []string
	inner := coalesce.HookFunc(func(_ context.Context, _ *sql.Tx, _ *reducer.AccountState, _ reducer.AccountState, _ []string, _ []claims.Conflict) error {
		order = append(order, "inner")
		return nil
	})
	tx, err := pg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	h := WithProjection(inner, nil)
	if err := h.AfterRecompute(bg, tx, nil, reducer.AccountState{AccountID: s["A"]}, []string{s["ACT_CALL"]}, nil); err != nil {
		t.Fatal(err)
	}
	var reason string
	if err := tx.QueryRow(`SELECT reasons[1] FROM graph_projection_jobs WHERE account_id = $1::uuid`, s["A"]).Scan(&reason); err != nil || reason != ReasonRecompute {
		t.Fatalf("reason = %q %v", reason, err)
	}
	if len(order) != 1 {
		t.Fatalf("inner hook ran %d times", len(order))
	}
	if err := Enqueue(bg, tx, "", nil, ReasonManual, time.Now()); err == nil {
		t.Error("an enqueue without an account must fail")
	}
}
