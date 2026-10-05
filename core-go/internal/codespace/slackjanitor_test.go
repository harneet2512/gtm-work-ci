package codespace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

// fakeChannel is a Slack channel with a history, never the real one.
type fakeChannel struct {
	mu       sync.Mutex
	botID    string
	botApp   string
	pages    [][]ChannelMessage // history pages, newest first
	deleted  []string
	deleteAt map[string]error // ts -> error
	authErr  error
	histErr  error
	cursors  []string
}

func (f *fakeChannel) BotIdentity(context.Context) (string, string, error) {
	return f.botID, f.botApp, f.authErr
}

func (f *fakeChannel) History(_ context.Context, _ string, cursor string) ([]ChannelMessage, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors = append(f.cursors, cursor)
	if f.histErr != nil {
		return nil, "", f.histErr
	}
	i := 0
	if cursor != "" {
		i = int(cursor[0] - '0')
	}
	next := ""
	if i+1 < len(f.pages) {
		next = string(rune('0' + i + 1))
	}
	return f.pages[i], next, nil
}

func (f *fakeChannel) Delete(_ context.Context, _ string, ts string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.deleteAt[ts]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, ts)
	return nil
}

func TestClearDeletesOnlyTheBotsOwnMessagesAcrossPages(t *testing.T) {
	ch := &fakeChannel{botID: "UBOT", botApp: "BBOT", pages: [][]ChannelMessage{
		{{TS: "5.0", User: "UBOT"}, {TS: "4.0", User: "UHUMAN"}},
		{{TS: "3.0", User: "UBOT"}, {TS: "2.0", BotID: "BBOT", User: ""}, {TS: "1.0", User: "UOTHER"}},
	}}
	j := Janitor{API: ch, Channel: "C0DEMO"}
	n, err := j.Clear(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("Clear = %d, %v", n, err)
	}
	if got := strings.Join(ch.deleted, ","); got != "5.0,3.0,2.0" {
		t.Fatalf("deleted = %s: only what the bot itself posted, never a human's message", got)
	}
	if len(ch.cursors) != 2 {
		t.Fatalf("both history pages are read: %v", ch.cursors)
	}
}

func TestClearIsQuietOnAnEmptyChannelAndIdempotent(t *testing.T) {
	ch := &fakeChannel{botID: "UBOT", pages: [][]ChannelMessage{{}}}
	j := Janitor{API: ch, Channel: "C0DEMO"}
	for i := 0; i < 2; i++ {
		if n, err := j.Clear(context.Background()); err != nil || n != 0 {
			t.Fatalf("Clear = %d, %v", n, err)
		}
	}
}

func TestClearReportsEveryProblemInsteadOfLeavingStaleMessagesQuietly(t *testing.T) {
	ctx := context.Background()
	if _, err := (Janitor{API: &fakeChannel{authErr: errors.New("invalid_auth")}, Channel: "C"}).Clear(ctx); err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Fatalf("auth: %v", err)
	}
	if _, err := (Janitor{API: &fakeChannel{botID: "U", histErr: errors.New("missing_scope")}, Channel: "C"}).Clear(ctx); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("history: %v", err)
	}
	ch := &fakeChannel{botID: "U", pages: [][]ChannelMessage{{{TS: "1.0", User: "U"}, {TS: "2.0", User: "U"}}}, deleteAt: map[string]error{"1.0": errors.New("cant_delete_message")}}
	n, err := (Janitor{API: ch, Channel: "C"}).Clear(ctx)
	if err == nil || !strings.Contains(err.Error(), "cant_delete_message") || n != 1 {
		t.Fatalf("a failed delete is reported and the rest still goes: %d %v", n, err)
	}
	if _, err := (Janitor{API: ch}).Clear(ctx); err == nil || !strings.Contains(err.Error(), "SLACK_CHANNEL_ID") {
		t.Fatalf("no channel: %v", err)
	}
}

