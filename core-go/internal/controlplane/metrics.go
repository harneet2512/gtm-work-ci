package controlplane

import (
	"context"
	"fmt"
	"slices"

	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
)

// Latency is operational_metrics.v1.json latency: the summed duration core measured for each live worker call and the
// summed time the worker spent inside the provider layer. Neither is the run's elapsed time: concurrent calls add up
// and the time between calls is in neither, so the field says "worker call", not "wall".
type Latency struct {
	WorkerCallMs int64 `json:"worker_call_ms"`
	ModelMs      int64 `json:"model_ms"`
}

// StageMetrics is one worker operation's share of the metrics (a latency stage).
type StageMetrics struct {
	Stage             string   `json:"stage"`
	WorkerCalls       int      `json:"worker_calls"`
	ModelCalls        int      `json:"model_calls"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	CachedInputTokens *int64   `json:"cached_input_tokens"`
	ReasoningTokens   *int64   `json:"reasoning_tokens"`
	ToolCalls         int      `json:"tool_calls"`
	Retries           int      `json:"retries"`
	CostUSD           *float64 `json:"cost_usd"`
	WorkerCallMs      int64    `json:"worker_call_ms"`
	ModelMs           int64    `json:"model_ms"`
}

// OperationalMetrics is operational_metrics.v1.json: what one run cost, summed from the usage the worker reported per
// call. A METRIC (HAR-97 M1-M5), never an eval: Classification is always "metric" and nothing here has a verdict.
type OperationalMetrics struct {
	Classification    string         `json:"classification"`
	AgentRunID        string         `json:"agent_run_id"`
	DecisionEpisodeID *string        `json:"decision_episode_id"`
	Measured          bool           `json:"measured"`     // true only when live usage was recorded
	UsageSource       *string        `json:"usage_source"` // live | replay | mixed; nil when no usage was recorded
	ModelCalls        int            `json:"model_calls"`
	InputTokens       int64          `json:"input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	CachedInputTokens *int64         `json:"cached_input_tokens"`
	ReasoningTokens   *int64         `json:"reasoning_tokens"`
	ToolCalls         int            `json:"tool_calls"`
	ContextPulls      int            `json:"context_pulls"`
	Retries           int            `json:"retries"`
	CostUSD           *float64       `json:"cost_usd"`
	Latency           Latency        `json:"latency"`
	Models            []string       `json:"models"`
	Stages            []StageMetrics `json:"stages"`
}

// usageRow is one run_model_usage row.
type usageRow struct {
	stage                    string
	models                   []string
	calls, tools, retries    int
	in, out, modelMs, wallMs int64
	cached, reasoning        *int64
	cost                     *float64
	source                   string // live | replay
}

// reportedSum sums a figure a provider may leave unreported over the rows that made model calls: the sum is known only when
// every such row reported it (a partial sum would understate it), and a row with no model call has none to report.
type reportedSum struct {
	sum      float64
	calling  int // rows with at least one model call
	reported int // of those, rows that reported the figure
}

func (c *reportedSum) add(r usageRow, v *float64) {
	if r.calls == 0 {
		return
	}
	c.calling++
	if v != nil {
		c.reported++
		c.sum += *v
	}
}

func (c reportedSum) known() bool { return c.calling > 0 && c.reported == c.calling }

func (c reportedSum) cost() *float64 {
	if !c.known() {
		return nil
	}
	v := c.sum
	return &v
}

func (c reportedSum) count() *int64 {
	if !c.known() {
		return nil
	}
	v := int64(c.sum)
	return &v
}

// figures are the three optionally-reported figures of a group of rows.
type figures struct{ cost, cached, reasoning reportedSum }

func (f *figures) add(r usageRow) {
	f.cost.add(r, r.cost)
	f.cached.add(r, toFloat(r.cached))
	f.reasoning.add(r, toFloat(r.reasoning))
}

func toFloat(p *int64) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}

// Metrics returns the OperationalMetrics of the episode's run. ErrNotFound: no such episode. A run with no recorded
// usage answers measured false and zeros, never a not-found.
func (r *Reader) Metrics(ctx context.Context, episodeID string) (OperationalMetrics, error) {
	var out OperationalMetrics
	err := r.snapshot(ctx, func(db claimstore.DB) error {
		e, err := loadEpisodeRow(ctx, db, episodeID)
		if err != nil {
			return err
		}
		rows, err := loadUsage(ctx, db, e.RunID)
		if err != nil {
			return err
		}
		pulls, err := countPulls(ctx, db, e.RunID)
		if err != nil {
			return err
		}
		out = summarize(e, rows, pulls)
		return nil
	})
	return out, err
}

