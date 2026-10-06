package learning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledgestore"
	"github.com/harneet2512/gtm-work/core-go/internal/retiredgold"
)

// EpisodeMetrics is the decision-episode leg of a backtest: how often the axis's gap already recurred
// in stored human corrections.
type EpisodeMetrics struct {
	Total      int      `json:"n_episodes"`
	Supporting int      `json:"n_supporting"`
	Explained  int      `json:"n_explained"`
	Corrected  int      `json:"n_corrected_verdicts"`
	EpisodeIDs []string `json:"supporting_episode_ids"`
	Executable bool     `json:"spec_executable"`
}

// GoldMetrics is the gold-set leg: how much of the WP16 gold asserts the axis, and the spec's
// agreement where it is world-agnostic enough to run.
type GoldMetrics struct {
	Cases      int      `json:"n_cases"`
	GoldFail   int      `json:"n_gold_fail"`
	Scored     int      `json:"n_scored"`
	Agree      int      `json:"n_agree"`
	FalsePass  int      `json:"n_false_pass"`
	FalseBlock int      `json:"n_false_block"`
	Agreement  *float64 `json:"agreement"`
	Dirs       []string `json:"dirs"`
	// Retired lists the gold directories that were skipped because they are retired (invented accounts).
	Retired []string `json:"retired_dirs,omitempty"`
}

// Metrics is the evaluator_version.metrics shape persisted on the row and in the run.
type Metrics struct {
	NCases         int      `json:"n_cases"`
	Agreement      *float64 `json:"agreement"`
	FalsePassRate  *float64 `json:"false_pass_rate"`
	FalseBlockRate *float64 `json:"false_block_rate"`
}

// BacktestReport is one persisted eval_backtest_runs row.
type BacktestReport struct {
	ID        string         `json:"id"`
	Evaluator string         `json:"evaluator"`
	Version   int            `json:"version"`
	Episodes  EpisodeMetrics `json:"episodes"`
	Gold      GoldMetrics    `json:"gold"`
	Metrics   Metrics        `json:"metrics"`
	Passed    bool           `json:"passed"`
	Reason    string         `json:"reason"`
	CreatedAt time.Time      `json:"created_at"`
}

// Gate are the thresholds a backtest applies. MinSupportingEpisodes counts unexplained deltas (plus
// corrected verdicts for the human_delta axis) on the evaluator's axis; MinGoldCases is the gold
// coverage the axis must have.
type Gate struct {
	MinSupportingEpisodes int
	MinGoldCases          int
}

// DefaultGate is the candidate->shadow gate: the gap must have recurred once in a stored correction
// (the seed delta counts) and the axis must be a dimension the gold set asserts.
var DefaultGate = Gate{MinSupportingEpisodes: 1, MinGoldCases: 1}

// candidateRow is the evaluator_versions row a backtest runs against.
type candidateRow struct {
	Evaluator, Status, Rubric string
	Kind                      string
	CreatedFrom               string
	SourceDeltaID             *string
	KnowledgeID               *string
	Spec                      Spec
}

func loadCandidate(ctx context.Context, tx *sql.Tx, evaluator string, version int) (candidateRow, error) {
	var c candidateRow
	var specRaw *string
	var src, kid *string
	err := tx.QueryRowContext(ctx, `SELECT evaluator::text, status, kind, rubric, created_from,
 source_human_delta_id::text, knowledge_id::text, shadow_spec::text
 FROM evaluator_versions WHERE evaluator = $1 AND version = $2 FOR UPDATE`, evaluator, version).
		Scan(&c.Evaluator, &c.Status, &c.Kind, &c.Rubric, &c.CreatedFrom, &src, &kid, &specRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("%w: %s:v%d", ErrNotFound, evaluator, version)
	}
	if err != nil {
		return c, fmt.Errorf("learning: load candidate %s:v%d: %w", evaluator, version, err)
	}
	c.SourceDeltaID, c.KnowledgeID = src, kid
	if specRaw != nil {
		spec, err := parseSpec([]byte(*specRaw))
		if err != nil {
			return c, err
		}
		c.Spec = spec
	}
	if c.Status != "candidate" && c.Status != "shadow" {
		return c, fmt.Errorf("%w: %s:v%d is %s", ErrNotCandidate, evaluator, version, c.Status)
	}
	return c, nil
}

