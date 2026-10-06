package slacksurface

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// askWork bounds one question or confirmed action (core's worker loop is 60 s, a Play 55 s).
const askWork = 100 * time.Second

// AskHandler turns Slack DMs, @mentions and confirmation clicks into core API calls. It keeps no business state: the
// question goes to core as it came, the answer is shown where the question was asked, and a state-changing action is
// forwarded only after the asker pressed Run. The only memory is a retry guard and the set of users with a question
// in flight (one at a time per user).
type AskHandler struct {
	core    AskCore
	poster  AskPoster
	botUser string // Cliff's own member id: its messages are ignored and its mention is removed from questions
	botID   string // Cliff's bot id (events of other bots carry a different one)
	log     *slog.Logger
	now     func() time.Time

	mu       sync.Mutex
	inflight map[string]bool
	seen     map[string]time.Time
}

// NewAskHandler builds the handler. botUser is Cliff's Slack member id (auth.test).
func NewAskHandler(core AskCore, poster AskPoster, botUser, botID string, log *slog.Logger) *AskHandler {
	if log == nil {
		log = slog.Default()
	}
	return &AskHandler{core: core, poster: poster, botUser: botUser, botID: botID, log: log, now: time.Now,
		inflight: map[string]bool{}, seen: map[string]time.Time{}}
}

// WithAsk adds Ask Cliff to the Handler: confirmation clicks arrive as interactions and events go to HandleEvent.
func WithAsk(a *AskHandler) Option { return func(h *Handler) { h.ask = a } }

// question is one message to answer.
type question struct {
	channel, user, text string
	replyThread         string // thread to answer in; "" only for a DM message that is not in a thread
	kind                string
	eventTS             string
}

// HandleEvent routes an Events API payload. It returns the work to do after the envelope is acknowledged (nil when
// there is nothing to do), so the adapter never holds the acknowledgement while core thinks.
func (a *AskHandler) HandleEvent(ev slackevents.EventsAPIEvent) Deferred {
	if ev.Type != slackevents.CallbackEvent {
		return nil
	}
	var q question
	switch e := ev.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		if a.fromBot(e.User, e.BotID, "") {
			return nil
		}
		// Always a thread: the one on the mention if it is already in one, else a new one under the mention.
		thread := e.ThreadTimeStamp
		if thread == "" {
			thread = e.TimeStamp
		}
		q = question{channel: e.Channel, user: e.User, text: a.stripMention(e.Text), replyThread: thread, kind: AskChannelThread, eventTS: e.TimeStamp}
	case *slackevents.MessageEvent:
		if e.ChannelType != "im" || a.fromBot(e.User, e.BotID, e.SubType) {
			return nil
		}
		q = question{channel: e.Channel, user: e.User, text: a.stripMention(e.Text), replyThread: e.ThreadTimeStamp, kind: AskChannelDM, eventTS: e.TimeStamp}
	default:
		return nil
	}
	if !a.firstDelivery(q.channel + ":" + q.eventTS) {
		return nil
	}
	return func(ctx context.Context) { a.answer(ctx, q) }
}

// fromBot reports messages Cliff must not answer: its own, other bots', and message edits or deletions.
func (a *AskHandler) fromBot(user, botID, subType string) bool {
	return user == "" || user == a.botUser || botID != "" || (a.botID != "" && botID == a.botID) || subType != ""
}

func (a *AskHandler) stripMention(text string) string {
	if a.botUser != "" {
		text = strings.ReplaceAll(text, "<@"+a.botUser+">", "")
	}
	return strings.TrimSpace(text)
}

func (a *AskHandler) firstDelivery(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, at := range a.seen {
		if now.Sub(at) > dedupeTTL {
			delete(a.seen, k)
		}
	}
	if _, dup := a.seen[key]; dup {
		return false
	}
	a.seen[key] = now
	return true
}

// claim takes the user's single in-flight slot; false when they already have a question running.
func (a *AskHandler) claim(user string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight[user] {
		return false
	}
	a.inflight[user] = true
	return true
}

func (a *AskHandler) release(user string) {
	a.mu.Lock()
	delete(a.inflight, user)
	a.mu.Unlock()
}

