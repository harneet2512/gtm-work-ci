package slacksurface

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/slack-go/slack"
)

const (
	dedupeTTL     = 15 * time.Minute
	dedupeMaxKeys = 4096
	// ackBudget bounds core work done before a modal submission is acknowledged. Slack requires the
	// ack within three seconds; if core is slower the modal stays open with an inline error.
	ackBudget = 2500 * time.Millisecond
)

// Handler turns Slack interactions into core API calls and message updates. It keeps no business
// state: choose/edit/send/verdict all go to core, and every re-render is rebuilt from core's answer.
// The only memory it holds is a short-lived set of already-handled Slack deliveries (a retry guard)
// and per-episode locks that serialise concurrent clicks; core stays the authority on idempotency
// (a repeated action answers ErrConflict, which is treated as success).
type Handler struct {
	core    Core
	poster  Poster
	pub     *Publisher
	log     *slog.Logger
	now     func() time.Time
	allowed map[string]bool // empty: every channel member may act

	mu      sync.Mutex
	seen    map[string]time.Time
	locks   map[string]*episodeLock
	polling map[string]bool // episodes whose judgment is being waited for
	runCtx  context.Context // set by Serve; ends background polls on shutdown
	bg      sync.WaitGroup  // background polls

	// Judgment polling after a Send (core has no push yet): backoff pollBase doubling up to pollMax,
	// at most pollAttempts reads.
	pollBase, pollMax time.Duration
	pollAttempts      int
}

// episodeLock is a channel semaphore, so waiting for it can time out (a mutex cannot).
type episodeLock struct {
	sem  chan struct{}
	refs int
}

// Option configures a Handler.
type Option func(*Handler)

// WithAllowedUsers restricts Choose/Edit/Send/verdicts to these Slack member ids. Without it every
// member of the channel (including guests in a shared channel) can act.
func WithAllowedUsers(ids []string) Option {
	return func(h *Handler) {
		for _, id := range ids {
			if id != "" {
				h.allowed[id] = true
			}
		}
	}
}

// WithManifest names the replay manifest the Publisher's View trace link carries.
func WithManifest(manifestID string) Option {
	return func(h *Handler) { h.pub.WithManifest(manifestID) }
}

// NewHandler builds a Handler. channel is where the Publisher posts (judgment follow-up after Send).
func NewHandler(core Core, poster Poster, channel, webURL string, log *slog.Logger, opts ...Option) *Handler {
	if log == nil {
		log = slog.Default()
	}
	h := &Handler{
		core: core, poster: poster, pub: NewPublisher(core, poster, channel, webURL), log: log, now: time.Now,
		allowed: map[string]bool{}, seen: map[string]time.Time{}, locks: map[string]*episodeLock{},
		polling: map[string]bool{}, runCtx: context.Background(),
		pollBase: time.Second, pollMax: 8 * time.Second, pollAttempts: 15,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Publisher exposes the message publisher (used by the post command).
func (h *Handler) Publisher() *Publisher { return h.pub }

// Deferred is work to run after the Socket Mode envelope has been acknowledged.
type Deferred func(ctx context.Context)

// HandleInteraction validates a Slack interaction and returns the acknowledgement payload (nil for
// a plain ack, or a view-submission errors response) plus the work to do after acking. Slack
// requires the ack within three seconds, so block actions only do their core work in the Deferred
// part; modal submissions do one bounded core write before the ack so a failure shows inline.
func (h *Handler) HandleInteraction(ctx context.Context, cb slack.InteractionCallback) (ack any, work Deferred) {
	if !h.authorized(cb.User.ID) {
		h.log.Warn("interaction from a user who is not allowed", "user", cb.User.ID)
		ch, _ := where(cb)
		return nil, func(ctx context.Context) {
			h.tell(ctx, ch, cb.User.ID, "You are not allowed to act on Cliff messages.")
		}
	}
	switch cb.Type {
	case slack.InteractionTypeBlockActions:
		return nil, h.blockAction(cb)
	case slack.InteractionTypeViewSubmission:
		return h.viewSubmission(ctx, cb)
	default:
		return nil, nil
	}
}

func (h *Handler) authorized(userID string) bool {
	return len(h.allowed) == 0 || h.allowed[userID]
}

// firstDelivery reports whether key has not been handled yet and records it.
func (h *Handler) firstDelivery(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if len(h.seen) >= dedupeMaxKeys {
		for k, at := range h.seen {
			if now.Sub(at) > dedupeTTL {
				delete(h.seen, k)
			}
		}
	}
	if at, ok := h.seen[key]; ok && now.Sub(at) <= dedupeTTL {
		return false
	}
	h.seen[key] = now
	return true
}

// forget removes a key so that a redelivery of an attempt that failed is processed again.
func (h *Handler) forget(key string) {
	h.mu.Lock()
	delete(h.seen, key)
	h.mu.Unlock()
}

// acquire takes the lock for key, waiting at most wait (wait <= 0 waits as long as it takes). It
// returns false when the wait timed out. Locks are reference-counted and dropped when idle.
func (h *Handler) acquire(key string, wait time.Duration) (release func(), ok bool) {
	h.mu.Lock()
	l, found := h.locks[key]
	if !found {
		l = &episodeLock{sem: make(chan struct{}, 1)}
		h.locks[key] = l
	}
	l.refs++
	h.mu.Unlock()
	drop := func() {
		h.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(h.locks, key)
		}
		h.mu.Unlock()
	}
	if wait <= 0 {
		l.sem <- struct{}{}
	} else {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case l.sem <- struct{}{}:
		case <-timer.C:
			drop()
			return nil, false
		}
	}
	return func() { <-l.sem; drop() }, true
}

