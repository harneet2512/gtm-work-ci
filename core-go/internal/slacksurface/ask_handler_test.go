package slacksurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Ask Cliff through the adapter with a fake core and a fake poster: no Slack, no model, no core.

const botUser = "UCLIFF"

type postedReply struct{ channel, thread, text, ts string }
type updatedReply struct{ channel, ts, text string }
type ephemeralMsg struct {
	channel, user, thread, text string
	blocks                      []slack.Block
}

type fakeAskPoster struct {
	mu         sync.Mutex
	posts      []postedReply
	updates    []updatedReply
	ephemerals []ephemeralMsg
	n          int
}

// shown is what a person sees: the text of the section blocks (the plain text is only the notification).
func shown(text string, blocks []slack.Block) string {
	var parts []string
	for _, b := range blocks {
		if s, ok := b.(*slack.SectionBlock); ok && s.Text != nil {
			parts = append(parts, s.Text.Text)
		}
	}
	if len(parts) == 0 {
		return text
	}
	return strings.Join(parts, "\n")
}

func (f *fakeAskPoster) PostReply(_ context.Context, channel, thread, text string, blocks []slack.Block) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	ts := fmt.Sprintf("900.%03d", f.n)
	f.posts = append(f.posts, postedReply{channel, thread, shown(text, blocks), ts})
	return ts, nil
}

func (f *fakeAskPoster) UpdateReply(_ context.Context, channel, ts, text string, blocks []slack.Block) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, updatedReply{channel, ts, shown(text, blocks)})
	return nil
}

func (f *fakeAskPoster) PostEphemeralTo(_ context.Context, channel, user, thread, text string, blocks []slack.Block) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ephemerals = append(f.ephemerals, ephemeralMsg{channel, user, thread, text, blocks})
	return nil
}

type fakeAskCore struct {
	mu      sync.Mutex
	asks    []AskRequest
	actions []AskActionRequest
	answer  AskAnswer
	err     error
	result  AskActionResult
	actErr  error
	gate    chan struct{} // when set, Ask waits on it
}

func (f *fakeAskCore) Ask(_ context.Context, r AskRequest) (AskAnswer, error) {
	f.mu.Lock()
	f.asks = append(f.asks, r)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return f.answer, f.err
}

func (f *fakeAskCore) RunAction(_ context.Context, r AskActionRequest) (AskActionResult, error) {
	f.mu.Lock()
	f.actions = append(f.actions, r)
	f.mu.Unlock()
	return f.result, f.actErr
}

func newAsk() (*AskHandler, *fakeAskCore, *fakeAskPoster) {
	core := &fakeAskCore{answer: AskAnswer{AnswerMarkdown: "**MedTech** asked for a security review.\n\nOpen in gtm_ai: [Episode](http://web.test/episodes/e1)"}}
	poster := &fakeAskPoster{}
	return NewAskHandler(core, poster, botUser, "BCLIFF", nil), core, poster
}

func dmEvent(user, text, ts string) slackevents.EventsAPIEvent {
	return slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{
		Type: "message", Data: &slackevents.MessageEvent{User: user, Text: text, TimeStamp: ts, Channel: "D1", ChannelType: "im"}}}
}

func mentionEvent(user, text, ts, thread string) slackevents.EventsAPIEvent {
	return slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{
		Type: "app_mention", Data: &slackevents.AppMentionEvent{User: user, Text: text, TimeStamp: ts, ThreadTimeStamp: thread, Channel: "C1"}}}
}

func handle(t *testing.T, a *AskHandler, ev slackevents.EventsAPIEvent) bool {
	t.Helper()
	work := a.HandleEvent(ev)
	if work == nil {
		return false
	}
	work(context.Background())
	return true
}