func loadUsage(ctx context.Context, db claimstore.DB, runID string) ([]usageRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT stage, to_jsonb(models), model_calls, input_tokens, output_tokens, cached_input_tokens,
 reasoning_tokens, tool_calls, retries, cost_usd::float8, model_ms, wall_ms, usage_source FROM run_model_usage WHERE agent_run_id = $1::uuid ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("controlplane: read model usage: %w", err)
	}
	defer rows.Close()
	var out []usageRow
	for rows.Next() {
		var u usageRow
		var models []byte
		if err := rows.Scan(&u.stage, &models, &u.calls, &u.in, &u.out, &u.cached, &u.reasoning, &u.tools, &u.retries, &u.cost, &u.modelMs, &u.wallMs, &u.source); err != nil {
			return nil, fmt.Errorf("controlplane: scan model usage: %w", err)
		}
		if u.models, err = decodeIDs(models); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func countPulls(ctx context.Context, db claimstore.DB, runID string) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM context_access_log WHERE agent_run_id = $1::uuid`, runID).Scan(&n); err != nil {
		return 0, fmt.Errorf("controlplane: count context pulls: %w", err)
	}
	return n, nil
}

// summarize sums the rows overall and per stage (in the order each stage first ran).
// Replayed rows spent nothing and are never summed: they only decide usage_source and leave measured false when no live
// call was recorded.
func summarize(e episodeRow, all []usageRow, pulls int) OperationalMetrics {
	episode := e.ID
	rows := make([]usageRow, 0, len(all))
	for _, r := range all {
		if r.source == "live" {
			rows = append(rows, r)
		}
	}
	m := OperationalMetrics{Classification: "metric", AgentRunID: e.RunID, DecisionEpisodeID: &episode, Measured: len(rows) > 0,
		UsageSource: sourceOf(len(rows), len(all)-len(rows)), ContextPulls: pulls, Models: []string{}, Stages: []StageMetrics{}}
	var total figures
	stageFig := map[string]*figures{}
	for _, r := range rows {
		m.ModelCalls += r.calls
		m.InputTokens += r.in
		m.OutputTokens += r.out
		m.ToolCalls += r.tools
		m.Retries += r.retries
		m.Latency.WorkerCallMs += r.wallMs
		m.Latency.ModelMs += r.modelMs
		total.add(r)
		for _, name := range r.models {
			if !slices.Contains(m.Models, name) {
				m.Models = append(m.Models, name)
			}
		}
		i := slices.IndexFunc(m.Stages, func(s StageMetrics) bool { return s.Stage == r.stage })
		if i < 0 {
			m.Stages = append(m.Stages, StageMetrics{Stage: r.stage})
			i = len(m.Stages) - 1
			stageFig[r.stage] = &figures{}
		}
		s := &m.Stages[i]
		s.WorkerCalls++
		s.ModelCalls += r.calls
		s.InputTokens += r.in
		s.OutputTokens += r.out
		s.ToolCalls += r.tools
		s.Retries += r.retries
		s.WorkerCallMs += r.wallMs
		s.ModelMs += r.modelMs
		stageFig[r.stage].add(r)
	}
	m.CostUSD, m.CachedInputTokens, m.ReasoningTokens = total.cost.cost(), total.cached.count(), total.reasoning.count()
	for i := range m.Stages {
		f := stageFig[m.Stages[i].Stage]
		m.Stages[i].CostUSD, m.Stages[i].CachedInputTokens, m.Stages[i].ReasoningTokens = f.cost.cost(), f.cached.count(), f.reasoning.count()
	}
	return m
}

// sourceOf names where the recorded answers came from; nil when nothing was recorded.
func sourceOf(live, replayed int) *string {
	var v string
	switch {
	case live > 0 && replayed > 0:
		v = "mixed"
	case live > 0:
		v = "live"
	case replayed > 0:
		v = "replay"
	default:
		return nil
	}
	return &v
}