// lockEpisode waits for the lock without a timeout (background work and tests).
func (h *Handler) lockEpisode(key string) func() {
	release, _ := h.acquire(key, 0)
	return release
}

var errBusy = errors.New("another action on this run is still in progress")

// actorOf is the actor_label sent to core: "slack:<member id> (<display name>)". The member id is
// the immutable identity; the contract has no external-actor field yet, so it travels in the label
// and core cannot resolve actor_person_id from Slack until one exists.
func actorOf(u slack.User) string {
	label := "slack:" + u.ID
	if u.Name != "" {
		label += " (" + u.Name + ")"
	}
	return truncate(label, 200)
}

// where returns the channel and ts of the message a block action came from.
func where(cb slack.InteractionCallback) (channel, ts string) {
	channel, ts = cb.Container.ChannelID, cb.Container.MessageTs
	if channel == "" {
		channel = cb.Channel.ID
	}
	if ts == "" {
		ts = cb.Message.Timestamp
	}
	return channel, ts
}

// Errors with a specific message for the user.
var (
	errEditAfterSend = errors.New("the email was already sent, so the edit was not applied")
	errNotSent       = errors.New("core did not send the email")
	// errVerdictLocked: core refused a verdict because an earlier answer stands (no_learning, or a correction).
	errVerdictLocked = errors.New("an earlier answer on what to learn stands")
	// errUnknown: the call may or may not have been applied (timeout, dropped connection, 5xx).
	errUnknown = errors.New("the outcome is unknown")
)

// slackSideError marks a failure that happened after core had already applied the action.
type slackSideError struct{ error }

func (e slackSideError) Unwrap() error { return e.error }

// userMessage turns an error into text safe to show the acting user: Cliff speaks, and no internal detail
// (the action id, the error) reaches the person. what is only for the log.
func userMessage(_ string, err error) string {
	var side slackSideError
	switch {
	case errors.As(err, &side):
		return "Your action was recorded, but Slack could not refresh this message."
	case errors.Is(err, errEditAfterSend):
		return "This email was already sent, so your edit was not applied."
	case errors.Is(err, errVerdictLocked):
		return "An earlier answer on what to learn stands, so this one was not applied. The message shows what I recorded."
	case errors.Is(err, errNotSent):
		return "Cliff did not send this email. Check the message for the reason."
	case errors.Is(err, errUnknown):
		return "Status unknown: I did not get a confirmation. I refreshed the message with what I have; check it before trying again."
	case errors.Is(err, errBusy):
		return "Another action on this run is still in progress. Try again in a moment."
	case errors.Is(err, ErrNotFound):
		return "Cliff could not find that decision any more."
	}
	return "That did not go through. Check the message, then try again."
}

// fail logs the error and tells only the acting user, so no extra channel message appears.
func (h *Handler) fail(ctx context.Context, channel, userID, what string, err error) {
	h.log.ErrorContext(ctx, "slack action failed", "action", what, "error", err)
	h.tell(ctx, channel, userID, userMessage(what, err))
}

func (h *Handler) tell(ctx context.Context, channel, userID, text string) {
	if channel == "" || userID == "" {
		return
	}
	if err := h.poster.PostEphemeral(ctx, channel, userID, text); err != nil {
		h.log.ErrorContext(ctx, "could not tell the user", "error", err)
	}
}
