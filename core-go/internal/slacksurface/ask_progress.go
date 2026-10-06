package slacksurface

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Visible progress (owner request, 2026-10-06). While core works, the "Thinking…" message is updated in place with
// the short step lines core records for the turn; the final answer then replaces it. Nothing is ever posted as a new
// message for progress, so a channel's top level is never written.
const (
	defaultProgressEvery = 1500 * time.Millisecond
	progressShown        = 4 // the latest lines shown under the heading
)

func newTurnID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b) // crypto/rand does not fail on supported platforms
	return "t" + hex.EncodeToString(b)
}

// progressText is the working message: the heading and the latest step lines.
func progressText(heading string, lines []string) string {
	if len(lines) > progressShown {
		lines = lines[len(lines)-progressShown:]
	}
	var b strings.Builder
	b.WriteString(heading)
	for _, l := range lines {
		b.WriteString("\n• " + escapeMrkdwn(l))
	}
	return b.String()
}

// working runs work while updating the message at ts in place with core's progress for turnID. It returns when work
// has, after the last progress update, so nothing it writes can land after the caller's final update.
func working[T any](ctx context.Context, a *AskHandler, channel, ts, turnID, heading string, work func() (T, error)) (T, error) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.watch(ctx, channel, ts, turnID, heading, stop)
	}()
	v, err := work()
	close(stop)
	wg.Wait()
	return v, err
}

func (a *AskHandler) watch(ctx context.Context, channel, ts, turnID, heading string, stop <-chan struct{}) {
	tick := time.NewTicker(a.progressEvery)
	defer tick.Stop()
	shownLines := 0
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		p, err := a.core.Progress(ctx, turnID)
		if err != nil {
			a.log.DebugContext(ctx, "ask: could not read the progress", "error", err)
			continue
		}
		if len(p.Lines) == shownLines {
			continue
		}
		shownLines = len(p.Lines)
		a.update(ctx, channel, ts, progressText(heading, p.Lines))
	}
}
