package codespace

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// realStub records what reaches the "real" channel.
type realStub struct {
	posted, updated, whispered, views int
}

func (r *realStub) PostMessage(context.Context, string, slacksurface.Message) (string, error) {
	r.posted++
	return "1.5", nil
}
func (r *realStub) UpdateMessage(context.Context, string, string, slacksurface.Message) error {
	r.updated++
	return nil
}
func (r *realStub) OpenView(context.Context, string, slack.ModalViewRequest) (string, error) {
	r.views++
	return "V", nil
}
func (r *realStub) UpdateView(context.Context, string, slack.ModalViewRequest) error {
	r.views++
	return nil
}
func (r *realStub) PostEphemeral(context.Context, string, string, string) error {
	r.whispered++
	return nil
}
func (r *realStub) FindMessage(context.Context, string, time.Time, slacksurface.MessageMeta) (string, bool, error) {
	return "", false, nil
}
func (r *realStub) DeleteMessage(context.Context, string, string) error { return nil }

func TestMirrorPosterSendsMessagesToSlackButKeepsModalsAndWhispersLocal(t *testing.T) {
	ctx := context.Background()
	real, fake := &realStub{}, NewFakeSlack()
	p := NewMirrorPoster(real, fake)
	ts, err := p.PostMessage(ctx, "C1", textMessage("Message 2", nil))
	if err != nil || ts != "1.5" || real.posted != 1 {
		t.Fatalf("post = %s %v real=%d", ts, err, real.posted)
	}
	if m, ok := fake.Message("C1", ts); !ok || MessageTexts(m) != "Message 2" {
		t.Fatalf("the human's session reads the message back under Slack's ts: %v %v", m, ok)
	}
	if err := p.UpdateMessage(ctx, "C1", "9.9", textMessage("adopted", nil)); err != nil || real.updated != 1 {
		t.Fatalf("update = %v real=%d", err, real.updated)
	}
	if m, ok := fake.Message("C1", "9.9"); !ok || MessageTexts(m) != "adopted" {
		t.Fatal("an update of a message the bot posted is mirrored, so the human can read its buttons")
	}
	id, err := p.OpenView(ctx, "T1", slack.ModalViewRequest{Type: slack.VTModal, Title: slack.NewTextBlockObject("plain_text", "t", false, false)})
	if err != nil || id == "" || real.views != 0 {
		t.Fatalf("a synthetic trigger id must never reach Slack: %q %v real=%d", id, err, real.views)
	}
	_ = p.PostEphemeral(ctx, "C1", "U1", "That did not go through.")
	if real.whispered != 0 || len(fake.Problems()) != 1 {
		t.Fatal("a whisper stays local so the run sees the failure and no one is pinged")
	}
}
