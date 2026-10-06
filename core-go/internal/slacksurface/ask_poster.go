package slacksurface

import (
	"context"
	"errors"
	"fmt"

	"github.com/slack-go/slack"
)

// SlackAskPoster is the real AskPoster over github.com/slack-go/slack.
type SlackAskPoster struct{ api *slack.Client }

// NewSlackAskPoster wraps an authenticated client (bot token).
func NewSlackAskPoster(api *slack.Client) *SlackAskPoster { return &SlackAskPoster{api: api} }

func askOptions(threadTS, text string, blocks []slack.Block) []slack.MsgOption {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false), slack.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	return opts
}

func (p *SlackAskPoster) PostReply(ctx context.Context, channel, threadTS, text string, blocks []slack.Block) (string, error) {
	_, ts, err := p.api.PostMessageContext(ctx, channel, askOptions(threadTS, text, blocks)...)
	if err != nil {
		return "", fmt.Errorf("slacksurface: chat.postMessage: %w", err)
	}
	return ts, nil
}

func (p *SlackAskPoster) UpdateReply(ctx context.Context, channel, ts, text string, blocks []slack.Block) error {
	if _, _, _, err := p.api.UpdateMessageContext(ctx, channel, ts, askOptions("", text, blocks)...); err != nil {
		return fmt.Errorf("slacksurface: chat.update: %w", err)
	}
	return nil
}

func (p *SlackAskPoster) PostEphemeralTo(ctx context.Context, channel, user, threadTS, text string, blocks []slack.Block) error {
	if channel == "" || user == "" {
		return errors.New("slacksurface: ephemeral needs channel and user")
	}
	if _, err := p.api.PostEphemeralContext(ctx, channel, user, askOptions(threadTS, text, blocks)...); err != nil {
		return fmt.Errorf("slacksurface: chat.postEphemeral: %w", err)
	}
	return nil
}

var _ AskPoster = (*SlackAskPoster)(nil)
