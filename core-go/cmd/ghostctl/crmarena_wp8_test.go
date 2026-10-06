package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/pipeline"
	"github.com/harneet2512/gtm-work/core-go/internal/runs"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// HAR-106 acceptance on the CRMArena sample: replaying the imported Salesforce activity through the
// coalescer with the WP8 pipeline hooked in yields state diffs, signals and trigger evaluations, and
// every ineligible evaluation says why. Rule extractors only (no worker): the CRM-explicit claims
// (stage, owner, amount, ...) are enough to move state.
func TestCRMArenaSampleReplayYieldsDiffsSignalsAndEvaluations(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env, err := storetest.Start(context.Background())
	if err != nil {
		t.Fatalf("start db: %v", err)
	}
	defer env.Close()
	t.Setenv("DATABASE_URL", env.URL)
	sample := abs(t, crmarenaSample)
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"import-crmarena", "--until", "2023-11-01T00:00:00Z", sample}, &out); err != nil {
		t.Fatalf("import: %v\n%s", err, out.String())
	}

	clk := clock.NewFixed(time.Now().UTC().Add(24 * time.Hour))
	hook, err := pipeline.New(pipeline.Options{RunMode: runs.DryRun, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	co, err := coalesce.New(env.DB, coalesce.Options{Hook: hook, Clock: clk, WorkerID: "replay"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := co.Drain(context.Background())
	if err != nil || res.Failed != 0 || len(res.Recomputes) == 0 {
		t.Fatalf("drain: %+v %v", res, err)
	}
	count := func(q string) int {
		var n int
		if err := env.DB.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	diffs := count(`SELECT count(*) FROM state_diffs`)
	evals := count(`SELECT count(*) FROM trigger_evaluations`)
	if diffs != len(res.Recomputes) || evals != diffs {
		t.Fatalf("one diff and one evaluation per recompute: %d recomputes, %d diffs, %d evaluations", len(res.Recomputes), diffs, evals)
	}
	t.Logf("CRMArena sample replay: %d recomputes, %d material diffs, %d signals, %d eligible evaluations, %d dry runs",
		len(res.Recomputes), count(`SELECT count(*) FROM state_diffs WHERE is_material`), count(`SELECT count(*) FROM signals`),
		count(`SELECT count(*) FROM trigger_evaluations WHERE eligible`), count(`SELECT count(*) FROM agent_runs`))
	if count(`SELECT count(*) FROM state_diffs WHERE is_material`) == 0 || count(`SELECT count(*) FROM signals`) == 0 {
		t.Fatal("the sample must produce material diffs and signals")
	}
	if n := count(`SELECT count(*) FROM trigger_evaluations WHERE NOT eligible AND cardinality(reason_codes) = 0`); n != 0 {
		t.Fatalf("%d ineligible evaluations without a reason", n)
	}
	if n := count(`SELECT count(*) FROM agent_runs WHERE run_mode <> 'dry_run'`); n != 0 {
		t.Fatalf("%d runs are not dry runs", n)
	}
}