func TestADMIsAnsweredInTheDMWithAThinkingReplyUpdatedInPlace(t *testing.T) {
	a, core, p := newAsk()
	if !handle(t, a, dmEvent("U1", "What changed at MedTech on Nov 9?", "100.1")) {
		t.Fatal("a DM was not handled")
	}
	if len(p.posts) != 1 || p.posts[0].channel != "D1" || p.posts[0].thread != "" || p.posts[0].text != "Thinking…" {
		t.Fatalf("posts = %+v", p.posts)
	}
	if len(p.updates) != 1 || p.updates[0].ts != p.posts[0].ts || p.updates[0].channel != "D1" {
		t.Fatalf("the thinking reply must be updated in place: %+v", p.updates)
	}
	if got := p.updates[0].text; !strings.Contains(got, "*MedTech* asked") || !strings.Contains(got, "<http://web.test/episodes/e1|Episode>") {
		t.Errorf("answer text = %q", got)
	}
	if len(core.asks) != 1 || core.asks[0].ChannelKind != "dm" || core.asks[0].User != "U1" || core.asks[0].Text != "What changed at MedTech on Nov 9?" {
		t.Errorf("core got %+v", core.asks)
	}
}

func TestAMentionIsAnsweredInAThreadUnderItAndNeverAtTheTopLevel(t *testing.T) {
	a, core, p := newAsk()
	handle(t, a, mentionEvent("U1", "<@"+botUser+"> why did you recommend option A?", "200.5", ""))
	if len(p.posts) != 1 || p.posts[0].channel != "C1" || p.posts[0].thread != "200.5" {
		t.Fatalf("posts = %+v", p.posts)
	}
	if core.asks[0].Text != "why did you recommend option A?" || core.asks[0].ChannelKind != "thread" {
		t.Errorf("the mention must be removed from the question: %+v", core.asks[0])
	}
	// A mention inside an existing thread answers in that thread.
	handle(t, a, mentionEvent("U1", "<@"+botUser+"> and B?", "200.9", "200.1"))
	if p.posts[1].thread != "200.1" {
		t.Errorf("thread = %q", p.posts[1].thread)
	}
	for _, post := range p.posts {
		if post.channel == "C1" && post.thread == "" {
			t.Errorf("a top-level message in the channel: %+v", post)
		}
	}
}

func TestAProposedActionPostsAnEphemeralConfirmationAndRunsNothingYet(t *testing.T) {
	a, core, p := newAsk()
	core.answer = AskAnswer{AnswerMarkdown: "I can play the next event.", ProposedAction: &AskProposedAction{Kind: "play_next", Summary: "Release the next event", RequiresConfirmation: true}}
	handle(t, a, mentionEvent("U1", "<@"+botUser+"> Play the next event", "300.1", ""))
	if len(core.actions) != 0 {
		t.Fatal("the action ran before a human confirmed")
	}
	if len(p.ephemerals) != 1 || p.ephemerals[0].user != "U1" || p.ephemerals[0].thread != "300.1" {
		t.Fatalf("ephemerals = %+v", p.ephemerals)
	}
	var ids []string
	for _, b := range p.ephemerals[0].blocks {
		if ab, ok := b.(*slack.ActionBlock); ok {
			for _, el := range ab.Elements.ElementSet {
				ids = append(ids, el.(*slack.ButtonBlockElement).ActionID)
			}
		}
	}
	if strings.Join(ids, ",") != ActionAskRun+","+ActionAskCancel {
		t.Errorf("buttons = %v", ids)
	}
}

func click(user, action, value, responseURL string) slack.InteractionCallback {
	return slack.InteractionCallback{Type: slack.InteractionTypeBlockActions, User: slack.User{ID: user}, ResponseURL: responseURL,
		Container:      slack.Container{ChannelID: "C1"},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: action, Value: value}}}}
}

func TestRunForwardsTheActionAsTheClickerAndShowsTheOutcomeInTheThread(t *testing.T) {
	a, core, p := newAsk()
	core.result = AskActionResult{Status: "done", MessageMarkdown: "Released event 2 of 3."}
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		deleted = m["delete_original"] == true
	}))
	defer srv.Close()
	cb := click("U7", ActionAskRun, confirmValue("play_next", "300.1", "thread"), srv.URL)
	work := a.interaction(cb)
	if work == nil {
		t.Fatal("Run was not handled")
	}
	work(context.Background())
	if len(core.actions) != 1 || core.actions[0] != (AskActionRequest{Kind: "play_next", User: "U7"}) {
		t.Fatalf("actions = %+v", core.actions)
	}
	if len(p.posts) != 1 || p.posts[0].thread != "300.1" || len(p.updates) != 1 || p.updates[0].text != "Released event 2 of 3." {
		t.Errorf("posts %+v updates %+v", p.posts, p.updates)
	}
	if !deleted {
		t.Error("the confirmation was not removed, so its buttons could be pressed twice")
	}
	if a.interaction(cb) != nil {
		t.Error("a second click on the same confirmation must be ignored")
	}
}

