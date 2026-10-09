package codespace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/slack-go/slack"
)

// ChannelMessage is the part of a channel message the janitor needs.
type ChannelMessage struct {
	TS      string
	User    string
	BotID   string
	Subtype string // "" for an ordinary message; channel_join, channel_name ... for Slack's own system notices
}

// userSubtypes are the message subtypes a person or bot posts and chat.delete can remove. Every other non-empty subtype
// (channel_join, channel_leave, channel_name, channel_purpose, channel_topic, channel_archive ...) is a Slack system
// notice: Slack answers cant_delete_message for it, and it is not an earlier run's output.
var userSubtypes = map[string]bool{"bot_message": true, "thread_broadcast": true, "reply_broadcast": true, "me_message": true, "file_share": true}

func isSystemNotice(m ChannelMessage) bool { return m.Subtype != "" && !userSubtypes[m.Subtype] }

// SlackChannel is the Slack Web API the janitor uses: who the bot is, the channel history and chat.delete.
type SlackChannel interface {
	// BotIdentity is the bot's user id and its bot id (auth.test).
	BotIdentity(ctx context.Context) (userID, botID string, err error)
	// History is one page of the channel's messages, newest first, and the cursor of the next page ("" at the end).
	History(ctx context.Context, channel, cursor string) ([]ChannelMessage, string, error)
	Delete(ctx context.Context, channel, ts string) error
}

// Janitor deletes the bot's OWN messages from the demo channel, so a reset audience does not see the earlier run's Message 1
// to 3 above the new ones. It never touches a message a human wrote.
type Janitor struct {
	API     SlackChannel
	Channel string
	Log     func(format string, args ...any) // one line per message left in place; nil is quiet
}

func (j Janitor) logf(format string, args ...any) {
	if j.Log != nil {
		j.Log(format, args...)
	}
}

const (
	janitorRetryWait = 2 * time.Second
	janitorMaxPages  = 50
)

// Clear deletes every message the bot posted in the channel and returns how many. A message that cannot be deleted does
// not stop the rest, but the error is reported: stale messages must never be left behind quietly.
func (j Janitor) Clear(ctx context.Context) (int, error) {
	if j.Channel == "" {
		return 0, errors.New("codespace: SLACK_CHANNEL_ID is not set, so the channel cannot be cleared")
	}
	user, botApp, err := j.API.BotIdentity(ctx)
	if err != nil {
		return 0, fmt.Errorf("codespace: ask Slack who the bot is: %w", err)
	}
	deleted := 0
	var failures []error
	cursor := ""
	for page := 0; page < janitorMaxPages; page++ {
		msgs, next, err := j.API.History(ctx, j.Channel, cursor)
		if err != nil {
			return deleted, errors.Join(append(failures, fmt.Errorf("codespace: read the channel history: %w", err))...)
		}
		for _, m := range msgs {
			if isSystemNotice(m) {
				j.logf("skipping %s: system notice (subtype %s), Slack does not allow deleting it", m.TS, m.Subtype)
				continue
			}
			if m.User != user && (botApp == "" || m.BotID != botApp) {
				j.logf("leaving %s: message from a human (user %q, bot %q), only the bot's own messages are deleted", m.TS, m.User, m.BotID)
				continue
			}
			if err := j.API.Delete(ctx, j.Channel, m.TS); err != nil {
				failures = append(failures, fmt.Errorf("codespace: delete message %s: %w", m.TS, err))
				continue
			}
			deleted++
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return deleted, errors.Join(failures...)
}

// slackChannelAPI is SlackChannel over github.com/slack-go/slack with the bot token.
type slackChannelAPI struct{ api *slack.Client }

// NewSlackChannelAPI wraps the bot token's client; apiURL overrides Slack's address (tests), "" is Slack.
func NewSlackChannelAPI(botToken, apiURL string) SlackChannel {
	opts := []slack.Option{}
	if apiURL != "" {
		opts = append(opts, slack.OptionAPIURL(apiURL))
	}
	return slackChannelAPI{api: slack.New(botToken, opts...)}
}

func (s slackChannelAPI) BotIdentity(ctx context.Context) (string, string, error) {
	resp, err := s.api.AuthTestContext(ctx)
	if err != nil {
		return "", "", err
	}
	return resp.UserID, resp.BotID, nil
}

func (s slackChannelAPI) History(ctx context.Context, channel, cursor string) ([]ChannelMessage, string, error) {
	resp, err := s.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channel, Cursor: cursor, Limit: 200})
	if err != nil {
		return nil, "", err
	}
	out := make([]ChannelMessage, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		out = append(out, ChannelMessage{TS: m.Timestamp, User: m.User, BotID: m.BotID, Subtype: m.SubType})
	}
	next := ""
	if resp.HasMore {
		next = resp.ResponseMetaData.NextCursor
	}
	return out, next, nil
}

func (s slackChannelAPI) Delete(ctx context.Context, channel, ts string) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if _, _, err = s.api.DeleteMessageContext(ctx, channel, ts); err == nil {
			return nil
		}
		var limited *slack.RateLimitedError
		if !errors.As(err, &limited) {
			return err
		}
		wait := limited.RetryAfter
		if wait <= 0 {
			wait = janitorRetryWait
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return err
}
