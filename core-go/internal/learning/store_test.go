package learning_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxfixture"
	"github.com/harneet2512/gtm-work/core-go/internal/evals/deterministic"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/learning"
	"github.com/harneet2512/gtm-work/core-go/internal/store/storetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

var env *storetest.Env

func TestMain(m *testing.M) {
	os.Exit(storetest.Main(m, func(e *storetest.Env) { env = e }))
}

func ctx() context.Context { return context.Background() }

// seedEpisode seeds a fresh awaiting-choice episode on the sample world's first account.
func seedEpisode(t *testing.T) strategytest.Seeded {
	t.Helper()
	return strategytest.Seed(t, env.DB, ctxfixture.Get(t, env.DB).AccountA)
}

// insertDelta writes an unexplained human_deltas row (a delta and its explanations never coexist).
func insertDelta(t *testing.T, episodeID, axis string, changes string) string {
	t.Helper()
	crit, err := json.Marshal(map[string]any{"statement": "the human's correction", "suggested_eval_type": axis})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = env.DB.QueryRow(`INSERT INTO human_deltas (decision_episode_id, literal_changes, unexplained, candidate_criterion)
VALUES ($1::uuid, $2::jsonb, true, $3::jsonb) RETURNING id::text`, episodeID, changes, string(crit)).Scan(&id)
	if err != nil {
		t.Fatalf("insert delta of %s: %v", episodeID, err)
	}
	return id
}

// seedCriterion runs SeedDeltaCriterion in its own transaction (the caller asserts committed rows).
func seedCriterion(t *testing.T, episodeID, accountID, deltaID, axis, statement string, changes []learning.LiteralChange, at time.Time, rules *knowledge.Rules) learning.SeedResult {
	t.Helper()
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	sit, err := learning.EpisodeSituation(ctx(), tx, episodeID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := learning.SeedDeltaCriterion(ctx(), tx, learning.DeltaSeed{
		DeltaID: deltaID, EpisodeID: episodeID, AccountID: accountID,
		Criterion: learning.Criterion{Statement: statement, SuggestedEvalType: axis},
		Changes:   changes, Situation: sit, At: at}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

func scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v string
	if err := env.DB.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	return atoi(t, scalar(t, query, args...))
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		t.Fatalf("%q is not a number: %v", s, err)
	}
	return n
}

func testRules(t *testing.T) knowledge.Rules {
	t.Helper()
	r, err := knowledge.LoadRules(repoFile(t, "contracts/knowledge/lifecycle.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// repoFile finds a repository-root file by walking up from the test's working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found above %s", rel, dir)
		}
		dir = parent
	}
}

var goldDirs = []string{"testdata/gold"}

var fixedNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func TestSeedDeltaCriterionWritesCandidateKnowledgeAndVersion(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[{"kind":"cta_changed","after":"softer ask"}]`)
	wantVersion := atoi(t, scalar(t,
		`SELECT COALESCE(max(version), 1) + 1 FROM evaluator_versions WHERE evaluator = 'cta_calibration'`))

	rules := testRules(t)
	res := seedCriterion(t, seed.EpisodeID, seed.AccountID, deltaID, "cta_calibration",
		"The human softens the ask when the buyer asks for time.",
		[]learning.LiteralChange{{Kind: "cta_changed", After: "softer ask"}}, fixedNow, &rules)

	if res.Version != wantVersion || res.EvalVersion() != fmt.Sprintf("cta_calibration:v%d", wantVersion) {
		t.Fatalf("seeded version = %s, want the next free version", res.EvalVersion())
	}
	var status, kind, from, kid, src string
	var spec string
	err := env.DB.QueryRow(`SELECT status, kind, created_from, knowledge_id::text, source_human_delta_id::text, shadow_spec::text
FROM evaluator_versions WHERE evaluator = 'cta_calibration' AND version = $1`, res.Version).
		Scan(&status, &kind, &from, &kid, &src, &spec)
	if err != nil {
		t.Fatal(err)
	}
	if status != "candidate" || kind != "semantic" || from != "human_delta" || src != deltaID || kid != res.KnowledgeID {
		t.Fatalf("candidate row = %s %s %s src=%s kid=%s", status, kind, from, src, kid)
	}
	var decoded struct {
		AccountID string              `json:"account_id"`
		Changes   []map[string]string `json:"literal_changes"`
	}
	if err := json.Unmarshal([]byte(spec), &decoded); err != nil || decoded.AccountID != seed.AccountID ||
		len(decoded.Changes) != 1 || decoded.Changes[0]["kind"] != "cta_changed" {
		t.Fatalf("shadow_spec = %s (%v)", spec, err)
	}
	var kStatus, prov string
	var usedBy []byte
	err = env.DB.QueryRow(`SELECT status, provenance::text,
 to_jsonb(used_by_evaluators) FROM knowledge WHERE id = $1::uuid`,
		res.KnowledgeID).Scan(&kStatus, &prov, &usedBy)
	if err != nil {
		t.Fatal(err)
	}
	if kStatus != "candidate" {
		t.Fatalf("learned knowledge is %s, a candidate never jumps the lifecycle", kStatus)
	}
	var provenance struct {
		CreatedFrom             string  `json:"created_from"`
		SourceDecisionEpisodeID *string `json:"source_decision_episode_id"`
	}
	if err := json.Unmarshal([]byte(prov), &provenance); err != nil ||
		provenance.CreatedFrom != "human_delta" || provenance.SourceDecisionEpisodeID == nil ||
		*provenance.SourceDecisionEpisodeID != seed.EpisodeID {
		t.Fatalf("provenance = %s", prov)
	}
	var used []string
	if err := json.Unmarshal(usedBy, &used); err != nil || len(used) != 1 || used[0] != res.EvalVersion() {
		t.Fatalf("used_by_evaluators = %s", usedBy)
	}
	// The rules' evidence write at seed recorded the learned-from episode as decision_episode evidence.
	if n := count(t, `SELECT count(*) FROM knowledge_evidence
 WHERE knowledge_id = $1::uuid AND kind = 'decision_episode' AND ref_id = $2::uuid`,
		res.KnowledgeID, seed.EpisodeID); n != 1 {
		t.Fatalf("decision_episode evidence rows = %d, want the learned-from episode", n)
	}
	if n := count(t, `SELECT count(*) FROM knowledge_status_history
 WHERE knowledge_id = $1::uuid AND to_status = 'candidate'`, res.KnowledgeID); n < 1 {
		t.Fatal("the candidate status write is audited")
	}
}

func TestSeedDeltaCriterionRefuses(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "cta_calibration", `[{"kind":"cta_changed","after":"x"}]`)
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	sit, err := learning.EpisodeSituation(ctx(), tx, seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	base := learning.DeltaSeed{DeltaID: deltaID, EpisodeID: seed.EpisodeID, AccountID: seed.AccountID,
		Changes: []learning.LiteralChange{{Kind: "cta_changed", After: "x"}}, Situation: sit, At: fixedNow}
	bad := base
	bad.Criterion = learning.Criterion{Statement: "", SuggestedEvalType: "cta_calibration"}
	if _, err := learning.SeedDeltaCriterion(ctx(), tx, bad, nil); !errors.Is(err, learning.ErrNoCriterion) {
		t.Fatalf("empty statement err = %v, want ErrNoCriterion", err)
	}
	bad = base
	bad.Criterion = learning.Criterion{Statement: "s", SuggestedEvalType: "invented_axis"}
	if _, err := learning.SeedDeltaCriterion(ctx(), tx, bad, nil); !errors.Is(err, learning.ErrGate) {
		t.Fatalf("invented axis err = %v, want ErrGate", err)
	}
	if n := count(t, `SELECT count(*) FROM evaluator_versions WHERE source_human_delta_id = $1::uuid`, deltaID); n != 0 {
		t.Fatalf("a refused seed wrote %d versions", n)
	}
}

func TestSeedVerdictCriterionSeedsHumanDeltaCandidateIdempotently(t *testing.T) {
	seed := seedEpisode(t)
	rules := testRules(t)
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID,
		"The rep removed the second CTA because the buyer asked for a slower cadence.", fixedNow, false, &rules)
	if err != nil {
		t.Fatal(err)
	}
	if res.EvalType != "human_delta" || res.Version < 2 {
		t.Fatalf("verdict seed = %+v", res)
	}
	again, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID,
		"The rep removed the second CTA because the buyer asked for a slower cadence.", fixedNow, false, &rules)
	if err != nil {
		t.Fatal(err)
	}
	if again != res {
		t.Fatalf("an identical repeated correction reseeded: %+v vs %+v", again, res)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM evaluator_versions WHERE knowledge_id = $1::uuid`, res.KnowledgeID); n != 1 {
		t.Fatalf("the repeated correction wrote %d versions", n)
	}
	if got := scalar(t, `SELECT provenance ->> 'created_from' FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID); got != "manual" {
		t.Fatalf("verdict-seeded provenance = %s, want manual", got)
	}
	var spec string
	if err := env.DB.QueryRow(`SELECT shadow_spec::text FROM evaluator_versions
 WHERE evaluator = 'human_delta' AND version = $1`, res.Version).Scan(&spec); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Changes []any `json:"literal_changes"`
	}
	if err := json.Unmarshal([]byte(spec), &decoded); err != nil || len(decoded.Changes) != 0 {
		t.Fatalf("a verdict candidate carries no literal checks: %s", spec)
	}
}

func TestEpisodeSituationFallsBackToRelationshipState(t *testing.T) {
	seed := seedEpisode(t)
	// The fixture world never ran the transition detector, so set the projected relationship state the
	// situation falls back to before the episode's situation is read.
	if _, err := env.DB.Exec(`UPDATE account_state SET state = jsonb_set(state, '{relationship_state}',
 '{"value":"EXPANSION"}'::jsonb) WHERE account_id = $1::uuid`, seed.AccountID); err != nil {
		t.Fatal(err)
	}
	sit, err := learning.EpisodeSituation(ctx(), env.DB, seed.EpisodeID)
	if err != nil {
		t.Fatal(err)
	}
	if sit.TransitionStatus != "" {
		t.Fatalf("the seeded episode has no transition; got %+v", sit)
	}
	want := scalar(t, `SELECT state -> 'relationship_state' ->> 'value' FROM account_state WHERE account_id = $1::uuid`, seed.AccountID)
	if sit.Relationship != want {
		t.Fatalf("situation relationship = %q, want %q", sit.Relationship, want)
	}
	sig, _ := sit.Signature()
	if len(sig) != 1 || sig[0].Field != "relationship_state" || sig[0].Value != want {
		t.Fatalf("signature = %+v, want relationship_state eq %s", sig, want)
	}
}

func TestLoadAxesAndShadowResults(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "channel_appropriateness",
		`[{"kind":"channel_changed","after":"slack"}]`)
	res := seedCriterion(t, seed.EpisodeID, seed.AccountID, deltaID, "channel_appropriateness",
		"The human moves the reply to Slack when the buyer lives there.",
		[]learning.LiteralChange{{Kind: "channel_changed", After: "slack"}}, fixedNow, nil)

	// A candidate never shadow-runs: it must earn shadow through a backtest. Other tests may leave active
	// axes of the account behind, so the assertion keys on this version.
	mine := func(axes []learning.Axis) (out []learning.Axis) {
		for _, a := range axes {
			if a.Evaluator == res.EvalType && a.Version == res.Version {
				out = append(out, a)
			}
		}
		return out
	}
	if axes, err := learning.LoadAxes(ctx(), env.DB, seed.AccountID); err != nil || len(mine(axes)) != 0 {
		t.Fatalf("candidate axes = %+v %v", axes, err)
	}
	if _, err := env.DB.Exec(`UPDATE evaluator_versions SET status = 'shadow'
 WHERE evaluator = $1 AND version = $2`, res.EvalType, res.Version); err != nil {
		t.Fatal(err)
	}
	loaded, err := learning.LoadAxes(ctx(), env.DB, seed.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	axes := mine(loaded)
	if len(axes) != 1 || axes[0].Evaluator != "channel_appropriateness" || axes[0].Status != "shadow" {
		t.Fatalf("axes = %+v", loaded)
	}
	if other, err := learning.LoadAxes(ctx(), env.DB, strategytest.NewID()); err != nil || len(mine(other)) != 0 {
		t.Fatalf("a person-bound spec never shadows another account: %+v", other)
	}
	in := deterministic.Input{AgentRunID: strategytest.NewID(), DraftIndex: 1,
		EvaluatedAt: fixedNow,
		Draft:       deterministic.Output{FinishedArtifact: deterministic.Artifact{Channel: "email", Body: "b"}}}
	results := learning.ShadowResults(in, axes)
	if len(results) != 1 {
		t.Fatalf("shadow results = %+v", results)
	}
	r := results[0]
	if r.Verdict != "fail" || r.Blocking || string(r.EvalType) != "channel_appropriateness" ||
		r.EvalVersion != res.EvalVersion() || r.Kind != "human_delta" {
		t.Fatalf("shadow result = %+v", r)
	}
	in.Draft.FinishedArtifact.Channel = "slack"
	results = learning.ShadowResults(in, axes)
	if len(results) != 1 || results[0].Verdict != "pass" || results[0].Blocking {
		t.Fatalf("a corrected draft = %+v", results)
	}
	// Same input, same result id: replay produces the same row.
	in.Draft.FinishedArtifact.Channel = "email"
	if learning.ShadowResults(in, axes)[0].ID != r.ID {
		t.Fatal("a replayed shadow check must derive the same result id")
	}
	if versions, err := learning.ActiveVersions(ctx(), env.DB, seed.AccountID); err != nil || versions[res.EvalType] == res.Version {
		t.Fatalf("a shadow version is not active: %+v %v", versions, err)
	}
}

func TestActiveVersions(t *testing.T) {
	seed := seedEpisode(t)
	deltaID := insertDelta(t, seed.EpisodeID, "grounding",
		`[{"kind":"paragraph_added","after":"cited the security review"}]`)
	res := seedCriterion(t, seed.EpisodeID, seed.AccountID, deltaID, "grounding",
		"The human re-adds the omitted evidence citation.",
		[]learning.LiteralChange{{Kind: "paragraph_added", After: "cited the security review"}}, fixedNow, nil)
	if _, err := env.DB.Exec(`UPDATE evaluator_versions SET status = 'shadow' WHERE evaluator = $1 AND version = $2`,
		res.EvalType, res.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.Exec(`SELECT promote_evaluator_version($1, $2)`, res.EvalType, res.Version); err != nil {
		t.Fatal(err)
	}
	versions, err := learning.ActiveVersions(ctx(), env.DB, seed.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if versions["grounding"] != res.Version {
		t.Fatalf("active versions = %+v, want grounding:v%d", versions, res.Version)
	}
	// And no other account's runtime map carries it — the version bump is bound to where it was learned.
	if other, err := learning.ActiveVersions(ctx(), env.DB, strategytest.NewID()); err != nil || len(other) != 0 {
		t.Fatalf("a stranger account sees %+v %v — promotion leaked", other, err)
	}
	defer func() {
		_, _ = env.DB.Exec(`UPDATE evaluator_versions SET status = 'retired' WHERE evaluator = $1 AND version = $2`,
			res.EvalType, res.Version)
	}()
}

func contains(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}

// The note the human typed beside an edited interpretation is the scope they gave the proposal. A
// free-text scope cannot be an applicability condition (conditions name state fields, so a text one would
// never match), so it travels with the seeded candidate as its provenance note, where a reviewer reads it.
func TestSeedVerdictCriterionCarriesTheHumansScopeNote(t *testing.T) {
	seed := seedEpisode(t)
	inference := decidedWithInference(t, seed)
	if _, err := env.DB.Exec(`INSERT INTO judgment_notes (judgment_inference_id, note, surface, actor_label)
 VALUES ($1::uuid, 'Only for security-led buyers.', 'slack', 'alex')`, inference); err != nil {
		t.Fatal(err)
	}
	rules := testRules(t)
	tx, err := env.DB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := learning.SeedVerdictCriterion(ctx(), tx, seed.EpisodeID,
		"The rep leads with the security review for this buyer.", fixedNow, false, &rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	note := scalar(t, `SELECT provenance ->> 'note' FROM knowledge WHERE id = $1::uuid`, res.KnowledgeID)
	if !strings.Contains(note, "Only for security-led buyers.") {
		t.Fatalf("provenance note = %q, want the human's scope note", note)
	}
}