func TestCancelChangesNothing(t *testing.T) {
	a, core, p := newAsk()
	a.interaction(click("U7", ActionAskCancel, confirmValue("play_next", "1.1", "dm"), ""))(context.Background())
	if len(core.actions) != 0 || len(p.ephemerals) != 1 || p.ephemerals[0].text != askCancelled {
		t.Errorf("actions %+v ephemerals %+v", core.actions, p.ephemerals)
	}
}

func TestAnActionFailureIsNeverReportedAsSuccess(t *testing.T) {
	a, core, p := newAsk()
	core.actErr = errors.New("boom internal detail")
	a.interaction(click("U1", ActionAskRun, confirmValue("play_next", "1.1", "thread"), ""))(context.Background())
	if len(p.updates) != 1 || p.updates[0].text != askActionFailed || strings.Contains(p.updates[0].text, "boom") {
		t.Errorf("updates = %+v", p.updates)
	}
}

func TestCliffsOwnAndOtherBotsMessagesAreIgnored(t *testing.T) {
	a, core, p := newAsk()
	own := dmEvent(botUser, "my own words", "1.1")
	otherBot := dmEvent("UOTHERBOT", "from a bot", "1.2")
	otherBot.InnerEvent.Data.(*slackevents.MessageEvent).BotID = "BOTHER"
	edited := dmEvent("U1", "edited", "1.3")
	edited.InnerEvent.Data.(*slackevents.MessageEvent).SubType = "message_changed"
	ownMention := mentionEvent(botUser, "<@"+botUser+"> echo", "1.4", "")
	botMention := mentionEvent("UB", "<@"+botUser+"> hi", "1.5", "")
	botMention.InnerEvent.Data.(*slackevents.AppMentionEvent).BotID = "BOTHER"
	channelMsg := dmEvent("U1", "hello channel", "1.6")
	channelMsg.InnerEvent.Data.(*slackevents.MessageEvent).ChannelType = "channel"
	for name, ev := range map[string]slackevents.EventsAPIEvent{"own": own, "other bot": otherBot, "edit": edited, "own mention": ownMention,
		"bot mention": botMention, "channel message": channelMsg} {
		if handle(t, a, ev) {
			t.Errorf("%s was handled", name)
		}
	}
	if len(core.asks) != 0 || len(p.posts) != 0 {
		t.Error("something was asked or posted")
	}
}

func TestOnlyOneQuestionPerUserIsInFlight(t *testing.T) {
	a, core, p := newAsk()
	core.gate = make(chan struct{})
	done := make(chan struct{})
	go func() { handle(t, a, dmEvent("U1", "first", "1.1")); close(done) }()
	for i := 0; ; i++ {
		core.mu.Lock()
		n := len(core.asks)
		core.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
		if i > 3000 {
			t.Fatal("the first question never reached core")
		}
	}
	handle(t, a, dmEvent("U1", "second", "1.2"))
	if len(p.ephemerals) != 1 || p.ephemerals[0].text != askBusy {
		t.Errorf("the second question was not turned away: %+v", p.ephemerals)
	}
	other := make(chan struct{})
	go func() { handle(t, a, dmEvent("U2", "another user", "1.3")); close(other) }() // a different user is not blocked
	close(core.gate)
	<-done
	<-other
	handle(t, a, dmEvent("U1", "third", "1.4"))
	if len(core.asks) != 3 || core.asks[2].Text != "third" { // the turned-away second question never reached core
		t.Errorf("after the first finished the user can ask again: %+v", core.asks)
	}
}

func TestADuplicateDeliveryIsIgnored(t *testing.T) {
	a, core, _ := newAsk()
	handle(t, a, dmEvent("U1", "q", "5.5"))
	if handle(t, a, dmEvent("U1", "q", "5.5")) || len(core.asks) != 1 {
		t.Error("the redelivered event was answered twice")
	}
}

