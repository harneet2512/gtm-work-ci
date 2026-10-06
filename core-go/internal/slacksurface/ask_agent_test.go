package slacksurface

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// Ask Cliff as an agent through the adapter (owner request, 2026-10-06): the conversation reference, in-place progress,
// confirmations that show the exact action, the allowed-users gate, and a task that continues after a Run.

func confirmationText(t *testing.T, m ephemeralMsg) string {
	t.Helper()
	var parts []string
	for _, b := range m.blocks {
		if s, ok := b.(*slack.SectionBlock); ok && s.Text != nil {
			parts = append(parts, s.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestTheConfirmationShowsTheActionThatWillRunNotTheModelsWords(t *testing.T) {
	// The model words a play proposal as something harmless; the kind decides what runs and what is shown.
	a, core, p := newAsk()
	core.answer = AskAnswer{AnswerMarkdown: "ok", ProposedAction: &AskProposedAction{Kind: "play_next", RequiresConfirmation: true,
		Summary: "Just show the status, nothing changes", Target: "MedTech Advances (event 13 of 13)"}}
	handle(t, a, dmEvent("U1", "do it", "1.1"))
	if len(p.ephemerals) != 1 {
		t.Fatalf("ephemerals = %+v", p.ephemerals)
	}
	text := confirmationText(t, p.ephemerals[0])
	if !strings.Contains(text, "Play the next event") || !strings.Contains(text, "MedTech Advances (event 13 of 13)") || strings.Contains(text, "status, nothing changes") {
		t.Errorf("confirmation = %q", text)
	}
	if strings.Contains(p.ephemerals[0].text, "nothing changes") {
		t.Errorf("the notification text carries the model's words: %q", p.ephemerals[0].text)
	}
	core.answer.ProposedAction = &AskProposedAction{Kind: "demo_status", RequiresConfirmation: true, Summary: "Play the next event", Target: "the demo case"}
	handle(t, a, dmEvent("U1", "status?", "1.2"))
	if text := confirmationText(t, p.ephemerals[1]); !strings.Contains(text, "replay status (read only)") || strings.Contains(text, "Play the next event") {
		t.Errorf("a status proposal must read as a status: %q", text)
	}
}

func TestAnActionKindTheAdapterDoesNotKnowIsNeverOffered(t *testing.T) {
	a, core, p := newAsk()
	for _, kind := range []string{"reset_demo", "send_email", ""} {
		core.answer = AskAnswer{AnswerMarkdown: "ok", ProposedAction: &AskProposedAction{Kind: kind, RequiresConfirmation: true, Summary: "Show status"}}
		handle(t, a, dmEvent("U1", "x "+kind, "2."+kind))
	}
	if len(p.ephemerals) != 0 {
		t.Errorf("an unknown action was offered: %+v", p.ephemerals)
	}
}

func TestEachQuestionCarriesItsConversationReferenceAndAProgressId(t *testing.T) {
	a, core, _ := newAsk()
	handle(t, a, dmEvent("U1", "what stage?", "1.1"))
	handle(t, a, mentionEvent("U1", "<@"+botUser+"> and EcoLite?", "5.1", "5.0"))
	if len(core.asks) != 2 || core.asks[0].ThreadRef != "D1:" || core.asks[1].ThreadRef != "C1:5.0" {
		t.Fatalf("asks = %+v", core.asks)
	}
	if core.asks[0].TurnID == "" || core.asks[0].TurnID == core.asks[1].TurnID {
		t.Errorf("each question needs its own progress id: %q %q", core.asks[0].TurnID, core.asks[1].TurnID)
	}
}

func TestTheThinkingMessageIsUpdatedInPlaceWithStepLinesThenTheAnswer(t *testing.T) {
	a, core, p := newAsk()
	a.progressEvery = 5 * time.Millisecond
	core.gate = make(chan struct{})
	core.lines = []string{"Reading MedTech's state as of Nov 9…", "Checking the D4 evals…"}
	done := make(chan struct{})
	go func() { handle(t, a, mentionEvent("U1", "<@"+botUser+"> what changed?", "7.1", "")); close(done) }()
	waitUntil(t, "a progress update", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.updates) > 0
	})
	close(core.gate)
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.posts) != 1 || p.posts[0].thread != "7.1" {
		t.Fatalf("progress must not post anything new: %+v", p.posts)
	}
	first, last := p.updates[0], p.updates[len(p.updates)-1]
	if !strings.HasPrefix(first.text, "Thinking…") || !strings.Contains(first.text, "• Reading MedTech's state as of Nov 9…") {
		t.Errorf("the first update = %q", first.text)
	}
	if first.ts != p.posts[0].ts || last.ts != p.posts[0].ts {
		t.Errorf("progress and the answer must update the thinking message in place: %+v", p.updates)
	}
	if !strings.Contains(last.text, "security review") || strings.Contains(last.text, "Thinking") {
		t.Errorf("the last write must be the answer: %q", last.text)
	}
}

func TestOnlyTheLatestStepLinesAreShown(t *testing.T) {
	got := progressText("Thinking…", []string{"one", "two", "three", "four", "five", "six <x>"})
	if strings.Contains(got, "one") || strings.Contains(got, "two") || !strings.Contains(got, "• six &lt;x&gt;") {
		t.Errorf("progress = %q", got)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestARunThatResumesATaskShowsTheOutcomeAndTheRestInPlaceThenAsksForTheNextConfirmation(t *testing.T) {
	a, core, p := newAsk()
	next := AskProposedAction{Kind: "demo_status", RequiresConfirmation: true, Target: "the demo case"}
	core.result = AskActionResult{Status: "done", MessageMarkdown: "Released event 13 of 13.",
		Continuation: &AskAnswer{AnswerMarkdown: "Two evals warned: D4 and S2.", ProposedAction: &next}}
	a.interaction(click("U7", ActionAskRun, confirmValue("play_next", "300.1", "thread"), ""))(context.Background())
	if len(p.posts) != 1 || len(p.updates) != 1 {
		t.Fatalf("posts %+v updates %+v", p.posts, p.updates)
	}
	if got := p.updates[0].text; !strings.Contains(got, "Released event 13 of 13.") || !strings.Contains(got, "Two evals warned: D4 and S2.") {
		t.Errorf("the outcome and the rest of the task belong in one message: %q", got)
	}
	if len(p.ephemerals) != 1 || !strings.Contains(confirmationText(t, p.ephemerals[0]), "replay status") || p.ephemerals[0].user != "U7" {
		t.Errorf("a second state-changing step needs its own confirmation, shown to the clicker: %+v", p.ephemerals)
	}
	if len(core.actions) != 1 || core.actions[0].ThreadRef != "C1:300.1" {
		t.Errorf("the click must name the conversation so core can resume the task: %+v", core.actions)
	}
}

func TestOnlyTheAllowedUsersMayAskAndOthersAreRefusedPolitely(t *testing.T) {
	core := &fakeAskCore{answer: AskAnswer{AnswerMarkdown: "ok"}}
	p := &fakeAskPoster{}
	a := NewAskHandler(core, p, botUser, "BCLIFF", nil, WithAskAllowedUsers([]string{"UOK", " UALSO "}))
	handle(t, a, dmEvent("UNOPE", "what stage?", "1.1"))
	if len(core.asks) != 0 || len(p.posts) != 0 {
		t.Fatalf("a refused user cost a model call or a post: %+v %+v", core.asks, p.posts)
	}
	if len(p.ephemerals) != 1 || p.ephemerals[0].user != "UNOPE" || p.ephemerals[0].text != askNotAllowed {
		t.Errorf("the refusal must be polite, private and short: %+v", p.ephemerals)
	}
	for i, u := range []string{"UOK", "UALSO"} {
		handle(t, a, dmEvent(u, "what stage?", "2."+string(rune('1'+i))))
	}
	if len(core.asks) != 2 {
		t.Errorf("allowed users must be answered: %+v", core.asks)
	}
	// the Run button is gated too
	core.result = AskActionResult{Status: "done", MessageMarkdown: "x"}
	a.interaction(click("UNOPE", ActionAskRun, confirmValue("play_next", "1.1", "thread"), ""))(context.Background())
	if len(core.actions) != 0 {
		t.Error("a refused user ran an action")
	}
}

func TestWithoutTheAllowListWhoeverReachesTheBotMayAsk(t *testing.T) {
	a, core, _ := newAsk()
	handle(t, a, dmEvent("UANYONE", "what stage?", "1.1"))
	if len(core.asks) != 1 {
		t.Error("with GHOST_ASK_ALLOWED_USERS unset, asking stays open as it was")
	}
}

func TestTheAllowListIsReadFromTheEnvironment(t *testing.T) {
	env := map[string]string{EnvAppToken: "xapp-1-x", EnvBotToken: "xoxb-x", EnvChannelID: "C1", EnvCoreURL: "http://127.0.0.1:8080",
		EnvAPIToken: strings.Repeat("a", 24), EnvAskAllowedUsers: " U1, U2 ,,U3 ", EnvAllowedUsers: "UP"}
	cfg, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.AskAllowedUsers, ",") != "U1,U2,U3" || strings.Join(cfg.AllowedUsers, ",") != "UP" {
		t.Errorf("ask %v presenters %v", cfg.AskAllowedUsers, cfg.AllowedUsers)
	}
}
