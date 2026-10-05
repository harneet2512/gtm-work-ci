package evalreport_test

// Shared harness of the evalreport tests: the embedded-Postgres env (storetest), a thin fixture over
// strategystore's choose/send flow so episodes, decisions, deltas and eval rows are written by the real
// code paths, and small helpers for the two things production writers own but a report test must stub —
// generation-time eval_runs rows (the orchestrator would write them) and timestamps.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

// episode is one seeded awaiting-choice episode plus the strategystore service that decides it.
type episode struct {
	t    *testing.T
	svc  *strategystore.Service
	seed strategytest.Seeded
	ctx  context.Context
}

// newEpisode seeds one awaiting-choice episode on the sample world's first account.
func newEpisode(t *testing.T) *episode {
	t.Helper()
	world := ctxfixture.Get(t, env.DB)
	svc, err := strategystore.New(env.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &episode{t: t, svc: svc, seed: strategytest.Seed(t, env.DB, world.AccountA), ctx: context.Background()}
}

// choose records the candidate choice; edit (optional) sets final_* fields.
func (e *episode) choose(candidate string, edit func(*strategystore.DecisionRequest)) {
	e.t.Helper()
	req := strategystore.DecisionRequest{SelectedCandidateID: candidate, Surface: "api", ActorLabel: "alex"}
	if edit != nil {
		edit(&req)
	}
	if _, _, err := e.svc.RecordDecision(e.ctx, e.seed.RunID, req); err != nil {
		e.t.Fatalf("choose: %v", err)
	}
}

// send completes the episode; decision is "send" or "discard".
func (e *episode) send(decision string) {
	e.t.Helper()
	if _, err := e.svc.Send(e.ctx, e.seed.RunID,
		strategystore.SendRequest{Decision: decision, Surface: "api", ActorLabel: "alex"}); err != nil {
		e.t.Fatalf("send %s: %v", decision, err)
	}
}

// artifact reads the chosen candidate's stored artifact (the pre-edit draft).
func (e *episode) artifact(candidateID string) strategystore.Artifact {
	e.t.Helper()
	var raw string
	if err := env.DB.QueryRow(`SELECT full_action_artifact::text FROM strategy_candidates WHERE id = $1::uuid`,
		candidateID).Scan(&raw); err != nil {
		e.t.Fatalf("candidate artifact: %v", err)
	}
	var a strategystore.Artifact
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		e.t.Fatalf("decode artifact: %v", err)
	}
	return a
}

// deltaID resolves the episode's human_delta id.
func (e *episode) deltaID() string {
	e.t.Helper()
	var id string
	if err := env.DB.QueryRow(`SELECT human_delta_id::text FROM decision_episodes WHERE id = $1::uuid`,
		e.seed.EpisodeID).Scan(&id); err != nil {
		e.t.Fatalf("delta of episode %s: %v", e.seed.EpisodeID, err)
	}
	return id
}

// setLabels overwrites the delta's semantic_labels (a test stands in for the labeler, whose live
// call this repository never makes inside tests).
func (e *episode) setLabels(labels ...string) {
	e.t.Helper()
	quoted := make([]string, len(labels))
	for i, l := range labels {
		quoted[i] = `"` + l + `"`
	}
	if _, err := env.DB.Exec(`UPDATE human_deltas SET semantic_labels = $2::text[] WHERE decision_episode_id = $1::uuid`,
		e.seed.EpisodeID, "{"+strings.Join(quoted, ",")+"}"); err != nil {
		e.t.Fatalf("label delta: %v", err)
	}
}

// setDecidedAt moves the episode's human decision to a fixed wall-clock (the report's before/after
// activation split reads human_decisions.created_at — eval_runs.created_at is the replay clock).
func (e *episode) setDecidedAt(at time.Time) {
	e.t.Helper()
	if _, err := env.DB.Exec(`UPDATE human_decisions SET created_at = $2 WHERE agent_run_id = $1::uuid`,
		e.seed.RunID, at); err != nil {
		e.t.Fatalf("set decided_at: %v", err)
	}
}

// insertEval writes a generation-time eval_runs row — what the orchestrator's publish path would have
// persisted for the draft bundle (conf may be nil; at is the replay-clock created_at).
func insertEval(t *testing.T, runID string, draft int, evaluator, tag, kind, verdict string,
	conf *float64, at time.Time) string {
	t.Helper()
	id := strategytest.NewID()
	if _, err := env.DB.Exec(`INSERT INTO eval_runs
 (id, agent_run_id, draft_index, evaluator, evaluator_version, kind, verdict, confidence, evidence_class, created_at)
 VALUES ($1::uuid, $2::uuid, $3, $4::eval_type, $5, $6, $7, $8, 'product_rule', $9)`,
		id, runID, draft, evaluator, tag, kind, verdict, conf, at); err != nil {
		t.Fatalf("insert eval %s on %s: %v", tag, runID, err)
	}
	return id
}

// insertVersion writes an evaluator_versions row (spec may be empty -> NULL shadow_spec).
func insertVersion(t *testing.T, evaluator string, version int, status, kind, createdFrom, spec string,
	promotedAt *time.Time, accountID string) {
	t.Helper()
	var specArg any
	if spec != "" {
		specArg = spec
	}
	var acct any
	if accountID != "" {
		acct = accountID
	}
	if _, err := env.DB.Exec(`INSERT INTO evaluator_versions
 (evaluator, version, status, kind, rubric, created_from, account_id, promoted_at, shadow_spec)
 VALUES ($1::eval_type, $2, $3, $4, 'test rubric for the report fixture', $5, $6::uuid, $7, $8::jsonb)`,
		evaluator, version, status, kind, createdFrom, acct, promotedAt, specArg); err != nil {
		t.Fatalf("insert version %s:v%d: %v", evaluator, version, err)
	}
}

// generate runs the report and fails on error.
func generate(t *testing.T, opts evalreport.Options) evalreport.ReportSet {
	t.Helper()
	set, err := evalreport.Generate(context.Background(), env.DB, opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return set
}

// reportOf fetches one version's report, failing if absent.
func reportOf(t *testing.T, set evalreport.ReportSet, tag string) *evalreport.Report {
	t.Helper()
	r, ok := set.Reports[tag]
	if !ok {
		t.Fatalf("no report for %s (reports: %v)", tag, keysOf(set))
	}
	return r
}

func keysOf(set evalreport.ReportSet) []string {
	keys := make([]string, 0, len(set.Reports))
	for k := range set.Reports {
		keys = append(keys, k)
	}
	return keys
}

// metric asserts the ok/n/a contract: ok needs a value, n/a needs a reason.
func metricOK(t *testing.T, name, status, reason string, hasValue bool) {
	t.Helper()
	switch status {
	case "ok":
		if !hasValue {
			t.Fatalf("metric %s is ok but carries no value", name)
		}
	case "n/a":
		if reason == "" {
			t.Fatalf("metric %s is n/a without a reason", name)
		}
	default:
		t.Fatalf("metric %s has status %q", name, status)
	}
}

func closeTo(got, want float64) bool {
	d := got - want
	return d < 0.0001 && d > -0.0001
}

func ptr(f float64) *float64 { return &f }
