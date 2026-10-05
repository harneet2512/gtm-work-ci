package slacksurface

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

type fakePoster struct {
	mu         sync.Mutex
	posts      []Message
	updates    []Message
	updateTS   []string
	opened     []slack.ModalViewRequest // loading modals (views.open)
	views      []slack.ModalViewRequest // filled-in modals (views.update)
	ephemerals []string
	updateErr  error
}

func (p *fakePoster) PostMessage(_ context.Context, _ string, m Message) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.posts = append(p.posts, m)
	return fmt.Sprintf("1700000000.%06d", len(p.posts)), nil
}

func (p *fakePoster) UpdateMessage(_ context.Context, _, ts string, m Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.updateErr != nil {
		return p.updateErr
	}
	p.updates, p.updateTS = append(p.updates, m), append(p.updateTS, ts)
	return nil
}

func (p *fakePoster) OpenView(_ context.Context, _ string, v slack.ModalViewRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opened = append(p.opened, v)
	return fmt.Sprintf("V%d", len(p.opened)), nil
}

func (p *fakePoster) UpdateView(_ context.Context, _ string, v slack.ModalViewRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.views = append(p.views, v)
	return nil
}

func (p *fakePoster) PostEphemeral(_ context.Context, _, _, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ephemerals = append(p.ephemerals, text)
	return nil
}

func (p *fakePoster) FindMessage(context.Context, string, time.Time, MessageMeta) (string, bool, error) {
	return "", false, nil
}

func (p *fakePoster) DeleteMessage(context.Context, string, string) error { return nil }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestHandler() (*Handler, *memCore, *fakePoster) {
	core, poster := newMemCore(), &fakePoster{}
	return NewHandler(core, poster, "C0TEST", "", quietLog()), core, poster
}

func actionCB(actionID, trigger string, t Target) slack.InteractionCallback {
	return slack.InteractionCallback{
		Type: slack.InteractionTypeBlockActions, TriggerID: trigger,
		User:      slack.User{ID: "U1", Name: "alex"},
		Container: slack.Container{ChannelID: "C0TEST", MessageTs: "1.2"},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{
			{ActionID: actionID, Value: t.Encode()},
		}},
	}
}

func viewCB(id, callback string, t Target, values map[string]string) slack.InteractionCallback {
	st := map[string]map[string]slack.BlockAction{}
	for k, v := range values {
		st[k] = map[string]slack.BlockAction{inputAction: {Value: v}}
	}
	return slack.InteractionCallback{
		Type: slack.InteractionTypeViewSubmission, User: slack.User{ID: "U1", Name: "alex"},
		View: slack.View{ID: id, CallbackID: callback, PrivateMetadata: t.Encode(), State: &slack.ViewState{Values: st}},
	}
}

func runWork(t *testing.T, h *Handler, cb slack.InteractionCallback) any {
	t.Helper()
	ack, work := h.HandleInteraction(context.Background(), cb)
	if work != nil {
		work(context.Background())
	}
	return ack
}

var (
	tgtA   = Target{RunID: FixtureRunID, CandidateID: fixtureCandA}
	tgtB   = Target{RunID: FixtureRunID, CandidateID: fixtureCandB}
	tgtRun = Target{RunID: FixtureRunID}
	tgtJ   = Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID}
	metaB  = Target{RunID: FixtureRunID, CandidateID: fixtureCandB, ChannelID: "C0TEST", MessageTS: "1.2"}
)

// editValues are the Edit modal's values for candidate B as the human saw them.
func editValues(core *memCore) map[string]string {
	c, _ := core.fx.Strategies.Candidate(fixtureCandB)
	v := viewOf(c, nil, core.fx.Directory)
	return map[string]string{inputTo: joinAddrs(v.To), inputCC: joinAddrs(v.CC), inputSubject: v.Subject, inputBody: v.Body}
}

func TestChooseTwiceReturnsTheExistingDecision(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionStrategyChoose, "T2", tgtB)) // a second click, same candidate
	if len(poster.updates) != 2 || len(poster.ephemerals) != 0 || len(core.writes()) != 2 {
		t.Fatalf("updates=%d ephemerals=%v writes=%d", len(poster.updates), poster.ephemerals, len(core.writes()))
	}
}

func TestChoosingADifferentCandidateShowsTheLockedChoice(t *testing.T) {
	h, _, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionStrategyChoose, "T2", tgtA)) // 409 selection_locked
	last := mustJSON(poster.updates[len(poster.updates)-1])
	if !strings.Contains(last, "Send package, buyer sets timing") || len(poster.ephemerals) != 0 {
		t.Fatalf("the message must show the locked choice (B), got %s", last)
	}
}

