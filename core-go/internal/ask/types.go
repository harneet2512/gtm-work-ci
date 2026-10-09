// Package ask is Ask Cliff (owner-approved HAR-129 contract change, 2026-10-06): the transport-neutral service that
// answers a free-form question with a tool-using worker agent. It owns everything a Slack handler must not: it mints
// the ask token, serves the agent's read tools from the existing core read APIs (in process, GET only), checks the
// answer's citations and links, offers state-changing demo actions only behind a confirmation, and refuses
// every demo action behind a confirmation. There is no tool here that sends an email or writes the CRM.
package ask

import (
	"context"
	"errors"
)

// Channel kinds of a question.
const (
	ChannelDM     = "dm"
	ChannelThread = "thread"
)

// Demo actions (contract: ask_cliff.actions).
const (
	ActionPlayNext   = "play_next"
	ActionDemoStatus = "demo_status"
)

// Limits of a question and an answer (contracts/openapi/core.yaml).
const (
	MaxTextChars   = 2000
	MaxUserChars   = 64
	MaxAnswerChars = 12000
	MaxCitations   = 20
)

// Errors the HTTP layer maps to statuses.
var (
	ErrBadRequest   = errors.New("ask: bad request")
	ErrUnknownTool  = errors.New("ask: unknown tool")
	ErrBadArguments = errors.New("ask: bad tool arguments")
	ErrBadToken     = errors.New("ask: invalid ask token")
	ErrUnavailable  = errors.New("ask: not available")
)

// Request is POST /ask.
type Request struct {
	Text        string `json:"text"`
	ChannelKind string `json:"channel_kind"`
	User        string `json:"user"`
	ThreadRef   string `json:"thread_ref,omitempty"`
}

// Citation names the tool call an answer rests on and, when the tool has one, its deep link.
type Citation struct {
	CallID string  `json:"call_id"`
	Tool   string  `json:"tool"`
	Label  string  `json:"label"`
	URL    *string `json:"url"`
}

// ProposedAction is a state-changing demo action Cliff offers; nothing has run.
type ProposedAction struct {
	Kind                 string `json:"kind"`
	Summary              string `json:"summary"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

// Answer is the 200 body of POST /ask.
type Answer struct {
	AnswerMarkdown string          `json:"answer_markdown"`
	Citations      []Citation      `json:"citations"`
	ProposedAction *ProposedAction `json:"proposed_action,omitempty"`
}

// ActionRequest is POST /ask/actions.
type ActionRequest struct {
	Kind string `json:"kind"`
	User string `json:"user"`
}

// ActionResult is its 200 body.
type ActionResult struct {
	Status          string `json:"status"` // done or refused
	MessageMarkdown string `json:"message_markdown"`
	Reason          string `json:"reason,omitempty"`
}

// Link is a deep link into the web control plane.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// ToolResult is the body of POST /internal/ask/tools/{tool}.
type ToolResult struct {
	Tool      string `json:"tool"`
	OK        bool   `json:"ok"`
	Empty     bool   `json:"empty"`
	Truncated bool   `json:"truncated"`
	DryRun    bool   `json:"dry_run,omitempty"`
	Data      any    `json:"data"`
	Links     []Link `json:"links"`
}

// WorkerRequest is what core sends the worker's POST /v1/ask.
type WorkerRequest struct {
	Text        string `json:"text"`
	ChannelKind string `json:"channel_kind"`
	AskToken    string `json:"ask_token"`
}

// WorkerCitation is a citation as the worker returns it.
type WorkerCitation struct {
	CallID string  `json:"call_id"`
	Tool   string  `json:"tool"`
	Label  string  `json:"label"`
	URL    *string `json:"url"`
}

// WorkerAction is the action the worker proposes; core decides whether it may be offered.
type WorkerAction struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

// WorkerAnswer is the worker's 200 body.
type WorkerAnswer struct {
	AnswerMarkdown string           `json:"answer_markdown"`
	Citations      []WorkerCitation `json:"citations"`
	ProposedAction *WorkerAction    `json:"proposed_action"`
	DontKnow       bool             `json:"dont_know"`
	TimedOut       bool             `json:"timed_out"`
	ToolCalls      int              `json:"tool_calls"`
	Model          string           `json:"model"`
}

// Worker is the model worker's ask endpoint (workerclient.Client implements it).
type Worker interface {
	Ask(ctx context.Context, req WorkerRequest) (WorkerAnswer, error)
}
