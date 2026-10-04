package evalreport_test

// S2: Generate reads every table inside one read-only REPEATABLE READ transaction, so the report is
// internally consistent under concurrent writes and provably never writes.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

// spyDB records the options Generate opens its read transaction with.
type spyDB struct {
	*sql.DB
	opts *sql.TxOptions
}

func (s *spyDB) BeginTx(ctx context.Context, o *sql.TxOptions) (*sql.Tx, error) {
	s.opts = o
	return s.DB.BeginTx(ctx, o)
}

func TestGenerateRunsInOneReadOnlyRepeatableReadTransaction(t *testing.T) {
	spy := &spyDB{DB: env.DB}
	if _, err := evalreport.Generate(context.Background(), spy, evalreport.Options{}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if spy.opts == nil || !spy.opts.ReadOnly || spy.opts.Isolation != sql.LevelRepeatableRead {
		t.Fatalf("tx options = %+v, want read-only repeatable read", spy.opts)
	}
}

func TestGenerateIgnoresAWriteCommittedMidReport(t *testing.T) {
	e := newEpisode(t)
	const tag = "grounding:v88"
	var inserted string
	restore := evalreport.SetBetweenReads(func(step int) {
		if step == 1 { // after the first table read: the snapshot already exists
			inserted = insertEval(t, e.seed.RunID, 1, "grounding", tag, "semantic", "pass", nil, t0)
		}
	})
	set := generate(t, evalreport.Options{})
	restore()
	t.Cleanup(func() { _, _ = env.DB.Exec(`DELETE FROM eval_runs WHERE id = $1::uuid`, inserted) })
	if inserted == "" {
		t.Fatal("the mid-report write never ran: the between-reads seam is not wired")
	}
	if _, ok := set.Reports[tag]; ok {
		t.Fatalf("a row committed after the snapshot leaked into the report (%v)", keysOf(set))
	}
	if _, ok := generate(t, evalreport.Options{}).Reports[tag]; !ok {
		t.Fatal("control: a fresh report must see the committed row")
	}
}