func TestViewFullNeverWritesToCore(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyViewFull, "T1", tgtB))
	if len(poster.opened) != 1 || len(poster.views) != 1 || len(core.writes()) != 0 || len(poster.updates) != 0 {
		t.Fatalf("View full must only open a modal: views=%d writes=%v", len(poster.views), core.writes())
	}
}

func TestFailureTellsOnlyTheUserAndAllowsRetry(t *testing.T) {
	h, core, poster := newTestHandler()
	core.failNext = errors.New("core down")
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	if len(poster.ephemerals) != 1 || len(poster.updates) != 0 || len(poster.posts) != 0 {
		t.Fatalf("a failure must be an ephemeral to the user, not a channel message: %+v", poster)
	}
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB)) // same delivery retried by Slack after our failure
	if len(poster.updates) != 1 {
		t.Fatal("a failed attempt must not be swallowed by the retry guard")
	}
}

func TestSendBeforeChoiceIsNotReportedAsSent(t *testing.T) {
	h, _, poster := newTestHandler()
	runWork(t, h, actionCB(ActionSelectedSend, "T1", tgtRun))
	if len(poster.ephemerals) != 1 || !strings.Contains(poster.ephemerals[0], "did not send") {
		t.Fatalf("ephemerals = %v", poster.ephemerals)
	}
}

func TestSendWhenInferenceNotReadyPostsNothingMore(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	core.inferenceHeld = true
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	if len(poster.updates) != 2 || len(poster.posts) != 0 || len(poster.ephemerals) != 0 {
		t.Fatalf("send must update message 2 and post nothing yet: updates=%d posts=%d", len(poster.updates), len(poster.posts))
	}
	core.inferenceHeld = false
	if _, err := h.Publisher().PostJudgment(context.Background(), FixtureRunID, FixtureEpisodeID); err != nil || len(poster.posts) != 1 {
		t.Fatalf("the judgment can be posted later: %v", err)
	}
}

func TestRepeatedSendNeverPostsASecondJudgment(t *testing.T) {
	h, _, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	runWork(t, h, actionCB(ActionSelectedSend, "T3", tgtRun)) // 409 already_decided
	if len(poster.posts) != 1 || len(poster.updates) != 3 || len(poster.ephemerals) != 0 {
		t.Fatalf("posts=%d updates=%d ephemerals=%v", len(poster.posts), len(poster.updates), poster.ephemerals)
	}
}

func TestEditAfterSendIsRefusedHonestly(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	vals := editValues(core)
	vals[inputBody] = "changed"
	ack := runWork(t, h, viewCB("V9", CallbackEditModal, metaB, vals))
	if p := mustJSON(ack); !strings.Contains(p, "already sent") {
		t.Fatalf("the modal must show the refusal inline, got %s", p)
	}
	if len(poster.updates) != 3 {
		t.Fatalf("the message must be re-rendered with core's truth, updates=%d", len(poster.updates))
	}
}

func TestNoOpEditDoesNotCallCore(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	before := len(core.writes())
	runWork(t, h, viewCB("V1", CallbackEditModal, metaB, editValues(core)))
	if len(core.writes()) != before {
		t.Fatal("an unchanged edit must not be written to core")
	}
}

func TestEditResolvesRecipientsToPeopleAndKeepsWhy(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	vals := editValues(core)
	vals[inputTo], vals[inputCC] = "MARCO@ACME.EXAMPLE.TEST", "" // case-insensitive email; CC cleared on purpose
	runWork(t, h, viewCB("V1", CallbackEditModal, metaB, vals))
	w := core.writes()
	last := w[len(w)-1]
	if !strings.Contains(last, `"final_to":[{"person_id":"`+fixtureMarco+`","role":"to","why":"Requested the security documents"}]`) ||
		!strings.Contains(last, `"final_cc":[]`) || strings.Contains(last, "final_artifact\":null") {
		t.Fatalf("edit request wrong: %s", last)
	}
}

func TestModalValidationErrorsAreInline(t *testing.T) {
	h, core, _ := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	before := len(core.writes())
	good := editValues(core)
	unknown := editValues(core)
	unknown[inputCC] = "ghost@nowhere.test"
	cases := []struct {
		cb   slack.InteractionCallback
		want string
	}{
		{viewCB("V1", CallbackEditModal, metaB, map[string]string{inputTo: "", inputSubject: "", inputBody: ""}), "recipient"},
		{viewCB("V2", CallbackEditModal, metaB, unknown), "No person on this account"},
		{viewCB("V3", CallbackCorrectionModal, tgtJ, map[string]string{inputCorrection: " "}), "what you meant"},
		{viewCB("V4", CallbackNoteModal, tgtJ, map[string]string{inputNote: ""}), "note"},
	}
	_ = good
	for i, c := range cases {
		ack, work := h.HandleInteraction(context.Background(), c.cb)
		if ack == nil || work != nil || !strings.Contains(mustJSON(ack), c.want) {
			t.Fatalf("case %d: want inline errors containing %q, got ack=%v", i, c.want, mustJSON(ack))
		}
	}
	if len(core.writes()) != before {
		t.Fatal("invalid input reached core")
	}
}

