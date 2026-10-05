package slacksurface

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Regression tests for the review of the first version of this package.

func TestServeReturnsWhenSocketModeCannotConnect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()
	cfg := Config{AppToken: "xapp-test", BotToken: "xoxb-test", ChannelID: "C"}
	api, smc := NewSlackClients(cfg, srv.URL+"/")
	h := NewHandler(newMemCore(), NewSlackPoster(api), "C", "", quietLog())
	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), smc, h, quietLog()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a failed connection must be reported")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve hung after the connection failed")
	}
}

func TestUnauthorizedUserCannotAct(t *testing.T) {
	core, poster := newMemCore(), &fakePoster{}
	h := NewHandler(core, poster, "C0TEST", "", quietLog(), WithAllowedUsers([]string{"U9"}))
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB)) // actionCB acts as U1
	if len(core.writes()) != 0 || len(poster.updates) != 0 || len(poster.ephemerals) != 1 {
		t.Fatalf("writes=%v updates=%d ephemerals=%v", core.writes(), len(poster.updates), poster.ephemerals)
	}
	cb := actionCB(ActionStrategyChoose, "T2", tgtB)
	cb.User.ID = "U9"
	runWork(t, h, cb)
	if len(core.writes()) != 1 {
		t.Fatal("an allowed user must be able to act")
	}
}

func TestSendConfirmStaysWithinSlackLimit(t *testing.T) {
	view := ArtView{Subject: strings.Repeat("S&", 200), To: []string{strings.Repeat("a&b", 100) + "@x.test", strings.Repeat("c", 200) + "@y.test"}}
	if n := len([]rune(confirmText(view))); n > maxConfirmText {
		t.Fatalf("confirm text is %d characters, limit %d", n, maxConfirmText)
	}
	long := `[{"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Send"},"confirm":` +
		`{"title":{"type":"plain_text","text":"t"},"text":{"type":"mrkdwn","text":"` + strings.Repeat("x", 301) + `"},` +
		`"confirm":{"type":"plain_text","text":"ok"},"deny":{"type":"plain_text","text":"no"}}}]}]`
	if err := ValidateBlocks([]byte(long), maxMessageBlocks); err == nil {
		t.Fatal("the validator must reject a confirm text over 300 characters")
	}
}

func TestViewFullOfferedWhenEscapingTruncatesTheBody(t *testing.T) {
	f := NewFixture()
	cands := f.Strategies.StrategySet.Candidates
	cands[1].FullActionArtifact.Body = strings.Repeat(">", 1500) // 1,500 characters, 6,000 once escaped
	f.Chosen.FinalArtifact = nil
	msg, err := RenderSelected(f.Strategies, f.Chosen, f.Directory, "", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := mustJSON(msg)
	if !strings.Contains(raw, "choose View full") || !strings.Contains(raw, `"text":"View full"`) {
		t.Fatal("a truncated body must always come with a View full button")
	}
	if err := ValidateMessage(msg); err != nil {
		t.Fatal(err)
	}
}

func TestEditModalFieldLimits(t *testing.T) {
	v := RenderEditModal(ArtView{To: []string{"a@x.test"}, Subject: strings.Repeat("s", maxEditSubject+1), Body: "b"}, Target{EpisodeID: "e"})
	if v.Submit != nil {
		t.Fatal("an over-long subject must not produce an editable input")
	}
	bad := `[{"type":"input","element":{"type":"plain_text_input","max_length":5,"initial_value":"123456"}}]`
	if err := ValidateBlocks([]byte(bad), maxModalBlocks); err == nil {
		t.Fatal("initial_value longer than max_length must be rejected")
	}
	ok := RenderEditModal(ArtView{To: []string{strings.Repeat("a", 600) + "@x.test"}, Subject: "s", Body: "b"}, Target{EpisodeID: "e"})
	if err := ValidateModal(ok); err != nil {
		t.Fatalf("long but valid recipients must fit the input: %v", err)
	}
}

func TestPublisherPostsOncePerObjectInOneProcess(t *testing.T) {
	core, poster := newMemCore(), &fakePoster{}
	pub := NewPublisher(core, poster, "C", "https://ghost.example.test")
	for i := 0; i < 3; i++ {
		if _, err := pub.PostBI(context.Background(), FixtureAccountID); err != nil {
			t.Fatal(err)
		}
	}
	if len(poster.posts) != 1 {
		t.Fatalf("posted %d times", len(poster.posts))
	}
}

func TestRunLocksAreReleased(t *testing.T) {
	h, _, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	h.mu.Lock()
	n := len(h.locks)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d locks leaked", n)
	}
}

// orderCore records how many modals were open when core was first read.
type orderCore struct {
	*memCore
	poster *fakePoster
	opened int
}