// episodeMetrics counts, on the candidate's axis, the unexplained deltas (each is a stored decision
// episode where the same gap recurred) and, for the human_delta axis, the corrected verdicts. An episode the
// human declined learning from (no_learning) is neither support nor a correction. It also
// counts explained deltas whose explanations name the axis — the existing version already predicts
// those, so they are context, not support.
func episodeMetrics(ctx context.Context, tx *sql.Tx, axis string) (EpisodeMetrics, error) {
	m := EpisodeMetrics{}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM decision_episodes`).Scan(&m.Total); err != nil {
		return m, fmt.Errorf("learning: count episodes: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT hd.decision_episode_id::text FROM human_deltas hd
 WHERE hd.unexplained AND hd.candidate_criterion ->> 'suggested_eval_type' = $1
   AND NOT EXISTS (SELECT 1 FROM judgment_inferences ji
     WHERE ji.decision_episode_id = hd.decision_episode_id AND ji.human_verdict = 'no_learning')
 ORDER BY hd.created_at, hd.id`, axis)
	if err != nil {
		return m, fmt.Errorf("learning: supporting deltas on %s: %w", axis, err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return m, fmt.Errorf("learning: scan supporting delta: %w", err)
		}
		m.EpisodeIDs = append(m.EpisodeIDs, id)
	}
	if err := rows.Close(); err != nil {
		return m, err
	}
	m.Supporting = len(m.EpisodeIDs)
	if axis == "human_delta" {
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM judgment_verdicts jv
 JOIN judgment_inferences ji ON ji.id = jv.judgment_inference_id
 WHERE jv.verdict = 'corrected' AND ji.human_verdict <> 'no_learning'`).
			Scan(&m.Corrected); err != nil {
			return m, fmt.Errorf("learning: count corrected verdicts: %w", err)
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT hd.id) FROM human_deltas hd
 JOIN human_delta_explanations x ON x.human_delta_id = hd.id
 JOIN eval_runs er ON er.id = x.eval_run_id WHERE er.evaluator = $1`, axis).
		Scan(&m.Explained); err != nil {
		return m, fmt.Errorf("learning: explained deltas on %s: %w", axis, err)
	}
	return m, nil
}

// goldMetrics scores the axis's coverage in the gold set and, when the spec is world-agnostic, replays
// its checks on every axis-asserted candidate_action. A false pass is a case the gold fails on the
// axis the spec's checks hold; a false block is the mirror.
func goldMetrics(axis string, spec Spec, cases []goldCase, dirs []string) (GoldMetrics, error) {
	g := GoldMetrics{Dirs: dirs}
	agnostic := spec.Agnostic()
	for _, c := range cases {
		want := c.expectedVerdict(axis)
		if want == "" {
			continue
		}
		g.Cases++
		if want == "fail" {
			g.GoldFail++
		}
		if !agnostic {
			continue
		}
		d, err := c.candidate()
		if err != nil {
			return g, err
		}
		violated, _ := spec.RunSpec(d)
		g.Scored++
		if (want == "fail") == violated {
			g.Agree++
		} else if want == "fail" {
			g.FalsePass++
		} else {
			g.FalseBlock++
		}
	}
	if g.Scored > 0 {
		a := float64(g.Agree) / float64(g.Scored)
		g.Agreement = &a
	}
	return g, nil
}

// BacktestOpts configures one backtest run.
type BacktestOpts struct {
	Evaluator string
	Version   int
	GoldDirs  []string
	Rules     *knowledge.Rules // nil: no knowledge-evidence write
	Now       time.Time
	// Replay marks a backtest over a replay (HAR-144): Now, the replay clock, is required and the wall
	// clock default is refused. Live (false) defaults Now to the wall clock.
	Replay bool
	Gate   Gate
}

