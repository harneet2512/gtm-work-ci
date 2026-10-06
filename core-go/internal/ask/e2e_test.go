package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// End to end on the MedTech fixture: a Slack event goes through the real Slack adapter, the real ask service and the real
// read tools over the fixture's read APIs. The model is a scripted agent (no live call): it makes the tool calls a model
// would make, through the service's own tool route with the token the service minted, and cites what came back. The
// model loop itself is tested in worker-py/tests/test_ask_loop.py against the same tool result shape.

type step struct {
	tool string
	args map[string]any
}

// scriptedAgent plays a model: it runs its tool calls through RunTool, then answers citing the calls that found data.
type scriptedAgent struct {
	svc    *Service
	steps  []step
	text   func(res []ToolResult) string
	action *WorkerAction
}

func (a *scriptedAgent) Ask(ctx context.Context, req WorkerRequest) (WorkerAnswer, error) {
	var results []ToolResult
	var cites []WorkerCitation
	for i, st := range a.steps {
		res, err := a.svc.RunTool(ctx, req.AskToken, st.tool, st.args)
		if err != nil {
			return WorkerAnswer{}, err
		}
		results = append(results, res)
		if !res.OK || res.Empty {
			continue
		}
		for _, l := range res.Links {
			u := l.URL
			cites = append(cites, WorkerCitation{CallID: fmt.Sprintf("t%d", i+1), Tool: st.tool, Label: l.Label, URL: &u})
		}
	}
	if len(cites) == 0 && a.action == nil {
		return WorkerAnswer{AnswerMarkdown: "I don't know. The data I can read does not show that.", DontKnow: true, ToolCalls: len(a.steps), Model: "scripted"}, nil
	}
	return WorkerAnswer{AnswerMarkdown: a.text(results), Citations: cites, ProposedAction: a.action, ToolCalls: len(a.steps), Model: "scripted"}, nil
}

type bridge struct{ s *Service }

func (b bridge) Ask(ctx context.Context, r slacksurface.AskRequest) (slacksurface.AskAnswer, error) {
	a, err := b.s.Ask(ctx, Request{Text: r.Text, ChannelKind: r.ChannelKind, User: r.User, ThreadRef: r.ThreadRef, TurnID: r.TurnID})
	if err != nil {
		return slacksurface.AskAnswer{}, err
	}
	var out slacksurface.AskAnswer
	raw, _ := json.Marshal(a)
	return out, json.Unmarshal(raw, &out)
}

func (b bridge) RunAction(ctx context.Context, r slacksurface.AskActionRequest) (slacksurface.AskActionResult, error) {
	a, err := b.s.RunAction(ctx, ActionRequest{Kind: r.Kind, User: r.User, ThreadRef: r.ThreadRef, TurnID: r.TurnID})
	if err != nil {
		return slacksurface.AskActionResult{}, err
	}
	var out slacksurface.AskActionResult
	raw, _ := json.Marshal(a)
	return out, json.Unmarshal(raw, &out)
}

func (b bridge) Progress(_ context.Context, turnID string) (slacksurface.AskProgress, error) {
	p, err := b.s.Progress(turnID)
	return slacksurface.AskProgress{State: p.State, Lines: p.Lines}, err
}

type slackRec struct {
	mu         sync.Mutex
	posts      []string
	threads    []string
	updates    []string
	ephemerals []string
	buttons    []string
}

func shownText(blocks []slack.Block, fallback string) string {
	var parts []string
	for _, b := range blocks {
		if s, ok := b.(*slack.SectionBlock); ok && s.Text != nil {
			parts = append(parts, s.Text.Text)
		}
	}
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, "\n")
}

func (r *slackRec) PostReply(_ context.Context, _, thread, t string, b []slack.Block) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.posts, r.threads = append(r.posts, shownText(b, t)), append(r.threads, thread)
	return fmt.Sprintf("1.%d", len(r.posts)), nil
}

func (r *slackRec) UpdateReply(_ context.Context, _, _, t string, b []slack.Block) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, shownText(b, t))
	return nil
}

func (r *slackRec) PostEphemeralTo(_ context.Context, _, _, _, t string, b []slack.Block) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ephemerals = append(r.ephemerals, shownText(b, t))
	for _, blk := range b {
		if ab, ok := blk.(*slack.ActionBlock); ok {
			for _, el := range ab.Elements.ElementSet {
				btn := el.(*slack.ButtonBlockElement)
				r.buttons = append(r.buttons, btn.ActionID+"="+btn.Value)
			}
		}
	}
	return nil
}

// ask runs one DM question through the whole chain and returns what Slack showed, the backend and the handler.
func askInSlack(t *testing.T, agent *scriptedAgent, question string) (*slackRec, *fakeBackend, *slacksurface.AskHandler) {
	t.Helper()
	b := medtech()
	svc := newService(t, agent, b, nil)
	agent.svc = svc
	rec := &slackRec{}
	h := slacksurface.NewAskHandler(bridge{svc}, rec, "UCLIFF", "BCLIFF", nil)
	ev := slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Type: "message",
		Data: &slackevents.MessageEvent{User: "U1", Text: question, TimeStamp: "10.1", Channel: "D1", ChannelType: "im"}}}
	work := h.HandleEvent(ev)
	if work == nil {
		t.Fatal("the DM was not handled")
	}
	work(context.Background())
	return rec, b, h
}