func (c *orderCore) GetStrategies(ctx context.Context, id string) (RunStrategies, error) {
	c.opened = len(c.poster.opened)
	return c.memCore.GetStrategies(ctx, id)
}

func TestModalIsOpenedBeforeCoreIsAsked(t *testing.T) {
	poster := &fakePoster{}
	core := &orderCore{memCore: newMemCore(), poster: poster}
	h := NewHandler(core, poster, "C", "", quietLog())
	runWork(t, h, actionCB(ActionStrategyViewFull, "T1", tgtB))
	if core.opened != 1 {
		t.Fatal("views.open must happen before any core call, or a slow core expires the trigger_id")
	}
}

func TestReadOnlyModalDoesNotWaitForAWriteInProgress(t *testing.T) {
	h, _, poster := newTestHandler()
	unlock := h.lockEpisode(tgtB.lockKey()) // a long Send holds the run
	done := make(chan struct{})
	go func() { runWork(t, h, actionCB(ActionStrategyViewFull, "T1", tgtB)); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a read-only modal blocked on the lock")
	}
	unlock()
	if len(poster.opened) != 1 {
		t.Fatal("modal not opened")
	}
}

func TestModalErrorWhenCoreCannotAnswer(t *testing.T) {
	h, core, poster := newTestHandler()
	core.fx.Strategies.StrategySet.Candidates = nil // the candidate is gone
	runWork(t, h, actionCB(ActionStrategyViewFull, "T1", tgtB))
	if len(poster.opened) != 1 || len(poster.views) != 1 || !strings.Contains(mustJSON(poster.views[0]), "could not load") || len(poster.ephemerals) != 1 {
		t.Fatalf("the loading modal must be replaced by an error: %+v", poster)
	}
}

func TestFailedEditSubmitKeepsTheModalOpenWithAnInlineError(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	core.failNext = errors.New("core down")
	values := editValues(core)
	values[inputBody] = "new body"
	ack := runWork(t, h, viewCB("V1", CallbackEditModal, metaB, values))
	if !strings.Contains(mustJSON(ack), "Your text is still here") || len(poster.updates) != 1 {
		t.Fatalf("ack=%s updates=%d", mustJSON(ack), len(poster.updates))
	}
	if ack = runWork(t, h, viewCB("V1", CallbackEditModal, metaB, values)); ack != nil {
		t.Fatalf("pressing Submit again must work: %s", mustJSON(ack))
	}
	if len(poster.updates) != 2 {
		t.Fatal("the retried edit must refresh the message")
	}
}

func TestRefusedSendShowsTheReasonInPlace(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	core.sendRefused = true
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	last := mustJSON(poster.updates[len(poster.updates)-1])
	if len(poster.posts) != 0 || !strings.Contains(last, "Send was refused: a blocking eval is unresolved") {
		t.Fatalf("posts=%d last=%s", len(poster.posts), last)
	}
}

func TestSlackRefreshFailureIsReportedAsRecorded(t *testing.T) {
	h, core, poster := newTestHandler()
	poster.updateErr = errors.New("slack down")
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	if len(core.writes()) != 1 || len(poster.ephemerals) != 1 || !strings.Contains(poster.ephemerals[0], "recorded in Ghost") {
		t.Fatalf("ephemerals=%v", poster.ephemerals)
	}
}

func TestConfigCoreURLAndAllowedUsers(t *testing.T) {
	m := goodEnv()
	m[EnvCoreURL] = "http://core.internal.example:8080"
	if _, err := LoadConfig(env(m)); err == nil || !strings.Contains(err.Error(), EnvCoreURL) || strings.Contains(err.Error(), "core.internal") {
		t.Fatalf("plain http to a remote core must be refused without echoing the URL: %v", err)
	}
	m[EnvCoreURL] = "https://core.internal.example"
	m[EnvAllowedUsers] = " U1, ,U2 "
	m[EnvWebURL] = "https://ghost.example.test"
	cfg, err := LoadConfig(env(m))
	if err != nil || len(cfg.AllowedUsers) != 2 || cfg.AllowedUsers[1] != "U2" || cfg.WebURL == "" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	cfg.CoreURL = "https://user:hunter2@core.example"
	if strings.Contains(cfg.String(), "hunter2") {
		t.Fatal("credentials in the core URL must not be logged")
	}
	m[EnvCoreURL] = "ftp://x"
	if _, err := LoadConfig(env(m)); err == nil {
		t.Fatal("non-http core URL accepted")
	}
	m[EnvCoreURL] = "http://127.0.0.1:8080"
	if _, err := LoadConfig(env(m)); err != nil {
		t.Fatalf("loopback http must be fine: %v", err)
	}
}
