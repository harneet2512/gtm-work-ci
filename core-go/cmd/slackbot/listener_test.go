package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

func liveEnv(extra map[string]string) func(string) string {
	m := map[string]string{
		"SLACK_APP_TOKEN": "xapp-test", "SLACK_BOT_TOKEN": "xoxb-test", "SLACK_CHANNEL_ID": "C1",
		"CORE_URL": "http://127.0.0.1:1", "GHOST_API_TOKEN": "t",
	}
	for k, v := range extra {
		m[k] = v
	}
	return func(k string) string { return m[k] }
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestListenerRefusesToStartWithoutAnAllowlist(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(nil, liveEnv(nil), &out, &errOut)
	if code == 0 {
		t.Fatal("the listener must not start without SLACK_ALLOWED_USER_IDS or SLACK_ALLOW_ALL_USERS=1")
	}
}

func TestListenerStartsWithAnAllowlistAndReportsAFailedConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()
	for name, extra := range map[string]map[string]string{
		"allowlist": {"SLACK_ALLOWED_USER_IDS": "U1"},
		"allow all": {"SLACK_ALLOW_ALL_USERS": "1"},
	} {
		cfg, err := slacksurface.LoadConfig(liveEnv(extra))
		if err != nil {
			t.Fatal(err)
		}
		if err := runLive(context.Background(), cfg, quiet(), postArgs{}, srv.URL+"/"); err == nil {
			t.Fatalf("%s: a failed Socket Mode connection must be an error", name)
		}
	}
	cfg, _ := slacksurface.LoadConfig(liveEnv(nil))
	if err := runLive(context.Background(), cfg, quiet(), postArgs{}, srv.URL+"/"); err == nil || !strings.Contains(err.Error(), "SLACK_ALLOWED_USER_IDS") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlayPostsMessageOneBeforeMessageTwo(t *testing.T) {
	var mu sync.Mutex
	var texts []string
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "chat.postMessage") {
			_ = r.ParseForm()
			mu.Lock()
			texts = append(texts, r.Form.Get("text"))
			mu.Unlock()
		}
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1.1"}`))
	}))
	defer slackSrv.Close()
	fx := slacksurface.NewFixture()
	refs := fakecore.NewRefs(nil) // the surface_messages of the contract, so the Publisher is exactly-once
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/surface-messages/") {
			refs.ServeHTTP(w, r)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "business-intelligence/latest"):
			_ = json.NewEncoder(w).Encode(fx.BI)
		case strings.HasSuffix(r.URL.Path, "/strategies"):
			_ = json.NewEncoder(w).Encode(fx.Strategies)
		case strings.HasSuffix(r.URL.Path, "/graph"):
			_, _ = w.Write([]byte(`{"nodes":[],"edges":[]}`))
		case strings.HasPrefix(r.URL.Path, "/accounts/"):
			_, _ = w.Write([]byte(`{"id":"a","name":"Acme"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer coreSrv.Close()
	cfg := slacksurface.Config{AppToken: "xapp-t", BotToken: "xoxb-t", ChannelID: "C1", CoreURL: coreSrv.URL, APIToken: "t"}
	pa := postArgs{run: slacksurface.FixtureRunID, account: slacksurface.FixtureAccountID, message: "play"}
	if err := runLive(context.Background(), cfg, quiet(), pa, slackSrv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "changed") || !strings.Contains(texts[1], "Choose the next move") {
		t.Fatalf("play must post Message 1 then Message 2, got %q", texts)
	}
	for _, m := range []string{"bi", "chooser"} {
		pa.message = m
		if err := runLive(context.Background(), cfg, quiet(), pa, slackSrv.URL+"/"); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	pa.message, pa.episode = "judgment", slacksurface.FixtureEpisodeID
	if err := runLive(context.Background(), cfg, quiet(), pa, slackSrv.URL+"/"); err == nil {
		t.Fatal("core has no inference in this fake: posting the judgment must fail")
	}
}
