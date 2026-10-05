package slacksurface

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

// Regression tests for the second review (authorization, locks, polling, actor id, unknown outcomes).

func TestListenerFailsClosedWithoutAnAllowlist(t *testing.T) {
	cfg, err := LoadConfig(env(goodEnv()))
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.RequireAuthorization()
	if err == nil || !strings.Contains(err.Error(), EnvAllowedUsers) || !strings.Contains(err.Error(), EnvAllowAll) {
		t.Fatalf("an empty allowlist must refuse to start and name both variables: %v", err)
	}
	m := goodEnv()
	m[EnvAllowedUsers] = "U1"
	if cfg, _ = LoadConfig(env(m)); cfg.RequireAuthorization() != nil {
		t.Fatal("an allowlist must be enough")
	}
	m = goodEnv()
	m[EnvAllowAll] = "1"
	if cfg, _ = LoadConfig(env(m)); cfg.RequireAuthorization() != nil {
		t.Fatal("the explicit opt-out must be enough")
	}
	m[EnvAllowAll] = "true" // only exactly 1 counts
	if cfg, _ = LoadConfig(env(m)); cfg.RequireAuthorization() == nil {
		t.Fatal("anything but 1 must not open the listener to everyone")
	}
}

func TestAcquireTimesOutInsteadOfBlocking(t *testing.T) {
	h, _, _ := newTestHandler()
	release, ok := h.acquire("k", time.Second)
	if !ok {
		t.Fatal("free lock refused")
	}
	start := time.Now()
	if _, ok := h.acquire("k", 50*time.Millisecond); ok || time.Since(start) > time.Second {
		t.Fatal("a held lock must time out promptly")
	}
	release()
	if r, ok := h.acquire("k", 50*time.Millisecond); !ok {
		t.Fatal("released lock must be available")
	} else {
		r()
	}
	h.mu.Lock()
	n := len(h.locks)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d locks leaked after timeouts", n)
	}
}

func TestBusyRunAnswersTheModalInlineWithinTheBudget(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	release := h.lockEpisode(tgtB.lockKey()) // a long Send holds the run
	defer release()
	vals := editValues(core)
	vals[inputBody] = "changed while busy"
	start := time.Now()
	ack, work := h.HandleInteraction(context.Background(), viewCB("V1", CallbackEditModal, metaB, vals))
	if time.Since(start) > ackBudget || work != nil || !strings.Contains(mustJSON(ack), "still in progress") {
		t.Fatalf("busy submit must answer inline within %v: %s", ackBudget, mustJSON(ack))
	}
}

func TestActorLabelCarriesTheImmutableMemberID(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	if w := core.writes()[0]; !strings.Contains(w, `"actor_label":"slack:U1 (alex)"`) {
		t.Fatalf("write = %s", w)
	}
	long := strings.Repeat("n", 300)
	if got := actorOf(slackUser("U9", long)); len([]rune(got)) > 200 || !strings.HasPrefix(got, "slack:U9") {
		t.Fatalf("label = %q", got)
	}
	if actorOf(slackUser("U9", "")) != "slack:U9" {
		t.Fatal("no display name")
	}
}

func TestUnknownOutcomeRefreshesAndNeverSaysNothingChanged(t *testing.T) {
	for _, action := range []string{ActionStrategyChoose, ActionSelectedSend} {
		h, core, poster := newTestHandler()
		if action == ActionSelectedSend {
			runWork(t, h, actionCB(ActionStrategyChoose, "T0", tgtB))
		}
		before := len(poster.updates)
		core.failNext = &AmbiguousError{Err: errors.New("timeout")}
		runWork(t, h, actionCB(action, "T1", Target{RunID: FixtureRunID, CandidateID: fixtureCandB}))
		if len(poster.ephemerals) != 1 || !strings.Contains(poster.ephemerals[0], "Status unknown") ||
			strings.Contains(poster.ephemerals[0], "Nothing was changed") {
			t.Fatalf("%s: ephemerals = %v", action, poster.ephemerals)
		}
		if action == ActionSelectedSend && len(poster.updates) != before+1 {
			t.Fatalf("the message must be refreshed from core's decision (updates %d -> %d)", before, len(poster.updates))
		}
	}
}

func TestGenericFailureTextMakesNoClaimAboutState(t *testing.T) {
	if msg := userMessage("x", errors.New("boom")); strings.Contains(msg, "Nothing was changed") {
		t.Fatalf("msg = %q", msg)
	}
}