func TestE2EWhatChangedAtMedTechOnNov9(t *testing.T) {
	agent := &scriptedAgent{steps: []step{
		{"account_state", map[string]any{"account": "MedTech", "as_of": "2023-11-09"}},
		{"timeline", map[string]any{"account": "MedTech", "from": "2023-11-09", "to": "2023-11-09"}}},
		text: func(r []ToolResult) string {
			return "On Nov 9 MedTech Advances asked for a security review before signing [t1][t2]. The next step is to send the security package [t1]."
		}}
	rec, _, _ := askInSlack(t, agent, "What changed at MedTech on Nov 9?")
	got := strings.Join(rec.updates, "\n")
	for _, want := range []string{"security review before signing", "Open in gtm_ai:", "<http://web.test/accounts/" + acctID + "|MedTech Advances in gtm_ai>"} {
		if !strings.Contains(got, want) {
			t.Errorf("answer lacks %q:\n%s", want, got)
		}
	}
	if rec.posts[0] != "Thinking…" || rec.threads[0] != "" {
		t.Errorf("posts %v threads %v", rec.posts, rec.threads)
	}
}

func TestE2EWhyDidYouRecommendOptionA(t *testing.T) {
	agent := &scriptedAgent{steps: []step{{"episode", map[string]any{"account": "MedTech"}}, {"strategies", map[string]any{"account": "MedTech"}}},
		text: func(r []ToolResult) string {
			return "Option A, send the security package, was recommended because the buyer asked for it [t2]; the recommendation is on the episode [t1]."
		}}
	rec, _, _ := askInSlack(t, agent, "Why did you recommend option A?")
	got := strings.Join(rec.updates, "\n")
	for _, want := range []string{"the buyer asked for it", "<http://web.test/episodes/" + episodeID + "|Episode>", "|Run evals>"} {
		if !strings.Contains(got, want) {
			t.Errorf("answer lacks %q:\n%s", want, got)
		}
	}
}

func TestE2EWhichEvalsWarnedOnM2(t *testing.T) {
	agent := &scriptedAgent{steps: []step{{"gate_results", map[string]any{"account": "MedTech", "episode_id": "latest"}}},
		text: func(r []ToolResult) string { return "D4 warned: the email names no date [t1]." }}
	rec, _, _ := askInSlack(t, agent, "Which evals warned on M2?")
	got := strings.Join(rec.updates, "\n")
	if !strings.Contains(got, "D4 warned") || !strings.Contains(got, "span=candidates%3Ac1") || !strings.Contains(got, "Gate D4 (warn) in the trace") {
		t.Errorf("answer = %s", got)
	}
	if strings.Contains(got, "Gate B2") {
		t.Error("a passing gate must not be linked as a warning")
	}
}

func TestE2EPlayTheNextEventAsksForAConfirmationThenPostsTheWebPlayRoute(t *testing.T) {
	agent := &scriptedAgent{action: &WorkerAction{Kind: "play_next", Summary: "Release the next event of the MedTech case"},
		text: func(r []ToolResult) string { return "I can play the next event. Confirm below and I will release it." }}
	rec, backend, h := askInSlack(t, agent, "Play the next event")
	if len(backend.posts) != 0 {
		t.Fatal("the event was played before a human confirmed")
	}
	if len(rec.ephemerals) != 1 || !strings.Contains(rec.ephemerals[0], "*Play the next event*\nOn: MedTech Advances (event 13 of 13)") || len(rec.buttons) != 2 {
		t.Fatalf("confirmation = %v buttons %v", rec.ephemerals, rec.buttons)
	}
	// Press Run.
	val := strings.TrimPrefix(rec.buttons[0], slacksurface.ActionAskRun+"=")
	cb := slack.InteractionCallback{Type: slack.InteractionTypeBlockActions, User: slack.User{ID: "U1"}, Container: slack.Container{ChannelID: "D1"},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: slacksurface.ActionAskRun, Value: val}}}}
	hh := slacksurface.NewHandler(nil, nil, "C", "", nil, slacksurface.WithAsk(h))
	_, work := hh.HandleInteraction(context.Background(), cb)
	work(context.Background())
	if len(backend.posts) != 1 || backend.posts[0] != "/replay/manifests/"+manifestID+"/episodes/next" {
		t.Fatalf("posts = %v", backend.posts)
	}
	if got := rec.updates[len(rec.updates)-1]; !strings.Contains(got, "Released event 13 of 13") {
		t.Errorf("outcome = %q", got)
	}
}

func TestE2EWhenTheDataDoesNotShowItCliffSaysIDontKnow(t *testing.T) {
	agent := &scriptedAgent{steps: []step{{"search_activities", map[string]any{"query": "zebra", "account": "MedTech"}}},
		text: func(r []ToolResult) string { return "They signed on Tuesday." }}
	rec, _, _ := askInSlack(t, agent, "When did MedTech sign?")
	got := strings.Join(rec.updates, "\n")
	if !strings.HasPrefix(got, "I don't know") || strings.Contains(got, "Open in gtm_ai") || strings.Contains(got, "Tuesday") {
		t.Errorf("answer = %q", got)
	}
}

func TestE2EADraftIsLabelledAndNothingIsSentOrWritten(t *testing.T) {
	agent := &scriptedAgent{steps: []step{{"draft_followup", map[string]any{"account": "MedTech", "intent": "send the security package"}}},
		text: func(r []ToolResult) string {
			return "Hi Dana, attached is the security package. Sam is copied. [t1]"
		}}
	rec, backend, _ := askInSlack(t, agent, "Draft a follow-up to MedTech about the security review")
	got := strings.Join(rec.updates, "\n")
	if !strings.HasPrefix(got, "*DRAFT (dry run, nothing was sent)*") {
		t.Errorf("answer = %q", got)
	}
	if len(backend.posts) != 0 || len(rec.ephemerals) != 0 {
		t.Errorf("a draft must write and propose nothing: posts %v ephemerals %v", backend.posts, rec.ephemerals)
	}
}
