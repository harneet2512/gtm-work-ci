package codespace

import (
	"context"
	"time"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// mirrorPoster is the transport of the live drive: Cliff's messages go to the real channel (the bot posts them, through the
// product), and the same messages are kept in a FakeSlack so the scripted human can read the buttons back. Modal views and
// whispers stay local: Slack refuses a synthetic trigger id, and a whisper would ping a real person.
type mirrorPoster struct {
	real slacksurface.Poster
	fake *FakeSlack
}

// NewMirrorPoster wires the real poster and the local mirror.
func NewMirrorPoster(real slacksurface.Poster, fake *FakeSlack) slacksurface.Poster {
	return mirrorPoster{real: real, fake: fake}
}

func (m mirrorPoster) PostMessage(ctx context.Context, channel string, msg slacksurface.Message) (string, error) {
	ts, err := m.real.PostMessage(ctx, channel, msg)
	if err != nil {
		return "", err
	}
	m.fake.Put(channel, ts, msg)
	return ts, nil
}

func (m mirrorPoster) UpdateMessage(ctx context.Context, channel, ts string, msg slacksurface.Message) error {
	if err := m.real.UpdateMessage(ctx, channel, ts, msg); err != nil {
		return err
	}
	m.fake.Put(channel, ts, msg)
	return nil
}

func (m mirrorPoster) OpenView(ctx context.Context, trigger string, v slack.ModalViewRequest) (string, error) {
	return m.fake.OpenView(ctx, trigger, v)
}

func (m mirrorPoster) UpdateView(ctx context.Context, id string, v slack.ModalViewRequest) error {
	return m.fake.UpdateView(ctx, id, v)
}

func (m mirrorPoster) PostEphemeral(ctx context.Context, channel, user, text string) error {
	return m.fake.PostEphemeral(ctx, channel, user, text)
}

func (m mirrorPoster) FindMessage(ctx context.Context, channel string, since time.Time, meta slacksurface.MessageMeta) (string, bool, error) {
	return m.real.FindMessage(ctx, channel, since, meta)
}

func (m mirrorPoster) DeleteMessage(ctx context.Context, channel, ts string) error {
	return m.real.DeleteMessage(ctx, channel, ts)
}
