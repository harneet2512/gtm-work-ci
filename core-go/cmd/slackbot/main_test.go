package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

func TestPostEpisodeAgainstFakeCoreAndSlack(t *testing.T) {
	var posted int
	var firstPost string
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "chat.postMessage") {
			if posted == 0 {
				body, _ := io.ReadAll(r.Body)
				firstPost = string(body)
			}
			posted++
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
	cfg := slacksurface.Config{AppToken: "xapp-t", BotToken: "xoxb-t", ChannelID: "C1", CoreURL: coreSrv.URL, APIToken: "t", WebURL: "https://ghost.example.test"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runLive(context.Background(), cfg, log, postArgs{run: slacksurface.FixtureRunID, account: slacksurface.FixtureAccountID, message: "play"}, slackSrv.URL+"/"); err != nil || posted != 2 {
		t.Fatalf("err=%v posted=%d, want M1 and M2", err, posted)
	}
	// Message 1 names its episode through the run's strategy set, so View trace is there from the first post.
	if !strings.Contains(firstPost, "ghost.bi.view_trace") || !strings.Contains(firstPost, "episodes") {
		t.Fatalf("M1 posted by --post-run lacks View trace: %s", firstPost)
	}
	if err := runLive(context.Background(), cfg, log, postArgs{run: "r", message: "nonsense"}, slackSrv.URL+"/"); err == nil {
		t.Fatal("unknown --message must fail")
	}
}

func TestDryRunPrintsBlockKitWithoutAnyEnvironment(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--dry-run"}, func(string) string { return "" }, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var previews []struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(out.Bytes(), &previews); err != nil {
		t.Fatal(err)
	}
	if len(previews) != 15 || previews[0].Name != "message1_business_intelligence_no_trace" || previews[len(previews)-2].Name != "modal_edit_interpretation" {
		t.Fatalf("previews = %+v", previews)
	}
}

func TestDryRunWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.json")
	var out, errOut bytes.Buffer
	if code := run([]string{"--dry-run", "--out", path}, func(string) string { return "" }, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Contains(b, []byte("message2_chooser")) {
		t.Fatalf("file not written: %v", err)
	}
	if code := run([]string{"--dry-run", "--out", filepath.Join(t.TempDir(), "no", "dir", "p.json")}, func(string) string { return "" }, &out, &errOut); code == 0 {
		t.Fatal("an unwritable --out must fail")
	}
}

func TestLiveModeRefusesToStartNamingVariablesOnly(t *testing.T) {
	var out, errOut bytes.Buffer
	env := map[string]string{"SLACK_APP_TOKEN": "xapp-NEVERPRINT", "SLACK_BOT_TOKEN": ""}
	code := run(nil, func(k string) string { return env[k] }, &out, &errOut)
	msg := errOut.String()
	if code == 0 || !strings.Contains(msg, "SLACK_BOT_TOKEN") || !strings.Contains(msg, "SLACK_CHANNEL_ID") {
		t.Fatalf("exit %d: %s", code, msg)
	}
	if strings.Contains(msg, "NEVERPRINT") {
		t.Fatal("the refusal message printed a token value")
	}
}

func TestBadFlagsAreRejected(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--nope"}, func(string) string { return "" }, &out, &errOut); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
