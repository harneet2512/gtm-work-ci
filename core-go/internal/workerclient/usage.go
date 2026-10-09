package workerclient

import (
	"context"
	"encoding/json"
	"strings"
)

// Usage is what the worker spent on one request (contracts/schemas/worker_usage.v1.json), returned as `usage` on the
// responses of /v1/strategies, /v1/judge, /v1/revise and /v1/human-delta. A METRIC (HAR-97 M1-M5), never an eval.
type Usage struct {
	ModelCalls        int      `json:"model_calls"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	CachedInputTokens *int64   `json:"cached_input_tokens"` // nil: the provider did not report it (never read as 0)
	ReasoningTokens   *int64   `json:"reasoning_tokens"`    // nil: the provider did not report it (never read as 0)
	ToolCalls         int      `json:"tool_calls"`
	Retries           int      `json:"retries"`
	ModelMs           int64    `json:"model_ms"`
	CostUSD           *float64 `json:"cost_usd"`
	Models            []string `json:"models"`
	// UsageSource is "live" (a real provider answered) or "replay" (recorded cassettes did: nothing was spent). A
	// usage object that does not say is not recorded: core will not guess whether money was spent.
	UsageSource string `json:"usage_source"`
}

// Usage sources a worker may report.
const (
	UsageLive   = "live"
	UsageReplay = "replay"
)

// valid reports whether the source is known and every count is non-negative: a usage object that is not is ignored,
// never recorded.
func (u Usage) valid() bool {
	if u.UsageSource != UsageLive && u.UsageSource != UsageReplay {
		return false
	}
	for _, n := range []int64{int64(u.ModelCalls), u.InputTokens, u.OutputTokens, int64(u.ToolCalls), int64(u.Retries), u.ModelMs} {
		if n < 0 {
			return false
		}
	}
	for _, p := range []*int64{u.CachedInputTokens, u.ReasoningTokens} {
		if p != nil && *p < 0 {
			return false
		}
	}
	return u.CostUSD == nil || *u.CostUSD >= 0
}

// UsageRecord is one worker call's usage with the stage (the worker operation) and the wall time core measured.
type UsageRecord struct {
	Stage  string // draft | strategies | judge | revise | human_delta | judgment_inference (operational_metrics stage)
	Usage  Usage
	WallMs int64
}

// UsageSink receives the usage of every worker call made with a context that carries it. RecordUsage must not block on
// a database: the caller may hold a transaction (the human-delta call happens inside the send transaction), so sinks
// collect in memory and the owner flushes after the transaction ends (internal/usagestore).
type UsageSink interface {
	RecordUsage(UsageRecord)
}

type usageSinkKey struct{}

// WithUsageSink returns a context whose worker calls report their usage to sink.
func WithUsageSink(ctx context.Context, sink UsageSink) context.Context {
	return context.WithValue(ctx, usageSinkKey{}, sink)
}

// UsageSinkFrom returns the context's usage sink, or nil. A caller that wants to see one call's usage wraps it.
func UsageSinkFrom(ctx context.Context) UsageSink { return usageSinkFrom(ctx) }

func usageSinkFrom(ctx context.Context) UsageSink {
	sink, _ := ctx.Value(usageSinkKey{}).(UsageSink)
	return sink
}

// ReportUsage hands one record to the context's sink, if it has one. The HTTP client does this itself; a double that
// stands in for the client (a test worker) calls it to behave like a worker that reports usage.
func ReportUsage(ctx context.Context, rec UsageRecord) {
	if sink := usageSinkFrom(ctx); sink != nil {
		sink.RecordUsage(rec)
	}
}

// stageOfPath maps a worker path to its operational_metrics stage.
func stageOfPath(path string) string {
	stage := strings.TrimPrefix(path, "/v1/")
	return strings.ReplaceAll(stage, "-", "_")
}

// reportUsage hands the usage of a successful response to the context's sink. A response without usage (an older
// worker), with a malformed or negative usage object, or a call with no sink reports nothing: usage is best effort and
// never fails the call.
func reportUsage(ctx context.Context, path string, body []byte, wallMs int64) {
	sink := usageSinkFrom(ctx)
	if sink == nil {
		return
	}
	var env struct {
		Usage *Usage `json:"usage"`
	}
	if json.Unmarshal(body, &env) != nil || env.Usage == nil || !env.Usage.valid() {
		return
	}
	sink.RecordUsage(UsageRecord{Stage: stageOfPath(path), Usage: *env.Usage, WallMs: wallMs})
}
