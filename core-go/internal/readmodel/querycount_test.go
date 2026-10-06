package readmodel_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// queryCounter is a pgx tracer that counts every statement sent to the database.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.n.Add(1)
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// countedReader is a Reader on its own connection pool whose statements are counted.
func countedReader(t *testing.T) (*readmodel.Reader, *queryCounter) {
	t.Helper()
	cfg, err := pgx.ParseConfig(env.URL)
	if err != nil {
		t.Fatal(err)
	}
	counter := &queryCounter{}
	cfg.Tracer = counter
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	r, err := readmodel.New(db)
	if err != nil {
		t.Fatal(err)
	}
	return r, counter
}

func statementsFor(t *testing.T, fn func(r *readmodel.Reader)) int64 {
	t.Helper()
	r, counter := countedReader(t)
	fn(r)
	return counter.n.Load()
}

func addRuns(t *testing.T, accountID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		// 'failed' is not an open status, so any number of runs may share the account.
		if _, _, err := ctxfixture.InsertRun(context.Background(), env.DB, accountID, "failed"); err != nil {
			t.Fatal(err)
		}
	}
}

// A list page costs a fixed number of statements, however many rows it holds: the per-row reads are batched.
func TestListAccountsStatementCountDoesNotGrowWithThePage(t *testing.T) {
	for i := 0; i < 12; i++ {
		seedAccount(t, fmt.Sprintf("Batch Acct %02d", i))
	}
	small := statementsFor(t, func(r *readmodel.Reader) {
		if _, err := r.ListAccounts(context.Background(), 2, ""); err != nil {
			t.Fatal(err)
		}
	})
	large := statementsFor(t, func(r *readmodel.Reader) {
		page, err := r.ListAccounts(context.Background(), 12, "")
		if err != nil || len(page.Items) != 12 {
			t.Fatalf("page = %d items, %v", len(page.Items), err)
		}
	})
	if large != small {
		t.Fatalf("statements: %d for 2 accounts, %d for 12: the page is not batched", small, large)
	}
}

func TestListRunsStatementCountDoesNotGrowWithThePage(t *testing.T) {
	accountID, _ := seedAccount(t, "Batch Runs Acct")
	addRuns(t, accountID, 11)
	filter := func(limit int) readmodel.RunFilter { return readmodel.RunFilter{AccountID: accountID, Limit: limit} }
	small := statementsFor(t, func(r *readmodel.Reader) {
		if _, err := r.ListRuns(context.Background(), filter(2)); err != nil {
			t.Fatal(err)
		}
	})
	large := statementsFor(t, func(r *readmodel.Reader) {
		page, err := r.ListRuns(context.Background(), filter(12))
		if err != nil || len(page.Items) != 12 {
			t.Fatalf("page = %d items, %v", len(page.Items), err)
		}
	})
	if large != small {
		t.Fatalf("statements: %d for 2 runs, %d for 12: the page is not batched", small, large)
	}
}