func TestVerdictsAndNotes(t *testing.T) {
	h, core, poster := newTestHandler()
	runWork(t, h, actionCB(ActionStrategyChoose, "T1", tgtB))
	runWork(t, h, actionCB(ActionSelectedSend, "T2", tgtRun))
	runWork(t, h, actionCB(ActionJudgmentConfirm, "T3", tgtJ))
	runWork(t, h, actionCB(ActionJudgmentConfirm, "T4", tgtJ)) // core: verdict already recorded -> conflict -> re-render
	runWork(t, h, actionCB(ActionJudgmentNote, "T5", tgtJ))
	meta := Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID, ChannelID: "C0TEST", MessageTS: "9.9"}
	runWork(t, h, viewCB("V1", CallbackNoteModal, meta, map[string]string{inputNote: "keep an eye on this"}))
	if core.inf.HumanVerdict != VerdictConfirmed || core.inf.HumanNote == nil || *core.inf.HumanNote != "keep an eye on this" {
		t.Fatalf("inference = %+v", core.inf)
	}
	if len(poster.opened) != 1 || len(poster.ephemerals) != 0 {
		t.Fatalf("modals opened=%d ephemerals=%v", len(poster.opened), poster.ephemerals)
	}
}

func TestIgnoredInteractions(t *testing.T) {
	h, core, poster := newTestHandler()
	for _, cb := range []slack.InteractionCallback{
		{Type: slack.InteractionTypeBlockActions}, // no actions
		actionCB(ActionBIViewAccount, "T1", tgtB),
		actionCB("ghost.unknown", "T2", tgtB),
		{Type: slack.InteractionTypeBlockActions, ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: ActionStrategyChoose, Value: "garbage"}}}},
		{Type: slack.InteractionTypeViewSubmission, View: slack.View{PrivateMetadata: "garbage"}},
		{Type: slack.InteractionTypeViewSubmission, View: slack.View{CallbackID: "other", PrivateMetadata: tgtB.Encode()}},
		{Type: slack.InteractionTypeViewClosed},
	} {
		runWork(t, h, cb)
	}
	if len(core.writes()) != 0 || len(poster.updates) != 0 || len(poster.opened) != 0 {
		t.Fatal("ignored interactions must have no effect")
	}
}

func TestSlackAndWebDecisionsDifferOnlyInSurfaceAndActor(t *testing.T) {
	slackReq := StrategyDecisionRequest{SelectedCandidateID: fixtureCandB, Surface: SurfaceSlack, ActorLabel: "alex"}
	webReq := StrategyDecisionRequest{SelectedCandidateID: fixtureCandB, Surface: "web", ActorLabel: "Dana Kim"}
	if mustJSON(slackReq) == mustJSON(webReq) {
		t.Fatal("surface must be recorded")
	}
	slackReq.Surface, slackReq.ActorLabel = "web", "Dana Kim"
	if mustJSON(slackReq) != mustJSON(webReq) {
		t.Fatal("beyond surface and actor, Slack and web decisions must be identical")
	}
}

func TestPublisherRefusesWrongCandidateCount(t *testing.T) {
	core, poster := newMemCore(), &fakePoster{}
	core.fx.Strategies.StrategySet.Candidates = core.fx.Strategies.StrategySet.Candidates[:2]
	if _, err := NewPublisher(core, poster, "C", "").PostChooser(context.Background(), FixtureRunID); err == nil || len(poster.posts) != 0 {
		t.Fatal("the demo path needs exactly three candidates")
	}
}

func TestPublisherJudgmentNotReady(t *testing.T) {
	core, poster := newMemCore(), &fakePoster{}
	if _, err := NewPublisher(core, poster, "C", "").PostJudgment(context.Background(), FixtureRunID, FixtureEpisodeID); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v", err)
	}
}

func TestDuplicateGuardExpires(t *testing.T) {
	h, _, _ := newTestHandler()
	if !h.firstDelivery("k") || h.firstDelivery("k") {
		t.Fatal("second delivery must be a duplicate")
	}
	h.now = func() time.Time { return time.Now().Add(2 * dedupeTTL) }
	if !h.firstDelivery("k") {
		t.Fatal("a delivery older than the TTL must be accepted again")
	}
}
