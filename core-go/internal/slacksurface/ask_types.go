package slacksurface

import (
	"context"
	"errors"

	"github.com/slack-go/slack"
)

// Ask Cliff (owner-approved HAR-129 contract change, 2026-10-06). This file holds the wire types of POST /ask and
// POST /ask/actions (contracts/openapi/core.yaml) and what the adapter needs from Slack. The adapter holds no
// business logic: it forwards the question and who asked, shows the answer where the question came from, and asks a
// human to confirm before it forwards a state-changing action.

// Channel kinds of a question (contract: AskRequest.channel_kind).
const (
	AskChannelDM     = "dm"
	AskChannelThread = "thread"
)

// Errors the core client maps from the ask routes' statuses.
var (
	// ErrAskUnavailable: the deployment has no worker to ask (503).
	ErrAskUnavailable = errors.New("slacksurface: ask is not available")
	// ErrAskProvider: the model provider is out of credits or paused (424).
	ErrAskProvider = errors.New("slacksurface: the model provider is unavailable")
)

// AskRequest is POST /ask.
type AskRequest struct {
	Text        string `json:"text"`
	ChannelKind string `json:"channel_kind"`
	User        string `json:"user"`
	ThreadRef   string `json:"thread_ref,omitempty"`
	TurnID      string `json:"turn_id,omitempty"`
}

// AskProposedAction is a demo action Cliff offers; nothing has run.
type AskProposedAction struct {
	Kind                 string `json:"kind"`
	Summary              string `json:"summary"`
	Target               string `json:"target,omitempty"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}

// AskAnswer is the 200 body of POST /ask.
type AskAnswer struct {
	AnswerMarkdown string             `json:"answer_markdown"`
	ProposedAction *AskProposedAction `json:"proposed_action,omitempty"`
}

// AskActionRequest is POST /ask/actions.
type AskActionRequest struct {
	Kind      string `json:"kind"`
	User      string `json:"user"`
	ThreadRef string `json:"thread_ref,omitempty"`
	TurnID    string `json:"turn_id,omitempty"`
}

// AskActionResult is its 200 body.
type AskActionResult struct {
	Status          string `json:"status"`
	MessageMarkdown string `json:"message_markdown"`
	Reason          string `json:"reason,omitempty"`
	// Continuation is the rest of a multi-step task, answered after the action ran (core resumes the paused task).
	Continuation *AskAnswer `json:"continuation,omitempty"`
}

// AskProgress is GET /ask/turns/{turn_id}/progress: the step lines of the question being answered.
type AskProgress struct {
	State string   `json:"state"`
	Lines []string `json:"lines"`
}

// AskCore is what the Slack adapter needs from core for Ask Cliff.
type AskCore interface {
	Ask(ctx context.Context, req AskRequest) (AskAnswer, error)
	RunAction(ctx context.Context, req AskActionRequest) (AskActionResult, error)
	Progress(ctx context.Context, turnID string) (AskProgress, error)
}

// AskPoster is the Slack Web API surface Ask Cliff uses. threadTS "" posts at the top level of a DM; in a channel
// the adapter always passes the thread, so a channel's top level is never written.
type AskPoster interface {
	// PostReply posts a message (in the thread when threadTS is set) and returns its ts.
	PostReply(ctx context.Context, channel, threadTS, text string, blocks []slack.Block) (ts string, err error)
	// UpdateReply replaces a message in place.
	UpdateReply(ctx context.Context, channel, ts, text string, blocks []slack.Block) error
	// PostEphemeralTo shows a message only to one user (in the thread when threadTS is set).
	PostEphemeralTo(ctx context.Context, channel, user, threadTS, text string, blocks []slack.Block) error
}
