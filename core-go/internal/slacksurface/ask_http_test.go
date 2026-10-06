package slacksurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func askServer(t *testing.T, status int, body string, seen *[]string) *AskHTTP {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(r.Body)
		if seen != nil {
			*seen = append(*seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+raw.String())
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewAskHTTP(srv.URL, Secret("tok-123"), nil)
}

func TestAskHTTPPostsTheQuestionAndDecodesTheAnswer(t *testing.T) {
	var seen []string
	c := askServer(t, 200, `{"answer_markdown":"hi","citations":[],"proposed_action":{"kind":"play_next","summary":"s","requires_confirmation":true}}`, &seen)
	ans, err := c.Ask(context.Background(), AskRequest{Text: "q", ChannelKind: "dm", User: "U1"})
	if err != nil || ans.AnswerMarkdown != "hi" || ans.ProposedAction == nil || ans.ProposedAction.Kind != "play_next" {
		t.Fatalf("answer %+v err %v", ans, err)
	}
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "POST /ask Bearer tok-123 ") || !strings.Contains(seen[0], `"channel_kind":"dm"`) {
		t.Errorf("request = %v", seen)
	}
	res, err := askServer(t, 200, `{"status":"done","message_markdown":"ok"}`, &seen).RunAction(context.Background(), AskActionRequest{Kind: "play_next", User: "U1"})
	if err != nil || res.Status != "done" || !strings.Contains(seen[1], "POST /ask/actions") {
		t.Errorf("action %+v err %v seen %v", res, err, seen)
	}
}

func TestAskHTTPMapsTheStatusesTheAdapterWordsDifferently(t *testing.T) {
	for status, want := range map[int]error{424: ErrAskProvider, 503: ErrAskUnavailable} {
		_, err := askServer(t, status, `{"error":{"code":"x","message":"y"}}`, nil).Ask(context.Background(), AskRequest{Text: "q", ChannelKind: "dm", User: "U"})
		if !errors.Is(err, want) {
			t.Errorf("%d -> %v, want %v", status, err, want)
		}
	}
	if _, err := askServer(t, 502, `{}`, nil).Ask(context.Background(), AskRequest{}); err == nil || errors.Is(err, ErrAskUnavailable) {
		t.Errorf("502 -> %v", err)
	}
}

func TestSlackAskPosterSendsThreadsAndEphemeralsToTheRightPlaces(t *testing.T) {
	var forms []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f := map[string]string{"method": strings.TrimPrefix(r.URL.Path, "/")}
		for _, k := range []string{"channel", "thread_ts", "ts", "user", "text"} {
			f[k] = r.Form.Get(k)
		}
		forms = append(forms, f)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "5.5", "channel": "C1", "message_ts": "5.5"})
	}))
	defer srv.Close()
	p := NewSlackAskPoster(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")))
	ctx := context.Background()
	ts, err := p.PostReply(ctx, "C1", "1.1", "hi", nil)
	if err != nil || ts != "5.5" {
		t.Fatal(ts, err)
	}
	if err := p.UpdateReply(ctx, "C1", "5.5", "new", nil); err != nil {
		t.Fatal(err)
	}
	if err := p.PostEphemeralTo(ctx, "C1", "U1", "1.1", "confirm", nil); err != nil {
		t.Fatal(err)
	}
	if forms[0]["method"] != "chat.postMessage" || forms[0]["thread_ts"] != "1.1" || forms[0]["channel"] != "C1" {
		t.Errorf("post = %v", forms[0])
	}
	if forms[1]["method"] != "chat.update" || forms[1]["ts"] != "5.5" {
		t.Errorf("update = %v", forms[1])
	}
	if forms[2]["method"] != "chat.postEphemeral" || forms[2]["user"] != "U1" || forms[2]["thread_ts"] != "1.1" {
		t.Errorf("ephemeral = %v", forms[2])
	}
	if err := p.PostEphemeralTo(ctx, "", "U1", "", "x", nil); err == nil {
		t.Error("an ephemeral with no channel must be refused")
	}
}