// slackServer is the Slack Web API in miniature, for the real client.
func slackServer(t *testing.T, history []map[string]any, deleteErr string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	var mu sync.Mutex
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v map[string]any) {
		v["ok"] = v["ok"] != false
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"user_id": "UBOT", "bot_id": "BBOT"})
	})
	mux.HandleFunc("/conversations.history", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		reply(w, map[string]any{"messages": history, "has_more": false})
	})
	mux.HandleFunc("/chat.delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		calls = append(calls, r.Form.Get("channel")+"/"+r.Form.Get("ts"))
		mu.Unlock()
		if deleteErr != "" {
			reply(w, map[string]any{"ok": false, "error": deleteErr})
			return
		}
		reply(w, map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestTheRealSlackClientListsAndDeletesOverTheWebAPI(t *testing.T) {
	srv, calls := slackServer(t, []map[string]any{
		{"ts": "10.0", "user": "UBOT", "text": "Message 2"}, {"ts": "9.0", "user": "UHUMAN", "text": "hi"}, {"ts": "8.0", "bot_id": "BBOT"},
	}, "")
	api := NewSlackChannelAPI("xoxb-fake-test-token", srv.URL+"/")
	j := Janitor{API: api, Channel: "C0DEMO"}
	n, err := j.Clear(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("Clear = %d, %v", n, err)
	}
	if got := strings.Join(*calls, ","); got != "C0DEMO/10.0,C0DEMO/8.0" {
		t.Fatalf("chat.delete calls = %s", got)
	}
}

func TestTheRealSlackClientSurfacesSlackErrors(t *testing.T) {
	srv, _ := slackServer(t, []map[string]any{{"ts": "10.0", "user": "UBOT"}}, "cant_delete_message")
	api := NewSlackChannelAPI("xoxb-fake-test-token", srv.URL+"/")
	if _, err := (Janitor{API: api, Channel: "C0DEMO"}).Clear(context.Background()); err == nil || !strings.Contains(err.Error(), "cant_delete_message") {
		t.Fatalf("err = %v", err)
	}
	bad := NewSlackChannelAPI("xoxb-fake-test-token", "http://127.0.0.1:1/")
	if _, err := (Janitor{API: bad, Channel: "C"}).Clear(context.Background()); err == nil {
		t.Fatal("an unreachable Slack must be an error")
	}
}

func TestResetAllClearsTheChannelAndStopsWhenThatFails(t *testing.T) {
	r := newRig(t)
	r.seeded()
	cleared := 0
	r.ops.Slack = clearerFunc(func(context.Context) (int, error) { cleared++; return 2, nil })
	var steps []string
	if err := r.ops.ResetAll(context.Background(), func(s string) { steps = append(steps, s) }); err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || !contains(steps, "Clearing earlier messages from #ghost-demo") {
		t.Fatalf("cleared=%d steps=%v", cleared, steps)
	}
	r2 := newRig(t)
	r2.seeded()
	r2.ops.Slack = clearerFunc(func(context.Context) (int, error) { return 0, errors.New("missing_scope") })
	if err := r2.ops.ResetAll(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("err = %v", err)
	}
	if len(r2.plat.events) != 0 {
		t.Fatalf("stale messages left in the channel must stop the reset before anything is restored: %v", r2.plat.events)
	}
}

type clearerFunc func(context.Context) (int, error)

func (f clearerFunc) Clear(ctx context.Context) (int, error) { return f(ctx) }

func TestTheJanitorIsWiredOnlyWhenSlackIsOn(t *testing.T) {
	r := newAssembleRig(t)
	on := Assemble(r.flow, r.options(slackOK))
	if _, ok := on.Ops.Slack.(Janitor); !ok || on.Boot.Ops.Slack == nil || on.Control.Ops.Slack == nil {
		t.Fatalf("a demo with Slack must clear the channel on reset: %T", on.Ops.Slack)
	}
	r2 := newAssembleRig(t)
	r2.flow.Cfg.Process = demorun.Merge(r2.flow.Cfg.Process, demorun.Env{"GHOST_DEMO_NO_SLACK": "1"})
	if off := Assemble(r2.flow, r2.options(slackOK)); off.Ops.Slack != nil {
		t.Fatal("with Slack off there is no channel to clear")
	}
}