func TestAMentionWithNoQuestionGetsHelpAndCostsNoModelCall(t *testing.T) {
	a, core, p := newAsk()
	handle(t, a, mentionEvent("U1", "<@"+botUser+">", "6.1", ""))
	if len(core.asks) != 0 || len(p.posts) != 1 || p.posts[0].thread != "6.1" || !strings.Contains(p.posts[0].text, "Ask me about") {
		t.Errorf("posts %+v asks %+v", p.posts, core.asks)
	}
}

func TestCoreFailuresBecomeFriendlySentencesWithNoInternals(t *testing.T) {
	cases := map[error]string{errors.New("dial tcp 127.0.0.1:8080: refused"): askFailed, ErrAskUnavailable: askUnavailable, ErrAskProvider: askProviderDown}
	for err, want := range cases {
		a, core, p := newAsk()
		core.err = err
		handle(t, a, dmEvent("U1", "q", "7.1"))
		if len(p.updates) != 1 || p.updates[0].text != want || strings.Contains(p.updates[0].text, "dial") {
			t.Errorf("%v -> %+v", err, p.updates)
		}
	}
}

func TestOnlyAllowedUsersMayPressRunButAnyoneMayAsk(t *testing.T) {
	a, core, _ := newAsk()
	h := NewHandler(newMemCore(), &fakePoster{}, "C1", "http://web.test", nil, WithAsk(a), WithAllowedUsers([]string{"UOK"}))
	cb := click("UNOPE", ActionAskRun, confirmValue("play_next", "1.1", "thread"), "")
	if _, work := h.HandleInteraction(context.Background(), cb); work != nil {
		work(context.Background())
	}
	if len(core.actions) != 0 {
		t.Error("a user outside the allowlist ran an action")
	}
	cb = click("UOK", ActionAskRun, confirmValue("play_next", "1.1", "thread"), "")
	_, work := h.HandleInteraction(context.Background(), cb)
	work(context.Background())
	if len(core.actions) != 1 {
		t.Errorf("an allowed user's Run was not routed: %+v", core.actions)
	}
	if h.HandleEvent(dmEvent("UNOPE", "q", "9.1")) == nil {
		t.Error("asking a question is read-only and open to every member")
	}
}

func TestAHandlerWithoutAskIgnoresEvents(t *testing.T) {
	h := NewHandler(newMemCore(), &fakePoster{}, "C1", "", nil)
	if h.HandleEvent(dmEvent("U1", "q", "1.1")) != nil {
		t.Error("events must be ignored when Ask Cliff is off")
	}
}

func TestMrkdwnConversionKeepsLinksAndEscapesControlCharacters(t *testing.T) {
	got := toMrkdwn("**Bold** <script> & [A & B](http://web.test/x?a=1&b=2) and [Two](http://web.test/y)")
	want := "*Bold* &lt;script&gt; &amp; <http://web.test/x?a=1&b=2|A &amp; B> and <http://web.test/y|Two>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if toMrkdwn("javascript:alert(1) [x](javascript:alert(1))") != "javascript:alert(1) [x](javascript:alert(1))" {
		t.Error("a non-http link must stay inert text")
	}
}

func TestLongAnswersAreSplitIntoSectionsWithinSlacksLimit(t *testing.T) {
	long := strings.Repeat("line of text that goes on\n", 400)
	blocks := askBlocks(long)
	if len(blocks) < 2 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	for _, b := range blocks {
		if n := len([]rune(b.(*slack.SectionBlock).Text.Text)); n > 3000 {
			t.Errorf("a section has %d characters", n)
		}
	}
	if len(askBlocks(strings.Repeat("x", askSectionLimit*60))) > askMaxBlocks {
		t.Error("too many blocks")
	}
}

func TestConfirmValuesRoundTripAndRefuseGarbage(t *testing.T) {
	k, th, ck, ok := parseConfirmValue(confirmValue("play_next", "12.3", "thread"))
	if !ok || k != "play_next" || th != "12.3" || ck != "thread" {
		t.Errorf("round trip: %v %v %v %v", k, th, ck, ok)
	}
	for _, bad := range []string{"", "play_next", "|1|2", "a|b", "play_next||thread"} {
		if _, _, _, ok := parseConfirmValue(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	a, core, _ := newAsk()
	if a.interaction(click("U1", ActionAskRun, "garbage", "")) != nil || len(core.actions) != 0 {
		t.Error("a malformed button value must be ignored")
	}
}
