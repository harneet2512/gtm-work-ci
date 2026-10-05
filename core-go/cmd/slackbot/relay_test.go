package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slackfake"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// outboxCore is a core that has one strategy_set.published event until it is acknowledged.
func outboxCore(t *testing.T, acked *atomic.Int64) *httptest.Server {
	t.Helper()
	fx := slacksurface.NewFixture()
	refs := fakecore.NewRefs(nil) // the surface_messages of the contract, so the relayed post is exactly-once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/surface-messages/") {
			refs.ServeHTTP(w, r)
			return
		}
		switch {
		case r.URL.Path == "/outbox/events" && acked.Load() == 0:
			_, _ = w.Write([]byte(`{"items":[{"id":3,"topic":"strategy_set.published","agent_run_id":"` + slacksurface.FixtureRunID +
				`","strategy_set_id":"s","decision_episode_id":"e","account_id":"` + slacksurface.FixtureAccountID + `"}]}`))
		case r.URL.Path == "/outbox/events":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/outbox/events/3/ack":
			acked.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/strategies"):
			_ = json.NewEncoder(w).Encode(fx.Strategies)
		case strings.HasSuffix(r.URL.Path, "/graph"):
			_, _ = w.Write([]byte(`{"nodes":[],"edges":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func relayRig(t *testing.T, core *httptest.Server, off bool) (slacksurface.Config, *slacksurface.CoreHTTP, *slacksurface.Handler, *slackfake.Server) {
	t.Helper()
	fs := slackfake.New()
	t.Cleanup(fs.Close)
	cfg := slacksurface.Config{AppToken: "xapp-t", BotToken: "xoxb-t", ChannelID: "C1", CoreURL: core.URL, APIToken: "t",
		RelayOff: off, RelayPoll: 10 * time.Millisecond}
	coreHTTP := slacksurface.NewCoreHTTP(core.URL, cfg.APIToken, nil)
	api, _ := slacksurface.NewSlackClients(cfg, fs.APIURL())
	h := slacksurface.NewHandler(coreHTTP, slacksurface.NewSlackPoster(api), cfg.ChannelID, "", quiet())
	return cfg, coreHTTP, h, fs
}

func TestTheListenerPostsMessageTwoWhenCoreAnnouncesAPublishedSet(t *testing.T) {
	var acked atomic.Int64
	cfg, core, h, fs := relayRig(t, outboxCore(t, &acked), false)
	stop, err := startRelay(context.Background(), cfg, core, h, quiet())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	calls, err := fs.WaitCalls(ctx, "chat.postMessage", 1)
	if err != nil {
		t.Fatalf("no message reached Slack: %v", err)
	}
	// the acknowledgement follows the post; stopping before it arrives would cancel it (a legitimate redelivery, not what is tested here)
	for deadline := time.Now().Add(10 * time.Second); acked.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	if !strings.Contains(calls[0].Text, "I see 3 reasonable paths.") {
		t.Fatalf("posted %q", calls[0].Text)
	}
	if acked.Load() != 1 || len(fs.CallsOf("chat.postMessage")) != 1 {
		t.Fatalf("acked %d times, %d messages: the event must be acknowledged once and posted once", acked.Load(), len(fs.CallsOf("chat.postMessage")))
	}
}

func TestSlackOutboxOffPostsNothingByItself(t *testing.T) {
	var acked atomic.Int64
	cfg, core, h, fs := relayRig(t, outboxCore(t, &acked), true)
	stop, err := startRelay(context.Background(), cfg, core, h, quiet())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	stop()
	if n := len(fs.Calls()); n != 0 || acked.Load() != 0 {
		t.Fatalf("with SLACK_OUTBOX=off %d Slack calls and %d acks happened", n, acked.Load())
	}
}
