package store_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
)

// HAR-145 operational metrics: migration 0034 (run_model_usage) and its JSON-to-SQL parity with
// contracts/schemas/operational_metrics.v1.json and worker_usage.v1.json.

const usageInsert = `INSERT INTO run_model_usage (agent_run_id, run_step, stage, models, model_calls, input_tokens, output_tokens, cached_input_tokens,
 reasoning_tokens, tool_calls, retries, cost_usd, model_ms, wall_ms, usage_source) VALUES `

var runModelUsageCases = []constraintCase{
	{"an unknown stage is rejected",
		usageInsert + `($RUN,'draft','extract','{}',1,1,1,0,0,0,0,NULL,1,1,'live')`, checkViolation},
	{"an unknown run step is rejected",
		usageInsert + `($RUN,'dinner','strategies','{}',1,1,1,0,0,0,0,NULL,1,1,'live')`, checkViolation},
	{"a call belongs to an existing run",
		usageInsert + `('99999999-9999-4999-8999-999999999999','draft','strategies','{}',1,1,1,0,0,0,0,NULL,1,1,'live')`, foreignKeyViolation},
	{"tokens are never negative",
		usageInsert + `($RUN,'draft','strategies','{}',1,-1,1,0,0,0,0,NULL,1,1,'live')`, checkViolation},
	{"cost is never negative",
		usageInsert + `($RUN,'draft','strategies','{}',1,1,1,0,0,0,0,-0.01,1,1,'live')`, checkViolation},
	{"latency is never negative",
		usageInsert + `($RUN,'draft','strategies','{}',1,1,1,0,0,0,0,NULL,-1,1,'live')`, checkViolation},
	{"cached tokens are part of the input, never more",
		usageInsert + `($RUN,'draft','strategies','{}',1,10,1,11,0,0,0,NULL,1,1,'live')`, checkViolation},
	{"reasoning tokens are part of the output, never more",
		usageInsert + `($RUN,'draft','strategies','{}',1,10,5,0,6,0,0,NULL,1,1,'live')`, checkViolation},
	{"no model call, no tokens",
		usageInsert + `($RUN,'draft','strategies','{}',0,10,0,0,0,0,0,NULL,1,1,'live')`, checkViolation},
	{"no model call, no cost",
		usageInsert + `($RUN,'draft','strategies','{}',0,0,0,0,0,0,0,0.5,1,1,'live')`, checkViolation},
	{"usage_source is required",
		`INSERT INTO run_model_usage (agent_run_id, run_step, stage, models, model_calls, input_tokens, output_tokens, tool_calls, retries, model_ms, wall_ms)
 VALUES ($RUN,'draft','strategies','{}',1,1,1,0,0,1,1)`, notNullViolation},
	{"usage_source is live or replay",
		usageInsert + `($RUN,'draft','strategies','{}',1,1,1,0,0,0,0,NULL,1,1,'estimated')`, checkViolation},
	{"a replayed call spent no tokens",
		usageInsert + `($RUN,'draft','strategies','{}',0,5,0,NULL,NULL,0,0,NULL,1,1,'replay')`, checkViolation},
	{"a replayed call has no cost",
		usageInsert + `($RUN,'draft','strategies','{}',0,0,0,NULL,NULL,0,0,0.5,1,1,'replay')`, checkViolation},
	{"a replayed call made no model call",
		usageInsert + `($RUN,'draft','strategies','{}',1,0,0,NULL,NULL,0,0,NULL,1,1,'replay')`, checkViolation},
	{"a negative cached count is rejected even though NULL is allowed",
		usageInsert + `($RUN,'draft','strategies','{}',1,10,1,-1,NULL,0,0,NULL,1,1,'live')`, checkViolation},
	{"a run with usage cannot be deleted away",
		usageInsert + `($RUN,'draft','strategies','{}',1,1,1,0,0,0,0,NULL,1,1,'live'); DELETE FROM agent_runs WHERE id = $RUN`, foreignKeyViolation},
}

func TestRunModelUsageConstraints(t *testing.T) {
	for _, tc := range runModelUsageCases {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Tx(t, env.DB, func(tx *sql.Tx) {
				ids := seedGraph(t, tx)
				assertSQLState(t, execExpectingError(tx, ids.expand(tc.sql)), tc.wantCode)
			})
		})
	}
}

func TestRunModelUsageAcceptsWhatTheWorkerReports(t *testing.T) {
	storetest.Tx(t, env.DB, func(tx *sql.Tx) {
		ids := seedGraph(t, tx)
		for _, s := range []string{
			usageInsert + `($RUN,'draft','strategies','{deepseek-v4-flash}',3,12000,1500,6000,400,3,1,0.008,13000,14000,'live')`,
			usageInsert + `($RUN,'draft','judge','{a,b}',2,100,10,100,10,0,0,NULL,50,60,'live')`,
			usageInsert + `($RUN,NULL,'human_delta','{}',0,0,0,0,0,0,0,NULL,0,5,'live')`,
			usageInsert + `($RUN,'draft','revise','{m}',1,10,2,NULL,NULL,0,0,NULL,3,4,'live')`, // cached and reasoning not reported
			usageInsert + `($RUN,'draft','judge','{}',0,0,0,NULL,NULL,0,0,NULL,0,4,'replay')`,
			usageInsert + `($RUN,'await_human','human_delta','{x}',1,1,1,0,0,0,0,0,1,1,'live')`,
		} {
			if _, err := tx.Exec(ids.expand(s)); err != nil {
				t.Fatalf("a legal usage row was refused: %v\n%s", err, s)
			}
		}
	})
}

// TestUsageStageVocabularyMatchesTheContract is the JSON-to-SQL parity: the stages the operational_metrics schema
// allows are exactly the stages the table accepts, and the worker operations are the same set.
func TestUsageStageVocabularyMatchesTheContract(t *testing.T) {
	root := repoRoot(t)
	var metrics struct {
		Defs struct {
			Stage struct {
				Properties struct {
					Stage struct {
						Enum []string `json:"enum"`
					} `json:"stage"`
				} `json:"properties"`
			} `json:"stage"`
		} `json:"$defs"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "schemas", "operational_metrics.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &metrics); err != nil {
		t.Fatal(err)
	}
	want := metrics.Defs.Stage.Properties.Stage.Enum
	if len(want) == 0 {
		t.Fatal("no stage enum in operational_metrics.v1.json")
	}
	sqlRaw, err := os.ReadFile(filepath.Join(root, "core-go", "internal", "store", "migrations", "0034_run_model_usage.sql"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`stage\s+text NOT NULL CHECK \(stage IN \(([^)]*)\)\)`).FindSubmatch(sqlRaw)
	if m == nil {
		t.Fatal("no stage CHECK list in migration 0034")
	}
	var got []string
	for _, q := range regexp.MustCompile(`'([a-z_]+)'`).FindAllSubmatch(m[1], -1) {
		got = append(got, string(q[1]))
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("SQL stages %v != contract stages %v", got, want)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "schemas")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}
