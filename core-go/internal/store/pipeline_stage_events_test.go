package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// Migration 0032 (HAR-145): pipeline_stage_events keeps the honesty rules of the live Play pipeline in the
// database itself, so no writer can store a stage that contradicts the contract.

const stageInsert = `INSERT INTO pipeline_stage_events (run_id, account_id, stage, status, started_at, ended_at, eval_result_ids, failure_kind)
 VALUES ($RUN, $ACCOUNT, %s)`

func stageSQL(values string) string { return strings.Replace(stageInsert, "%s", values, 1) }

func TestPipelineStageEventRules(t *testing.T) {
	const id = `'0e1a0000-0000-4000-8000-000000000911'`
	cases := []struct{ name, sql, want string }{
		{"an unknown stage", stageSQL(`'deploy','running', now(), NULL, '{}', NULL`), checkViolation},
		{"an unknown status", stageSQL(`'ingest','done', now(), NULL, '{}', NULL`), checkViolation},
		{"a running stage has not ended", stageSQL(`'ingest','running', now(), now(), '{}', NULL`), checkViolation},
		{"a finished stage has ended", stageSQL(`'ingest','completed', now(), NULL, '{}', NULL`), checkViolation},
		{"a stage cannot end before it starts", stageSQL(`'ingest','completed', now(), now() - interval '1 second', '{}', NULL`), checkViolation},
		{"a failure kind explains only a failed or unknown stage", stageSQL(`'ingest','completed', now(), now(), '{}', 'transport'`), checkViolation},
		{"a failed stage says why", stageSQL(`'ingest','failed', now(), now(), '{}', NULL`), checkViolation},
		{"an unknown failure kind", stageSQL(`'ingest','failed', now(), now(), '{}', 'oops'`), checkViolation},
		{"evals fail only as a judgment: needs results", stageSQL(`'evals','failed', now(), now(), '{}', NULL`), checkViolation},
		{"a transport error while judging is never an eval FAIL", stageSQL(`'evals','failed', now(), now(), ARRAY[` + id + `]::uuid[], 'transport'`), checkViolation},
		{"an error while judging is unknown, not passed", stageSQL(`'evals','passed', now(), now(), '{}', 'transport'`), checkViolation},
		{"passed is an eval verdict: a non-eval stage is completed", stageSQL(`'ingest','passed', now(), now(), '{}', NULL`), checkViolation},
		{"warning is an eval verdict: a non-eval stage is completed", stageSQL(`'cliff','warning', now(), now(), '{}', NULL`), checkViolation},
		{"the judging stage renders verdicts, it is never merely completed", stageSQL(`'evals','completed', now(), now(), '{}', NULL`), checkViolation},
		{"a stage needs an owner", `INSERT INTO pipeline_stage_events (account_id, stage, status, started_at) VALUES ($ACCOUNT, 'ingest', 'running', now())`, checkViolation},
		{"a stage runs once per run", stageSQL(`'ingest','running', now(), NULL, '{}', NULL`) + `; ` + stageSQL(`'ingest','running', now(), NULL, '{}', NULL`), uniqueViolation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				runWorld(t, tx, ids, strategyWorld(3))
				stmts := strings.Split(tc.sql, "; ")
				for _, s := range stmts[:len(stmts)-1] {
					if _, err := tx.Exec(ids.expand(s)); err != nil {
						t.Fatalf("setup: %v\n%s", err, s)
					}
				}
				assertSQLState(t, execExpectingError(tx, ids.expand(stmts[len(stmts)-1])), tc.want)
			})
		})
	}
}

func TestPipelineStageEventLegalShapes(t *testing.T) {
	const id = `'0e1a0000-0000-4000-8000-000000000911'`
	for name, values := range map[string]string{
		"a running stage":                      `'decide','running', now(), NULL, '{}', NULL`,
		"a completed stage":                    `'ingest','completed', now(), now(), '{}', NULL`,
		"a graph timeout":                      `'graph','failed', now(), now(), '{}', 'transport'`,
		"a transport error while judging":      `'evals','unknown', now(), now(), '{}', 'transport'`,
		"a judged failure names its results":   `'evals','failed', now(), now(), ARRAY[` + id + `]::uuid[], NULL`,
		"a skipped stage":                      `'cliff','skipped', now(), now(), '{}', NULL`,
		"a warning with results":               `'evals','warning', now(), now(), ARRAY[` + id + `]::uuid[], NULL`,
		"an unknown stage that never finished": `'cliff','unknown', now(), now(), '{}', NULL`,
	} {
		t.Run(name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				runWorld(t, tx, ids, strategyWorld(3))
				runWorld(t, tx, ids, []string{stageSQL(values)})
			})
		})
	}
}

func TestSendEvalResultsLinkADecisionToItsBatch(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{chooseSQL(cand129(2))})
		if _, err := tx.Exec(`INSERT INTO eval_runs (id, agent_run_id, draft_index, evaluator, evaluator_version, kind, verdict, blocking, evidence_class)
 VALUES ('0e1a0000-0000-4000-8000-000000000a11', $1::uuid, 2, 'recipient_correctness', 'recipient_correctness:v1', 'deterministic', 'pass', false, 'product_rule')`,
			ids["RUN"]); err != nil {
			t.Fatalf("eval_runs: %v", err)
		}
		if _, err := tx.Exec(`INSERT INTO send_eval_results (human_strategy_decision_id, eval_run_id, state_version, state_hash)
 SELECT id, '0e1a0000-0000-4000-8000-000000000a11', 3, repeat('a', 64) FROM human_strategy_decisions`); err != nil {
			t.Fatalf("link: %v", err)
		}
		assertSQLState(t, execExpectingError(tx, `INSERT INTO send_eval_results (human_strategy_decision_id, eval_run_id, state_version, state_hash)
 SELECT id, '0e1a0000-0000-4000-8000-000000000a11', 3, repeat('a', 64) FROM human_strategy_decisions`), uniqueViolation)
		// the state the send-time evaluation read is proven by a version and a sha-256, never optional
		assertSQLState(t, execExpectingError(tx, `INSERT INTO send_eval_results (human_strategy_decision_id, eval_run_id, state_version, state_hash)
 SELECT id, '0e1a0000-0000-4000-8000-000000000a12', 3, 'not-a-hash' FROM human_strategy_decisions`), checkViolation)
	})
}

// seq is a change counter (HAR-145 review): every update takes a new, larger value, so a poller that remembers the
// largest seq it saw detects any change of any stage.
func TestPipelineStageEventSeqChangesOnEveryUpdate(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		runWorld(t, tx, ids, strategyWorld(3))
		runWorld(t, tx, ids, []string{stageSQL(`'ingest','running', now(), NULL, '{}', NULL`)})
		var first, second, third int64
		get := func(dst *int64) {
			if err := tx.QueryRow(`SELECT seq FROM pipeline_stage_events WHERE stage = 'ingest'`).Scan(dst); err != nil {
				t.Fatal(err)
			}
		}
		get(&first)
		if _, err := tx.Exec(`UPDATE pipeline_stage_events SET detail = 'working' WHERE stage = 'ingest'`); err != nil {
			t.Fatal(err)
		}
		get(&second)
		if _, err := tx.Exec(`UPDATE pipeline_stage_events SET status = 'completed', ended_at = now() WHERE stage = 'ingest'`); err != nil {
			t.Fatal(err)
		}
		get(&third)
		if !(first < second && second < third) {
			t.Errorf("seq = %d, %d, %d: want strictly increasing across updates", first, second, third)
		}
	})
}
