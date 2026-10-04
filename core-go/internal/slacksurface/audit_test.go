package slacksurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type stubPoster struct{ err error }

func (s stubPoster) PostMessage(context.Context, string, Message) (string, error) {
	return "1700000000.000101", s.err
}
func (s stubPoster) UpdateMessage(context.Context, string, string, Message) error { return s.err }
func (s stubPoster) OpenView(context.Context, string, slack.ModalViewRequest) (string, error) {
	return "V1", s.err
}
func (s stubPoster) UpdateView(context.Context, string, slack.ModalViewRequest) error { return s.err }
func (s stubPoster) FindMessage(context.Context, string, time.Time, MessageMeta) (string, bool, error) {
	return "", false, s.err
}
func (s stubPoster) DeleteMessage(context.Context, string, string) error         { return s.err }
func (s stubPoster) PostEphemeral(context.Context, string, string, string) error { return s.err }

func auditLines(t *testing.T, buf *bytes.Buffer) []AuditRecord {
	t.Helper()
	var out []AuditRecord
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r AuditRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func TestAuditPosterRecordsEveryWriteWithTheTsSlackAssigned(t *testing.T) {
	var buf bytes.Buffer
	p := NewAuditPoster(stubPoster{}, &buf)
	ctx := context.Background()
	msg := Message{Blocks: []slack.Block{slack.NewDividerBlock(), slack.NewDividerBlock()}}

	ts, err := p.PostMessage(ctx, "C1", msg)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.UpdateMessage(ctx, "C1", ts, msg)
	_, _ = p.OpenView(ctx, "T", slack.ModalViewRequest{})
	_ = p.UpdateView(ctx, "V1", slack.ModalViewRequest{})
	_ = p.PostEphemeral(ctx, "C1", "U1", "secret text")

	got := auditLines(t, &buf)
	methods := []string{"chat.postMessage", "chat.update", "views.open", "views.update", "chat.postEphemeral"}
	if len(got) != len(methods) {
		t.Fatalf("%d records, want %d", len(got), len(methods))
	}
	for i, m := range methods {
		if got[i].Method != m || !got[i].OK {
			t.Fatalf("record %d = %+v, want %s ok", i, got[i], m)
		}
	}
	if got[0].TS != ts || got[1].TS != ts || got[0].Blocks != 2 {
		t.Fatalf("post/update records = %+v %+v", got[0], got[1])
	}
	if strings.Contains(buf.String(), "secret text") {
		t.Fatal("the audit log holds message text")
	}
}

func TestAuditPosterRedactsTokensInErrors(t *testing.T) {
	var buf bytes.Buffer
	p := NewAuditPoster(stubPoster{err: errors.New("slack said no to xoxb-123-abc and xapp-1-DEF456")}, &buf)

	_, err := p.PostMessage(context.Background(), "C1", Message{})

	if err == nil {
		t.Fatal("the inner error must be returned unchanged")
	}
	rec := auditLines(t, &buf)[0]
	if rec.OK || strings.Contains(rec.Error, "xoxb-") || strings.Contains(rec.Error, "xapp-") || !strings.Contains(rec.Error, "[redacted]") {
		t.Fatalf("record = %+v", rec)
	}
}
