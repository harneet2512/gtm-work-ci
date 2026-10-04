package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

// Poster is the Slack Web API surface the adapter uses. Nothing else in the package talks to Slack.
type Poster interface {
	PostMessage(ctx context.Context, channel string, m Message) (ts string, err error)
	UpdateMessage(ctx context.Context, channel, ts string, m Message) error
	// OpenView must be called while the trigger_id is fresh (about three seconds); it returns the
	// view id so the content can be filled in afterwards with UpdateView.
	OpenView(ctx context.Context, triggerID string, v slack.ModalViewRequest) (viewID string, err error)
	UpdateView(ctx context.Context, viewID string, v slack.ModalViewRequest) error
	PostEphemeral(ctx context.Context, channel, user, text string) error
	// FindMessage looks in the channel's history, since the given time, for the message posted with this
	// metadata and returns the earliest (HAR-136: a post whose ts was never stored is adopted, not repeated).
	FindMessage(ctx context.Context, channel string, since time.Time, meta MessageMeta) (ts string, found bool, err error)
	// DeleteMessage removes a message this bot posted (a duplicate that lost the race to record its ts).
	DeleteMessage(ctx context.Context, channel, ts string) error
}

// SlackPoster is the real Poster over github.com/slack-go/slack.
type SlackPoster struct{ api *slack.Client }

// NewSlackPoster wraps an authenticated client (bot token).
func NewSlackPoster(api *slack.Client) *SlackPoster { return &SlackPoster{api: api} }

func msgOptions(m Message) []slack.MsgOption {
	opts := []slack.MsgOption{
		slack.MsgOptionText(m.Text, true), // true: escape &, <, > in the fallback text
		slack.MsgOptionBlocks(m.Blocks...),
	}
	if m.Meta != nil {
		opts = append(opts, slack.MsgOptionMetadata(m.Meta.slack()))
	}
	return opts
}

func (p *SlackPoster) PostMessage(ctx context.Context, channel string, m Message) (string, error) {
	if err := ValidateMessage(m); err != nil {
		return "", fmt.Errorf("slacksurface: refusing to post invalid message: %w", err)
	}
	_, ts, err := p.api.PostMessageContext(ctx, channel, msgOptions(m)...)
	if err != nil {
		return "", fmt.Errorf("slacksurface: chat.postMessage: %w", err)
	}
	return ts, nil
}

func (p *SlackPoster) UpdateMessage(ctx context.Context, channel, ts string, m Message) error {
	if err := ValidateMessage(m); err != nil {
		return fmt.Errorf("slacksurface: refusing to update with invalid message: %w", err)
	}
	if _, _, _, err := p.api.UpdateMessageContext(ctx, channel, ts, msgOptions(m)...); err != nil {
		return fmt.Errorf("slacksurface: chat.update: %w", err)
	}
	return nil
}

func (p *SlackPoster) OpenView(ctx context.Context, triggerID string, v slack.ModalViewRequest) (string, error) {
	if err := ValidateModal(v); err != nil {
		return "", fmt.Errorf("slacksurface: refusing to open invalid modal: %w", err)
	}
	resp, err := p.api.OpenViewContext(ctx, triggerID, v)
	if err != nil {
		return "", fmt.Errorf("slacksurface: views.open: %w", err)
	}
	return resp.View.ID, nil
}

func (p *SlackPoster) UpdateView(ctx context.Context, viewID string, v slack.ModalViewRequest) error {
	if err := ValidateModal(v); err != nil {
		return fmt.Errorf("slacksurface: refusing to update with invalid modal: %w", err)
	}
	if _, err := p.api.UpdateViewContext(ctx, v, "", "", viewID); err != nil {
		return fmt.Errorf("slacksurface: views.update: %w", err)
	}
	return nil
}

func (p *SlackPoster) PostEphemeral(ctx context.Context, channel, user, text string) error {
	if channel == "" || user == "" {
		return errors.New("slacksurface: ephemeral needs channel and user")
	}
	if _, err := p.api.PostEphemeralContext(ctx, channel, user, slack.MsgOptionText(text, true)); err != nil {
		return fmt.Errorf("slacksurface: chat.postEphemeral: %w", err)
	}
	return nil
}

// historyLimit and historyPages bound one FindMessage: 200 messages a page, at most 10 pages, which is far more
// than a demo channel holds between a reservation and its reconciliation.
const (
	historyLimit = 200
	historyPages = 10
)

// FindMessage implements Poster. It needs the conversations.history scope for the channel type and
// metadata.message:read (docs/slack-setup.md).
func (p *SlackPoster) FindMessage(ctx context.Context, channel string, since time.Time, meta MessageMeta) (string, bool, error) {
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channel, Oldest: slackTS(since), Limit: historyLimit, Inclusive: true, IncludeAllMetadata: true,
	}
	for page := 0; page < historyPages; page++ {
		resp, err := p.api.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return "", false, fmt.Errorf("slacksurface: conversations.history: %w", err)
		}
		earliest := ""
		for _, m := range resp.Messages {
			if meta.matches(m.Metadata) && (earliest == "" || m.Timestamp < earliest) { // history is newest first
				earliest = m.Timestamp
			}
		}
		if earliest != "" {
			return earliest, true, nil
		}
		if !resp.HasMore || resp.ResponseMetaData.NextCursor == "" {
			return "", false, nil
		}
		params.Cursor = resp.ResponseMetaData.NextCursor
	}
	return "", false, fmt.Errorf("slacksurface: conversations.history: more than %d messages since the reservation", historyLimit*historyPages)
}

// DeleteMessage implements Poster.
func (p *SlackPoster) DeleteMessage(ctx context.Context, channel, ts string) error {
	if _, _, err := p.api.DeleteMessageContext(ctx, channel, ts); err != nil {
		return fmt.Errorf("slacksurface: chat.delete: %w", err)
	}
	return nil
}

var _ Poster = (*SlackPoster)(nil)
