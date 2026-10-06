package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// maxDataBytes bounds the data of one tool result so the worker's response cap (16 KiB) always holds.
const maxDataBytes = 9000

// maxStringChars clips every long text inside a result (activity bodies, quotes).
const maxStringChars = 400

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Tools serves the agent's read tools from core's own read APIs through Backend. Nothing here writes: Backend.Get is the
// only call the tools make (TestToolsOnlyEverReadFromTheBackend), and there is no tool that sends or writes.
type Tools struct {
	B     Backend
	L     Links
	Clock func() time.Time
}

// ToolNames are the tools of POST /internal/ask/tools/{tool}.
var ToolNames = []string{
	"list_accounts", "account_state", "timeline", "graph_neighborhood", "episode", "gate_results", "strategies",
	"human_decision", "judgment_inference", "knowledge", "knowledge_attribution", "search_activities",
	"draft_followup", "crm_update_preview",
}

func (t *Tools) now() time.Time {
	if t.Clock != nil {
		return t.Clock()
	}
	return time.Now()
}

type toolFn func(*Tools, context.Context, map[string]any) (ToolResult, error)

var registry = map[string]toolFn{
	"list_accounts":         (*Tools).listAccounts,
	"account_state":         (*Tools).accountState,
	"timeline":              (*Tools).timeline,
	"graph_neighborhood":    (*Tools).graphNeighborhood,
	"episode":               (*Tools).episode,
	"gate_results":          (*Tools).gateResults,
	"strategies":            (*Tools).strategies,
	"human_decision":        (*Tools).humanDecision,
	"judgment_inference":    (*Tools).judgmentInference,
	"knowledge":             (*Tools).knowledge,
	"knowledge_attribution": (*Tools).knowledgeAttribution,
	"search_activities":     (*Tools).searchActivities,
	"draft_followup":        (*Tools).draftFollowup,
	"crm_update_preview":    (*Tools).crmUpdatePreview,
}

// Run executes one tool. ErrUnknownTool and ErrBadArguments are the caller's mistakes; anything else is a failure.
func (t *Tools) Run(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	fn, ok := registry[name]
	if !ok {
		return ToolResult{}, ErrUnknownTool
	}
	res, err := fn(t, ctx, args)
	if err != nil {
		return ToolResult{}, err
	}
	res.Tool = name
	if res.Links == nil {
		res.Links = []Link{}
	}
	res.Data, res.Truncated = bound(clip(res.Data), maxDataBytes)
	return res, nil
}

func badArgs(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadArguments, fmt.Sprintf(format, a...))
}

func notFound(msg string) ToolResult {
	return ToolResult{OK: false, Empty: true, Data: map[string]any{"error": msg}}
}

func found(data any, links ...Link) ToolResult {
	return ToolResult{OK: true, Empty: isEmpty(data), Data: data, Links: Of(links...)}
}

func str(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

func intArg(args map[string]any, key string, def, maxv int) int {
	switch v := args[key].(type) {
	case float64:
		return min(max(int(v), 1), maxv)
	case int:
		return min(max(v, 1), maxv)
	}
	return def
}

// get reads one path and decodes the JSON. 404 is (nil, 404, nil); other non-200 statuses are errors.
func (t *Tools) get(ctx context.Context, path string) (any, int, error) {
	status, body, err := t.B.Get(ctx, path)
	if err != nil {
		return nil, 0, err
	}
	if status == 404 {
		return nil, 404, nil
	}
	if status != 200 {
		return nil, status, fmt.Errorf("ask: core GET %s answered %d", path, status)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, status, fmt.Errorf("ask: core GET %s: undecodable answer: %w", path, err)
	}
	return v, status, nil
}

// getCode is get that also reports the error code of a 404 envelope.
func (t *Tools) getCode(ctx context.Context, path string) (v any, code string, err error) {
	status, body, err := t.B.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if status == 404 {
		var env struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(body, &env)
		return nil, env.Error.Code, nil
	}
	if status != 200 {
		return nil, "", fmt.Errorf("ask: core GET %s answered %d", path, status)
	}
	return v, "", json.Unmarshal(body, &v)
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case string:
		return x == ""
	}
	return false
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }
func text(v any) string        { s, _ := v.(string); return s }

// clip shortens every string longer than maxStringChars, so one activity body cannot fill a result.
func clip(v any) any {
	switch x := v.(type) {
	case string:
		if r := []rune(x); len(r) > maxStringChars {
			return string(r[:maxStringChars]) + "..."
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = clip(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = clip(e)
		}
		return out
	}
	return v
}

// bound keeps the encoded data within limit: a list loses its tail, an object its largest member.
func bound(v any, limit int) (any, bool) {
	truncated := false
	for size(v) > limit {
		truncated = true
		switch x := v.(type) {
		case []any:
			if len(x) <= 1 {
				return map[string]any{"note": "too large to show"}, true
			}
			v = x[:len(x)/2]
		case map[string]any:
			big, bigSize := "", -1
			for k, e := range x {
				if s := size(e); s > bigSize {
					big, bigSize = k, s
				}
			}
			next := make(map[string]any, len(x))
			for k, e := range x {
				if k != big {
					next[k] = e
				}
			}
			if bigSize > limit/2 {
				if l, ok := x[big].([]any); ok && len(l) > 1 {
					next[big] = l[:len(l)/2]
				}
			}
			v = next
		default:
			return map[string]any{"note": "too large to show"}, true
		}
	}
	return v, truncated
}

func size(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// parseWhen reads a date (YYYY-MM-DD) or an RFC3339 time. A date names the whole UTC day.
func parseWhen(s string) (at time.Time, dateOnly bool, err error) {
	if t, e := time.Parse("2006-01-02", s); e == nil {
		return t, true, nil
	}
	if t, e := time.Parse(time.RFC3339, s); e == nil {
		return t.UTC(), false, nil
	}
	return time.Time{}, false, badArgs("%q is not a date: use YYYY-MM-DD or an RFC3339 time", s)
}

// endOf is the first instant after the moment a date or time names: the world "as of" a date includes that day.
func endOf(at time.Time, dateOnly bool) time.Time {
	if dateOnly {
		return at.AddDate(0, 0, 1)
	}
	return at
}

func q(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	v := url.Values{}
	for k, s := range params {
		if s != "" {
			v.Set(k, s)
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}
