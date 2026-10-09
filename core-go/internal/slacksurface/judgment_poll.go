package slacksurface

import (
	"context"
	"errors"
	"time"
)

// startJudgmentPoll waits for core's judgment inference after a Send and posts Message 3 once.
// Core has no push yet (its outbox is not specified), so the listener polls with exponential
// backoff (pollBase doubling up to pollMax) for at most pollAttempts reads, one poll per episode.
// The post itself is guarded by the Publisher, so Message 3 is posted at most once per process.
//
// Until core's outbox exists, re-running `slackbot --post-run ... --message judgment` after the
// listener has already posted duplicates Message 3: the CLI and the listener do not share state.
func (h *Handler) startJudgmentPoll(runID, episodeID string) {
	h.mu.Lock()
	if h.polling[episodeID] {
		h.mu.Unlock()
		return
	}
	h.polling[episodeID] = true
	ctx := h.runCtx
	h.bg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.bg.Done()
		defer h.recoverPanic("judgment poll")
		defer func() {
			h.mu.Lock()
			delete(h.polling, episodeID)
			h.mu.Unlock()
		}()
		h.pollJudgment(ctx, runID, episodeID)
	}()
}

func (h *Handler) pollJudgment(ctx context.Context, runID, episodeID string) {
	wait := h.pollBase
	for attempt := 0; attempt < h.pollAttempts; attempt++ {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_, err := h.pub.PostJudgment(ctx, runID, episodeID)
		switch {
		case err == nil:
			return
		case errors.Is(err, ErrNotReady) || errors.Is(err, ErrNotFound):
		default:
			h.log.Error("could not post the judgment inference", "episode", episodeID, "error", err)
		}
		if wait *= 2; wait > h.pollMax {
			wait = h.pollMax
		}
	}
	h.log.Warn("gave up waiting for the judgment inference", "episode", episodeID, "attempts", h.pollAttempts)
}

// recoverPanic keeps a panic in a background goroutine from taking the listener down.
func (h *Handler) recoverPanic(what string) {
	if r := recover(); r != nil {
		h.log.Error("recovered from a panic", "in", what, "panic", r)
	}
}

// Wait blocks until background polls have ended (after the Serve context is cancelled).
func (h *Handler) Wait() { h.bg.Wait() }

func (h *Handler) setRunContext(ctx context.Context) {
	h.mu.Lock()
	h.runCtx = ctx
	h.mu.Unlock()
}
