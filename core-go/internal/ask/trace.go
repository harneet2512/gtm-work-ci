package ask

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// maxStepOutput bounds the output kept for one traced tool call (contracts/openapi/core.yaml AskTraceStep).
const maxStepOutput = 4096

// TraceStep is one tool call of an answer: what was asked and what came back.
type TraceStep struct {
	N      int            `json:"n"`
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	OK     bool           `json:"ok"`
	Empty  bool           `json:"empty"`
	DryRun bool           `json:"dry_run,omitempty"`
	Output any            `json:"output"`
	Links  []Link         `json:"links,omitempty"`
}

// Trace is the record of one answer, served at GET /ask/traces/{id} and shown by the web control plane.
type Trace struct {
	ID             string      `json:"id"`
	ThreadRef      string      `json:"thread_ref"`
	Question       string      `json:"question"`
	AnswerMarkdown string      `json:"answer_markdown"`
	CreatedAt      time.Time   `json:"created_at"`
	Model          string      `json:"model"`
	Steps          []TraceStep `json:"steps"`
	TokensIn       int         `json:"tokens_in"`
	TokensOut      int         `json:"tokens_out"`
	CostUSD        *float64    `json:"cost_usd"`
	DurationMS     int64       `json:"duration_ms"`
	TimedOut       bool        `json:"timed_out"`
}

func newTraceID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b) // crypto/rand does not fail on supported platforms
	return "tr_" + hex.EncodeToString(b)
}

// boundOutput keeps a tool's data when it is small and a cut preview when it is not.
func boundOutput(data any) any {
	raw, err := json.Marshal(data)
	if err != nil || len(raw) <= maxStepOutput {
		return data
	}
	return map[string]any{"truncated": true, "preview": string([]rune(string(raw))[:maxStepOutput/2])}
}

// stepLog collects what one ask does: the steps the worker takes through core's tool endpoint.
type stepLog struct {
	mu    sync.Mutex
	steps []TraceStep
}

func (r *stepLog) add(tool string, args map[string]any, res ToolResult) {
	if args == nil {
		args = map[string]any{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, TraceStep{N: len(r.steps) + 1, Tool: tool, Args: args, OK: res.OK, Empty: res.Empty,
		DryRun: res.DryRun, Output: boundOutput(res.Data), Links: res.Links})
}

func (r *stepLog) failed(tool string, args map[string]any, err error) {
	if args == nil {
		args = map[string]any{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, TraceStep{N: len(r.steps) + 1, Tool: tool, Args: args, Output: map[string]any{"error": err.Error()}})
}

func (r *stepLog) snapshot() []TraceStep {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TraceStep{}, r.steps...)
}
