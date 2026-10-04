package slacksurface

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

const workTimeout = 60 * time.Second

// NewSlackClients builds the Web API client (bot token, app-level token for the socket) and the
// Socket Mode client. apiURL is empty in production; tests point it at a fake Slack server.
func NewSlackClients(cfg Config, apiURL string) (*slack.Client, *socketmode.Client) {
	opts := []slack.Option{slack.OptionAppLevelToken(string(cfg.AppToken))}
	if apiURL != "" {
		opts = append(opts, slack.OptionAPIURL(apiURL))
	}
	api := slack.New(string(cfg.BotToken), opts...)
	return api, socketmode.New(api)
}

// Serve runs the Socket Mode connection until ctx ends. The event loop only reads envelopes: each
// interaction is handled in its own goroutine (acknowledged at once, or after one bounded core write
// for a modal submission), so a slow core or a busy run never blocks other envelopes.
func Serve(parent context.Context, smc *socketmode.Client, h *Handler, log *slog.Logger) error {
	// RunContext returns an error on a failed connect (bad token, network down) without ending the
	// event loop below; cancelling here makes Serve return instead of hanging with no connection.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	h.setRunContext(ctx)
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-smc.Events:
				if !ok {
					return
				}
				handleEvent(ctx, smc, h, log, &wg, evt)
			}
		}
	}()
	err := smc.RunContext(ctx)
	cancel()
	<-done
	wg.Wait()
	h.Wait()
	return err
}

func handleEvent(ctx context.Context, smc *socketmode.Client, h *Handler, log *slog.Logger, wg *sync.WaitGroup, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeInteractive:
		cb, ok := evt.Data.(slack.InteractionCallback)
		if !ok || evt.Request == nil {
			return
		}
		req := *evt.Request
		wg.Add(1)
		go func() {
			defer wg.Done()
			handleInteractive(ctx, smc, h, log, cb, req)
		}()
	case socketmode.EventTypeEventsAPI, socketmode.EventTypeSlashCommand:
		if evt.Request != nil { // not used by this app; ack so Slack does not retry
			_ = smc.Ack(*evt.Request)
		}
	case socketmode.EventTypeConnected:
		log.Info("slack socket connected")
	case socketmode.EventTypeConnectionError, socketmode.EventTypeInvalidAuth:
		log.Error("slack socket problem", "type", string(evt.Type))
	}
}

// handleInteractive handles one interaction. A panic is recovered and logged; the envelope is
// still acknowledged so Slack does not redeliver it forever.
func handleInteractive(ctx context.Context, smc *socketmode.Client, h *Handler, log *slog.Logger, cb slack.InteractionCallback, req socketmode.Request) {
	acked := false
	defer func() {
		if r := recover(); r != nil {
			log.Error("recovered from a panic while handling an interaction", "panic", r)
			if !acked {
				_ = smc.Ack(req)
			}
		}
	}()
	ack, work := h.HandleInteraction(ctx, cb)
	if err := smc.Ack(req, ack); err != nil {
		log.Error("could not acknowledge interaction", "error", err)
	}
	acked = true
	if work == nil {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workTimeout)
	defer cancel()
	work(wctx)
}
