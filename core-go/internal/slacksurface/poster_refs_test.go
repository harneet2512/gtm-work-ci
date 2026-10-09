package slacksurface

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/slackfake"
)

// The real SlackPoster against the fake Slack's HTTP API: message metadata, conversations.history and chat.delete.

func realPoster(t *testing.T) (*SlackPoster, *slackfake.Server) {
	t.Helper()
	fs := slackfake.New()
	t.Cleanup(fs.Close)
	api, _ := NewSlackClients(Config{AppToken: "xapp-t", BotToken: "xoxb-t", ChannelID: "C1"}, fs.APIURL())
	return NewSlackPoster(api), fs
}

func simple(text string, meta *MessageMeta) Message {
	m := RenderBI(NewFixture().BI, "Acme", "", "")
	m.Text, m.Meta = text, meta
	return m
}

func TestAPostedMessageCarriesItsMetadataAndIsFoundInTheHistory(t *testing.T) {
	p, fs := realPoster(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Second)
	want := MessageMeta{SubjectID: "s-1", Kind: KindChooser}

	if _, _, err := p.FindMessage(ctx, "C1", before, want); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := p.FindMessage(ctx, "C1", before, want); found {
		t.Fatal("found a message that was never posted")
	}
	if _, err := p.PostMessage(ctx, "C1", simple("other subject", &MessageMeta{SubjectID: "s-2", Kind: KindChooser})); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PostMessage(ctx, "C1", simple("other kind", &MessageMeta{SubjectID: "s-1", Kind: KindJudgment})); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PostMessage(ctx, "C1", simple("no metadata", nil)); err != nil {
		t.Fatal(err)
	}
	first, err := p.PostMessage(ctx, "C1", simple("ours", &want))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.PostMessage(ctx, "C1", simple("ours again, a duplicate", &want)); err != nil {
		t.Fatal(err)
	}

	ts, found, err := p.FindMessage(ctx, "C1", before, want)
	if err != nil || !found || ts != first {
		t.Fatalf("find = %q, %v, %v: want the earliest matching message %q and none of the look-alikes", ts, found, err, first)
	}
	if _, found, _ := p.FindMessage(ctx, "C2", before, want); found {
		t.Fatal("found a message in another channel")
	}
	if _, found, _ := p.FindMessage(ctx, "C1", time.Now().Add(time.Hour), want); found {
		t.Fatal("a message older than the window was found")
	}
	if got := fs.CallsOf("chat.postMessage"); len(got) != 5 || !strings.Contains(string(got[3].Metadata), `"event_type":"ghost_message"`) ||
		!strings.Contains(string(got[3].Metadata), `"subject_id":"s-1"`) || !strings.Contains(string(got[3].Metadata), `"kind":"chooser"`) {
		t.Fatalf("metadata on the wire: %s", got[3].Metadata)
	}
	if got := fs.CallsOf("chat.postMessage"); len(got[4].Metadata) == 0 || len(got[2].Metadata) != 0 {
		t.Fatal("only messages with a Meta carry metadata")
	}
}

func TestAnUpdateKeepsTheMetadataAndADeleteRemovesTheMessage(t *testing.T) {
	p, fs := realPoster(t)
	ctx := context.Background()
	meta := MessageMeta{SubjectID: "s-1", Kind: KindBI}
	ts, err := p.PostMessage(ctx, "C1", simple("v1", &meta))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdateMessage(ctx, "C1", ts, simple("v2", &meta)); err != nil {
		t.Fatal(err)
	}
	if got, found, _ := p.FindMessage(ctx, "C1", time.Now().Add(-time.Minute), meta); !found || got != ts {
		t.Fatalf("after an update the message is found as %q, %v", got, found)
	}
	if err := p.DeleteMessage(ctx, "C1", ts); err != nil {
		t.Fatal(err)
	}
	if v := fs.Visible("C1"); len(v) != 0 {
		t.Fatalf("a deleted message is still visible: %+v", v)
	}
	if _, found, _ := p.FindMessage(ctx, "C1", time.Now().Add(-time.Minute), meta); found {
		t.Fatal("a deleted message is still found")
	}
}

func TestHistoryAndDeleteErrorsAreReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer srv.Close()
	api, _ := NewSlackClients(Config{AppToken: "xapp-t", BotToken: "xoxb-t"}, srv.URL+"/")
	p := NewSlackPoster(api)
	if _, _, err := p.FindMessage(context.Background(), "C1", time.Now(), MessageMeta{SubjectID: "s", Kind: KindBI}); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("find: %v", err)
	}
	if err := p.DeleteMessage(context.Background(), "C1", "1.1"); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("delete: %v", err)
	}
}

func TestTheAuditLogRecordsTheHistoryReadAndTheDelete(t *testing.T) {
	var buf strings.Builder
	a := NewAuditPoster(stubPoster{}, &buf)
	if _, _, err := a.FindMessage(context.Background(), "C1", time.Now(), MessageMeta{SubjectID: "s", Kind: KindBI}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteMessage(context.Background(), "C1", "1.2"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, `"method":"conversations.history"`) || !strings.Contains(got, `"method":"chat.delete"`) || !strings.Contains(got, `"ts":"1.2"`) {
		t.Fatalf("audit = %s", got)
	}
}

// The message refs over HTTP: 404 is ErrNotFound, a second ts is a conflict that carries the recorded ref.
func TestCoreHTTPMessageRefs(t *testing.T) {
	refs := newMemRefs()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !routeRefs(w, r, refs) {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewCoreHTTP(srv.URL, Secret(testToken), nil)
	ctx := context.Background()
	const subject = "0e9e0000-0000-4000-8000-000000000a01"

	if _, err := c.GetRef(ctx, subject, KindChooser); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before reserving: %v", err)
	}
	ref, created, err := c.ReserveRef(ctx, subject, KindChooser, "C1")
	if err != nil || !created || ref.TS != "" || ref.Channel != "C1" || ref.ReservedAt.IsZero() {
		t.Fatalf("reserve = %+v %v %v", ref, created, err)
	}
	if again, created, err := c.ReserveRef(ctx, subject, KindChooser, "C2"); err != nil || created || again.Channel != "C1" {
		t.Fatalf("second reserve = %+v %v %v", again, created, err)
	}
	if got, err := c.RecordRefTS(ctx, subject, KindChooser, "1700000000.000101"); err != nil || got.TS != "1700000000.000101" {
		t.Fatalf("record = %+v %v", got, err)
	}
	_, err = c.RecordRefTS(ctx, subject, KindChooser, "1700000000.000999")
	var conflict *RefTSConflictError
	if !errors.As(err, &conflict) || conflict.Existing.TS != "1700000000.000101" || conflict.Existing.Channel != "C1" {
		t.Fatalf("a different ts: %v", err)
	}
	if got, err := c.GetRef(ctx, subject, KindChooser); err != nil || got.TS != "1700000000.000101" {
		t.Fatalf("get = %+v %v", got, err)
	}
}