// Backtest evaluates a candidate or shadow evaluator version against the stored decision episodes and
// the gold sets, records the supporting episodes as knowledge evidence (the lifecycle gate the
// promotion reads), persists the eval_backtest_runs row and updates the version's metrics. It never
// changes the version's status — Advance does, gated by the persisted result.
func Backtest(ctx context.Context, db *sql.DB, o BacktestOpts) (BacktestReport, error) {
	gate := o.Gate
	if gate == (Gate{}) {
		gate = DefaultGate
	}
	at := o.Now
	if o.Replay && at.IsZero() {
		return BacktestReport{}, fmt.Errorf("%w: backtest of %s v%d", knowledgestore.ErrCreatedAtRequired, o.Evaluator, o.Version)
	}
	if at.IsZero() {
		at = time.Now()
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return BacktestReport{}, fmt.Errorf("learning: backtest begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	c, err := loadCandidate(ctx, tx, o.Evaluator, o.Version)
	if err != nil {
		return BacktestReport{}, err
	}
	ep, err := episodeMetrics(ctx, tx, c.Evaluator)
	if err != nil {
		return BacktestReport{}, err
	}
	ep.Executable = c.Spec.Executable()
	cases, err := loadGoldCases(o.GoldDirs)
	if err != nil {
		return BacktestReport{}, err
	}
	gold, err := goldMetrics(c.Evaluator, c.Spec, cases, o.GoldDirs)
	if err != nil {
		return BacktestReport{}, err
	}
	_, retired, err := retiredgold.Split(o.GoldDirs)
	if err != nil {
		return BacktestReport{}, fmt.Errorf("learning: %w", err)
	}
	for _, n := range retired {
		gold.Retired = append(gold.Retired, n.Dir)
	}
	if err := recordSupport(ctx, tx, c, ep, o.Rules, at); err != nil {
		return BacktestReport{}, err
	}
	rep := reportOf(c, o.Version, ep, gold, gate, at)
	if err := persistReport(ctx, tx, rep); err != nil {
		return BacktestReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return BacktestReport{}, fmt.Errorf("learning: backtest commit: %w", err)
	}
	return rep, nil
}

// recordSupport writes decision_episode evidence for every supporting episode the linked knowledge
// does not already carry. The lifecycle re-evaluates on each write; a candidate knowledge's status is
// what the promotion gate later reads.
func recordSupport(ctx context.Context, tx *sql.Tx, c candidateRow, ep EpisodeMetrics, rules *knowledge.Rules, at time.Time) error {
	if c.KnowledgeID == nil || rules == nil {
		return nil
	}
	k, err := knowledgestore.Get(ctx, tx, *c.KnowledgeID)
	if err != nil {
		return fmt.Errorf("learning: load candidate knowledge %s: %w", *c.KnowledgeID, err)
	}
	for _, epID := range ep.EpisodeIDs {
		if hasString(k.SupportingDecisionEpisodeIDs, epID) {
			continue
		}
		k, _, err = knowledgestore.RecordEvidence(ctx, tx, k.ID, knowledge.Evidence{
			Kind: knowledge.EvidenceDecisionEpisode, RefID: epID, At: at.UTC()}, *rules)
		if err != nil {
			return fmt.Errorf("learning: record support episode %s on %s: %w", epID, k.ID, err)
		}
	}
	return nil
}

// reportOf folds the two legs into the persisted report and the gate decision. The run's id derives
// from the version, the supporting episodes and the clock — a re-run of the same evidence is a new
// persisted run, not a deduplicated one.
func reportOf(c candidateRow, version int, ep EpisodeMetrics, gold GoldMetrics, gate Gate, at time.Time) BacktestReport {
	m := Metrics{NCases: gold.Cases, Agreement: gold.Agreement}
	if gold.Scored > 0 {
		fp := float64(gold.FalsePass) / float64(gold.Scored)
		fb := float64(gold.FalseBlock) / float64(gold.Scored)
		m.FalsePassRate, m.FalseBlockRate = &fp, &fb
	}
	rep := BacktestReport{
		ID: seedUUID("backtest", c.Evaluator, fmt.Sprint(version), at.UTC().Format(time.RFC3339Nano),
			fmt.Sprint(ep.Supporting), fmt.Sprint(ep.Corrected), fmt.Sprint(gold.Cases)),
		Evaluator: c.Evaluator, Version: version, Episodes: ep, Gold: gold, Metrics: m,
		CreatedAt: at.UTC(),
	}
	supporting := ep.Supporting
	if c.Evaluator == "human_delta" {
		supporting += ep.Corrected
	}
	// The gold leg only bounds axes judged on a candidate action: human_delta and trajectory are trace
	// axes no gold case can assert, so their evidence is the episode leg alone.
	goldNeeded := gate.MinGoldCases
	if k := KindOf(c.Evaluator); k == "human_delta" || k == "trace" {
		goldNeeded = 0
	}
	switch {
	case supporting < gate.MinSupportingEpisodes:
		rep.Reason = fmt.Sprintf("supporting corrections %d below the gate %d: the gap has not recurred enough",
			supporting, gate.MinSupportingEpisodes)
	case gold.Cases == 0 && goldNeeded > 0 && len(gold.Retired) > 0:
		rep.Reason = fmt.Sprintf("no gold: %s retired (invented accounts) and no live gold case asserts the axis %s",
			strings.Join(gold.Retired, ", "), c.Evaluator)
	case gold.Cases < goldNeeded:
		rep.Reason = fmt.Sprintf("gold coverage %d below the gate %d: the axis %s is not a labelled eval dimension",
			gold.Cases, goldNeeded, c.Evaluator)
	default:
		rep.Passed = true
		rep.Reason = fmt.Sprintf("%d supporting corrections and %d gold cases on %s clear the gate",
			supporting, gold.Cases, c.Evaluator)
	}
	return rep
}

// persistReport writes the eval_backtest_runs row and refreshes the version's stored metrics.
func persistReport(ctx context.Context, tx *sql.Tx, rep BacktestReport) error {
	ep, err := encodeJSON(rep.Episodes)
	if err != nil {
		return err
	}
	g, err := encodeJSON(rep.Gold)
	if err != nil {
		return err
	}
	m, err := encodeJSON(rep.Metrics)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO eval_backtest_runs (id, evaluator, version, episodes, gold, metrics, passed, reason, created_at)
 VALUES ($1::uuid, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9)`,
		rep.ID, rep.Evaluator, rep.Version, ep, g, m, rep.Passed, rep.Reason, rep.CreatedAt); err != nil {
		return fmt.Errorf("learning: persist backtest %s:v%d: %w", rep.Evaluator, rep.Version, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE evaluator_versions SET metrics = $3::jsonb
 WHERE evaluator = $1 AND version = $2`, rep.Evaluator, rep.Version, m); err != nil {
		return fmt.Errorf("learning: store metrics on %s:v%d: %w", rep.Evaluator, rep.Version, err)
	}
	return nil
}

func hasString(xs []string, x string) bool {
	for _, e := range xs {
		if e == x {
			return true
		}
	}
	return false
}
