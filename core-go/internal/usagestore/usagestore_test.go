package usagestore_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/usagestore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func scalar(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s string
	if err := env.DB.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// newRun makes an account with a state and an activity and one run on it.
func newRun(t *testing.T, name string) string {
	t.Helper()
	acct := scalar(t, `INSERT INTO accounts (name) VALUES ($1) RETURNING id::text`, name)
	state := `{"account_id":"` + acct + `","version":1}`
	for _, q := range []string{
		`INSERT INTO state_history (account_id, version, as_of, state) VALUES ($1::uuid, 1, now(), $2::jsonb)`,
		`INSERT INTO account_state (account_id, version, as_of, state) VALUES ($1::uuid, 1, now(), $2::jsonb)`,
	} {
		if _, err := env.DB.Exec(q, acct, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.DB.Exec(`WITH se AS (INSERT INTO source_events (source_system, source_object_id, source_event_key, idempotency_key, payload, occurred_at)
 VALUES ('email', $2, 'k', encode(sha256(convert_to($2::text, 'UTF8')), 'hex'), '{}'::jsonb, now()) RETURNING id)
 INSERT INTO activities (source_event_id, activity_type, source_system, source_object_id, occurred_at, account_id, provenance)
 SELECT id, 'EmailReceived', 'email', $2, now(), $1::uuid, '{"source_system":"email"}'::jsonb FROM se`, acct, "obj-"+name); err != nil {
		t.Fatal(err)
	}
	run, _, err := ctxfixture.InsertRun(context.Background(), env.DB, acct, "context_built")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func store(t *testing.T) *usagestore.Store {
	t.Helper()
	s, err := usagestore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func cost(v float64) *float64 { return &v }

func n(v int64) *int64 { return &v }

func record(stage string, u workerclient.Usage, wall int64) workerclient.UsageRecord {
	return workerclient.UsageRecord{Stage: stage, Usage: u, WallMs: wall}
}

var strategiesUsage = workerclient.Usage{ModelCalls: 3, InputTokens: 12000, OutputTokens: 1500, CachedInputTokens: n(6000), ReasoningTokens: n(400),
	ToolCalls: 3, Retries: 1, ModelMs: 13000, CostUSD: cost(0.008), Models: []string{"deepseek-v4-flash"}, UsageSource: "live"}

func TestFlushStoresEveryCollectedCallWithItsStepAndStage(t *testing.T) {
	run := newRun(t, "Usage Flush")
	col := &usagestore.Collector{}
	col.RecordUsage(record("strategies", strategiesUsage, 14000))
	col.RecordUsage(record("judge", workerclient.Usage{ModelCalls: 2, InputTokens: 100, OutputTokens: 10, Models: []string{}, UsageSource: "live"}, 60))

	if err := store(t).Flush(context.Background(), run, "draft", col); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid AND run_step = 'draft'`, run); n != "2" {
		t.Fatalf("stored %s rows", n)
	}
	row := scalar(t, `SELECT concat_ws('|', model_calls, input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, tool_calls, retries, cost_usd, model_ms, wall_ms, array_to_string(models, ','))
 FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'strategies'`, run)
	if row != "3|12000|1500|6000|400|3|1|0.008000|13000|14000|deepseek-v4-flash" {
		t.Fatalf("stored = %s", row)
	}
	if c := scalar(t, `SELECT coalesce(cached_input_tokens::text, 'NULL') || '/' || coalesce(reasoning_tokens::text, 'NULL') FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'judge'`, run); c != "NULL/NULL" {
		t.Fatalf("unreported cached and reasoning tokens stay NULL, not 0: %s", c)
	}
	if c := scalar(t, `SELECT coalesce(cost_usd::text, 'NULL') FROM run_model_usage WHERE agent_run_id = $1::uuid AND stage = 'judge'`, run); c != "NULL" {
		t.Fatalf("an unreported cost stays NULL, got %s", c)
	}
}

func TestAReplayedCallIsStoredAsSpendingNothingWhateverTheWorkerSent(t *testing.T) {
	run := newRun(t, "Usage Replay")
	col := &usagestore.Collector{}
	// A worker (or a bug) that reports spend for replayed answers: core never stores it.
	col.RecordUsage(record("judge", workerclient.Usage{ModelCalls: 2, InputTokens: 500, OutputTokens: 50, CachedInputTokens: n(10),
		ReasoningTokens: n(5), CostUSD: cost(0.9), ToolCalls: 1, Models: []string{"m"}, UsageSource: "replay"}, 70))
	if err := store(t).Flush(context.Background(), run, "draft", col); err != nil {
		t.Fatal(err)
	}
	got := scalar(t, `SELECT concat_ws('|', usage_source, model_calls, input_tokens, output_tokens, coalesce(cached_input_tokens::text, 'NULL'),
 coalesce(cost_usd::text, 'NULL'), wall_ms) FROM run_model_usage WHERE agent_run_id = $1::uuid`, run)
	if got != "replay|0|0|0|NULL|NULL|70" {
		t.Fatalf("stored %s", got)
	}
}

func TestARecordWithoutAUsageSourceIsRefused(t *testing.T) {
	col := &usagestore.Collector{}
	col.RecordUsage(record("judge", workerclient.Usage{ModelCalls: 1, InputTokens: 1, OutputTokens: 1}, 1))
	if err := store(t).Flush(context.Background(), newRun(t, "Usage No Source"), "draft", col); err == nil {
		t.Fatal("usage that does not say whether it was live or replayed was stored")
	}
}

func TestOneBadRecordDoesNotCostTheOthersTheirRows(t *testing.T) {
	run := newRun(t, "Usage Partial")
	col := &usagestore.Collector{}
	col.RecordUsage(record("strategies", strategiesUsage, 1))
	col.RecordUsage(record("extract", strategiesUsage, 1)) // an unknown stage
	col.RecordUsage(record("judge", strategiesUsage, 1))
	err := store(t).Flush(context.Background(), run, "draft", col)
	if err == nil {
		t.Fatal("the bad record was not reported")
	}
	if got := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid`, run); got != "2" {
		t.Fatalf("stored %s rows, want the 2 good ones", got)
	}
}

func TestFlushClearsTheCollectorSoACallIsNeverStoredTwice(t *testing.T) {
	run := newRun(t, "Usage Twice")
	col := &usagestore.Collector{}
	col.RecordUsage(record("revise", strategiesUsage, 5))
	s := store(t)
	for i := 0; i < 2; i++ {
		if err := s.Flush(context.Background(), run, "draft", col); err != nil {
			t.Fatal(err)
		}
	}
	if n := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid`, run); n != "1" {
		t.Fatalf("stored %s rows, want 1", n)
	}
	if len(col.Records()) != 0 {
		t.Fatal("a flushed collector is empty")
	}
}

func TestFlushOfNothingIsANoop(t *testing.T) {
	if err := store(t).Flush(context.Background(), newRun(t, "Usage Empty"), "draft", &usagestore.Collector{}); err != nil {
		t.Fatal(err)
	}
}

func TestInconsistentWorkerFiguresAreClampedNotRefused(t *testing.T) {
	run := newRun(t, "Usage Clamp")
	col := &usagestore.Collector{}
	// More cached than input, more reasoning than output: the worker clamps these too, but core never loses the row over it.
	col.RecordUsage(record("judge", workerclient.Usage{ModelCalls: 1, InputTokens: 10, OutputTokens: 4, CachedInputTokens: n(99), ReasoningTokens: n(99), Models: []string{"m"}, UsageSource: "live"}, 1))
	// No model call, so no tokens or cost may be stored (a failed-then-retried request that spent only time).
	col.RecordUsage(record("judge", workerclient.Usage{ModelCalls: 0, InputTokens: 7, OutputTokens: 7, CostUSD: cost(0.5), Retries: 2, UsageSource: "live"}, 9))
	if err := store(t).Flush(context.Background(), run, "draft", col); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, `SELECT cached_input_tokens || '/' || reasoning_tokens FROM run_model_usage WHERE agent_run_id = $1::uuid AND model_calls = 1`, run); got != "10/4" {
		t.Fatalf("clamped to %s", got)
	}
	if got := scalar(t, `SELECT input_tokens || '/' || output_tokens || '/' || coalesce(cost_usd::text, 'NULL') || '/' || retries FROM run_model_usage WHERE agent_run_id = $1::uuid AND model_calls = 0`, run); got != "0/0/NULL/2" {
		t.Fatalf("a call-less record kept %s", got)
	}
}

func TestAnUnknownStageOrRunIsAnErrorTheCallerCanLog(t *testing.T) {
	col := &usagestore.Collector{}
	col.RecordUsage(record("extract", strategiesUsage, 1))
	if err := store(t).Flush(context.Background(), newRun(t, "Usage Bad Stage"), "draft", col); err == nil {
		t.Fatal("an unknown stage was stored")
	}
	col = &usagestore.Collector{}
	col.RecordUsage(record("judge", strategiesUsage, 1))
	if err := store(t).Flush(context.Background(), "99999999-9999-4999-8999-999999999999", "draft", col); err == nil {
		t.Fatal("usage of an unknown run was stored")
	}
	col = &usagestore.Collector{}
	col.RecordUsage(record("judge", strategiesUsage, 1))
	if err := store(t).Flush(context.Background(), "not-a-uuid", "draft", col); err == nil {
		t.Fatal("a malformed run id was accepted")
	}
}

func TestAFlushSurvivesACancelledCallerContext(t *testing.T) {
	run := newRun(t, "Usage Cancelled")
	col := &usagestore.Collector{}
	col.RecordUsage(record("strategies", strategiesUsage, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the run failed and its context is gone: the money was still spent
	if err := store(t).Flush(ctx, run, "draft", col); err != nil {
		t.Fatalf("flush with a cancelled context: %v", err)
	}
	if n := scalar(t, `SELECT count(*)::text FROM run_model_usage WHERE agent_run_id = $1::uuid`, run); n != "1" {
		t.Fatalf("stored %s rows", n)
	}
}

func TestCollectorIsSafeForConcurrentUse(t *testing.T) {
	col := &usagestore.Collector{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			col.RecordUsage(record("judge", strategiesUsage, 1))
		}()
	}
	wg.Wait()
	if n := len(col.Records()); n != 20 {
		t.Fatalf("collected %d records", n)
	}
	recs := col.Records()
	recs[0].Stage = "tampered"
	if col.Records()[0].Stage == "tampered" {
		t.Fatal("Records must hand out a copy")
	}
}

func TestNewRequiresADatabase(t *testing.T) {
	if _, err := usagestore.New(nil, nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
