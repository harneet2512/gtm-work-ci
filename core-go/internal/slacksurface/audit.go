package slacksurface

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

// AuditRecord is one Slack write the adapter made, as written to the audit log. It exists so a smoke test
// can count the visible channel messages from the bot's own post responses without the channels:history
// scope (HAR-137 section 12): a chat.postMessage record is one visible message, a chat.update record is an
// in-place update of the message with the same ts. It carries ids and counts only, never message text or
// tokens.
type AuditRecord struct {
	At      time.Time `json:"at"`
	Method  string    `json:"method"`
	Channel string    `json:"channel,omitempty"`
	TS      string    `json:"ts,omitempty"` // chat.postMessage: the ts Slack assigned; chat.update: the ts updated
	Blocks  int       `json:"blocks,omitempty"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
}

var tokenPattern = regexp.MustCompile(`(?:xox[a-z]|xapp)-[A-Za-z0-9-]+`)

// AuditPoster wraps a Poster and appends an AuditRecord (one JSON line) for every call to w.
type AuditPoster struct {
	inner Poster
	mu    sync.Mutex
	w     io.Writer
	now   func() time.Time
}

// NewAuditPoster records the calls of inner to w. Use an append-mode file so several processes of one
// smoke run can share it.
func NewAuditPoster(inner Poster, w io.Writer) *AuditPoster {
	return &AuditPoster{inner: inner, w: w, now: time.Now}
}

func (a *AuditPoster) write(r AuditRecord, err error) {
	r.At, r.OK = a.now().UTC(), err == nil
	if err != nil {
		r.Error = tokenPattern.ReplaceAllString(err.Error(), "[redacted]")
	}
	b, merr := json.Marshal(r)
	if merr != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.w.Write(append(b, '\n'))
}

func (a *AuditPoster) PostMessage(ctx context.Context, channel string, m Message) (string, error) {
	ts, err := a.inner.PostMessage(ctx, channel, m)
	a.write(AuditRecord{Method: "chat.postMessage", Channel: channel, TS: ts, Blocks: len(m.Blocks)}, err)
	return ts, err
}

func (a *AuditPoster) UpdateMessage(ctx context.Context, channel, ts string, m Message) error {
	err := a.inner.UpdateMessage(ctx, channel, ts, m)
	a.write(AuditRecord{Method: "chat.update", Channel: channel, TS: ts, Blocks: len(m.Blocks)}, err)
	return err
}

func (a *AuditPoster) FindMessage(ctx context.Context, channel string, since time.Time, meta MessageMeta) (string, bool, error) {
	ts, found, err := a.inner.FindMessage(ctx, channel, since, meta)
	a.write(AuditRecord{Method: "conversations.history", Channel: channel, TS: ts}, err)
	return ts, found, err
}

func (a *AuditPoster) DeleteMessage(ctx context.Context, channel, ts string) error {
	err := a.inner.DeleteMessage(ctx, channel, ts)
	a.write(AuditRecord{Method: "chat.delete", Channel: channel, TS: ts}, err)
	return err
}

func (a *AuditPoster) OpenView(ctx context.Context, triggerID string, v slack.ModalViewRequest) (string, error) {
	id, err := a.inner.OpenView(ctx, triggerID, v)
	a.write(AuditRecord{Method: "views.open", Blocks: len(v.Blocks.BlockSet)}, err)
	return id, err
}

func (a *AuditPoster) UpdateView(ctx context.Context, viewID string, v slack.ModalViewRequest) error {
	err := a.inner.UpdateView(ctx, viewID, v)
	a.write(AuditRecord{Method: "views.update", Blocks: len(v.Blocks.BlockSet)}, err)
	return err
}

func (a *AuditPoster) PostEphemeral(ctx context.Context, channel, user, text string) error {
	err := a.inner.PostEphemeral(ctx, channel, user, text)
	a.write(AuditRecord{Method: "chat.postEphemeral", Channel: channel}, err)
	return err
}

var _ Poster = (*AuditPoster)(nil)