func (a *AskHandler) answer(ctx context.Context, q question) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), askWork)
	defer cancel()
	if q.text == "" {
		a.reply(ctx, q.channel, q.replyThread, askHelp)
		return
	}
	if !a.claim(q.user) {
		a.tell(ctx, q, askBusy)
		return
	}
	defer a.release(q.user)
	ts, err := a.poster.PostReply(ctx, q.channel, q.replyThread, askThinking, askBlocks(askThinking))
	if err != nil {
		a.log.ErrorContext(ctx, "ask: could not post the thinking reply", "error", err)
		return
	}
	ref := q.channel + ":" + q.replyThread
	ans, err := a.core.Ask(ctx, AskRequest{Text: q.text, ChannelKind: q.kind, User: q.user, ThreadRef: ref})
	if err != nil {
		a.log.ErrorContext(ctx, "ask: core could not answer", "error", err)
		a.update(ctx, q.channel, ts, askErrorText(err))
		return
	}
	a.update(ctx, q.channel, ts, toMrkdwn(ans.AnswerMarkdown))
	if p := ans.ProposedAction; p != nil && p.RequiresConfirmation {
		blocks := askConfirmBlocks(p.Summary, confirmValue(p.Kind, q.replyThread, q.kind))
		if err := a.poster.PostEphemeralTo(ctx, q.channel, q.user, q.replyThread, "Confirm: "+p.Summary, blocks); err != nil {
			a.log.ErrorContext(ctx, "ask: could not show the confirmation", "error", err)
		}
	}
}

func askErrorText(err error) string {
	switch {
	case errors.Is(err, ErrAskUnavailable):
		return askUnavailable
	case errors.Is(err, ErrAskProvider):
		return askProviderDown
	}
	return askFailed
}

func (a *AskHandler) reply(ctx context.Context, channel, thread, mrkdwn string) {
	if _, err := a.poster.PostReply(ctx, channel, thread, plainFallback(mrkdwn), askBlocks(mrkdwn)); err != nil {
		a.log.ErrorContext(ctx, "ask: could not post a reply", "error", err)
	}
}

func (a *AskHandler) update(ctx context.Context, channel, ts, mrkdwn string) {
	if err := a.poster.UpdateReply(ctx, channel, ts, plainFallback(mrkdwn), askBlocks(mrkdwn)); err != nil {
		a.log.ErrorContext(ctx, "ask: could not update the reply", "error", err)
	}
}

func (a *AskHandler) tell(ctx context.Context, q question, text string) {
	if err := a.poster.PostEphemeralTo(ctx, q.channel, q.user, q.replyThread, text, askBlocks(text)); err != nil {
		a.log.ErrorContext(ctx, "ask: could not tell the user", "error", err)
	}
}

// owns reports whether an interaction is one of Ask Cliff's confirmation buttons.
func (a *AskHandler) owns(cb slack.InteractionCallback) bool {
	if cb.Type != slack.InteractionTypeBlockActions {
		return false
	}
	for _, act := range cb.ActionCallback.BlockActions {
		if act.ActionID == ActionAskRun || act.ActionID == ActionAskCancel {
			return true
		}
	}
	return false
}

// interaction handles Run and Cancel. Cancel changes nothing; Run forwards the action to core, which checks the
// presenter rule itself, and shows the outcome where the question was asked.
func (a *AskHandler) interaction(cb slack.InteractionCallback) Deferred {
	var act *slack.BlockAction
	for _, b := range cb.ActionCallback.BlockActions {
		if b.ActionID == ActionAskRun || b.ActionID == ActionAskCancel {
			act = b
			break
		}
	}
	channel, _ := where(cb)
	kind, thread, _, ok := parseConfirmValue(act.Value)
	if !ok || !a.firstDelivery("click:"+cb.ResponseURL+act.ActionID) {
		return nil
	}
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), askWork)
		defer cancel()
		a.dismiss(ctx, cb.ResponseURL)
		if act.ActionID == ActionAskCancel {
			a.tellUser(ctx, channel, cb.User.ID, thread, askCancelled)
			return
		}
		a.run(ctx, channel, thread, cb.User.ID, kind)
	}
}

func (a *AskHandler) tellUser(ctx context.Context, channel, user, thread, text string) {
	if err := a.poster.PostEphemeralTo(ctx, channel, user, thread, text, askBlocks(text)); err != nil {
		a.log.ErrorContext(ctx, "ask: could not tell the user", "error", err)
	}
}

// dismiss removes the ephemeral confirmation so its buttons cannot be pressed twice.
func (a *AskHandler) dismiss(ctx context.Context, responseURL string) {
	if responseURL == "" {
		return
	}
	msg := &slack.WebhookMessage{DeleteOriginal: true}
	if err := slack.PostWebhookContext(ctx, responseURL, msg); err != nil {
		a.log.WarnContext(ctx, "ask: could not remove the confirmation", "error", err)
	}
}

func (a *AskHandler) run(ctx context.Context, channel, thread, user, kind string) {
	ts, err := a.poster.PostReply(ctx, channel, thread, askRunning, askBlocks(askRunning))
	if err != nil {
		a.log.ErrorContext(ctx, "ask: could not post the progress reply", "error", err)
		return
	}
	res, err := a.core.RunAction(ctx, AskActionRequest{Kind: kind, User: user})
	switch {
	case err != nil:
		a.log.ErrorContext(ctx, "ask: the action failed", "kind", kind, "error", err)
		a.update(ctx, channel, ts, askActionFailed)
	default:
		a.update(ctx, channel, ts, toMrkdwn(res.MessageMarkdown))
	}
}