func TestStatus5xxAndNetworkErrorsAreAmbiguous(t *testing.T) {
	var amb *AmbiguousError
	if err := statusError(503, []byte(`{}`), "GET", "/x"); !errors.As(err, &amb) {
		t.Fatalf("5xx = %v", err)
	}
	if err := statusError(400, []byte(`{}`), "GET", "/x"); err == nil || errors.As(err, &amb) {
		t.Fatalf("4xx must be definite: %v", err)
	}
}

func TestJudgmentIsPostedAutomaticallyOnceAfterSend(t *testing.T) {
	h, core, poster := newTestHandler()
	h.pollBase, h.pollMax, h.pollAttempts = time.Millisecond, 4*time.Millisecond, 200
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	core.mu.Lock()
	core.inferenceHeld = true
	core.mu.Unlock()
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	runWork(t, h, actionCB(ActionSelectedSend, "T3", tgtRun)) // 409: must not start a second poll or post
	time.Sleep(20 * time.Millisecond)
	poster.mu.Lock()
	early := len(poster.posts)
	poster.mu.Unlock()
	if early != 0 {
		t.Fatal("nothing may be posted before core has the inference")
	}
	core.mu.Lock()
	core.inferenceHeld = false
	core.mu.Unlock()
	h.Wait()
	poster.mu.Lock()
	defer poster.mu.Unlock()
	if len(poster.posts) != 1 || !strings.Contains(mustJSON(poster.posts[0]), "What I noticed") {
		t.Fatalf("posts = %d, want exactly one Message 3", len(poster.posts))
	}
}

func TestJudgmentPollGivesUpAndStopsOnShutdown(t *testing.T) {
	h, core, poster := newTestHandler()
	h.pollBase, h.pollMax, h.pollAttempts = time.Millisecond, time.Millisecond, 3
	core.inferenceHeld = true
	h.startJudgmentPoll(FixtureRunID, FixtureEpisodeID)
	h.startJudgmentPoll(FixtureRunID, FixtureEpisodeID) // one poll per episode
	h.Wait()
	if len(poster.posts) != 0 {
		t.Fatal("gave up: nothing to post")
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.setRunContext(ctx)
	h.pollBase, h.pollAttempts = time.Hour, 10
	h.startJudgmentPoll(FixtureRunID, FixtureEpisodeID)
	cancel()
	done := make(chan struct{})
	go func() { h.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown must end the poll")
	}
}

type panicCore struct{ *memCore }

func (panicCore) GetJudgmentInference(context.Context, string) (JudgmentInference, error) {
	panic("boom")
}

func TestBackgroundPanicsAreRecovered(t *testing.T) {
	poster := &fakePoster{}
	h := NewHandler(panicCore{newMemCore()}, poster, "C", "", quietLog())
	h.pollBase = time.Millisecond
	h.startJudgmentPoll(FixtureRunID, FixtureEpisodeID)
	h.Wait() // would crash the test binary without recover
}

func TestBIClaimsAreNumberedAndEachHasItsOwnEvidenceUnderTheEvidenceHeading(t *testing.T) {
	f := NewFixture()
	m := RenderBI(f.BI, f.AccountName, "", "")
	raw := mustJSON(m)
	heading := strings.Index(raw, `"ghost.bi.evidence"`)
	if heading < strings.Index(raw, `"ghost.bi.why"`) {
		t.Fatal("Evidence comes after Why it matters")
	}
	for i := range f.BI.Claims {
		n := string(rune('1' + i))
		claim, ev := strings.Index(raw, `"ghost.bi.claim.`+n+`"`), strings.Index(raw, `"ghost.bi.evidence.`+n+`"`)
		if claim < 0 || ev < heading || ev < claim {
			t.Fatalf("claim %s and its evidence out of order", n)
		}
		if !strings.Contains(raw, "*"+n+".*") {
			t.Fatalf("claim %s is not numbered", n)
		}
	}
	if err := ValidateMessage(m); err != nil {
		t.Fatal(err)
	}
}

func TestTruncationNeverCutsAnEntityInHalf(t *testing.T) {
	for _, s := range []string{strings.Repeat("a", 8) + "&amp;&amp;&amp;", strings.Repeat("x", 6) + "&lt;tag&gt;"} {
		for n := 3; n < len(s); n++ {
			got := strings.TrimSuffix(truncate(s, n), "…")
			if i := strings.LastIndexByte(got, '&'); i >= 0 && !strings.Contains(got[i:], ";") {
				t.Fatalf("truncate(%q, %d) = %q ends in a partial entity", s, n, got)
			}
		}
	}
}

func slackUser(id, name string) slack.User { return slack.User{ID: id, Name: name} }
