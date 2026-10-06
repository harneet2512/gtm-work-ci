package ask

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// The owner's chained task end to end through the real Slack adapter, the real service and the real read tools, with a
// scripted model: "play the next event and tell me which evals warned" pauses on a confirmation, runs only after Run,
// waits for the pipeline, resumes with the evals and answers in the same thread; a follow-up then has its memory.
func TestE2EAChainedTaskPausesOnTheConfirmationThenFinishesInTheSameThread(t *testing.T) {
	var svc *Service
	w := &hookWorker{}
	w.fn = func(call int, req WorkerRequest) (WorkerAnswer, error) {
		switch call {
		case 1:
			return WorkerAnswer{AnswerMarkdown: "I will play the next event first, then check which evals warned.", Model: "scripted",
				ProposedAction: &WorkerAction{Kind: "play_next", Summary: "anything"}}, nil
		case 2:
			res, err := svc.RunTool(context.Background(), req.AskToken, "gate_results", map[string]any{"account": "MedTech", "episode_id": "latest"})
			if err != nil {
				return WorkerAnswer{}, err
			}
			var cites []WorkerCitation
			for _, l := range res.Links {
				u := l.URL
				cites = append(cites, WorkerCitation{CallID: "t1", Tool: "gate_results", Label: l.Label, URL: &u})
			}
			return WorkerAnswer{AnswerMarkdown: "After the event, D4 warned: the email names no date [t1].", Citations: cites, ToolCalls: 1, Model: "scripted"}, nil
		}
		return plainAnswer("Because the email names no date, so D4 could not pass."), nil
	}
	b := medtech()
	b.routes["/replay/manifests/"+manifestID+"/progress"] = ok(map[string]any{"overall": "complete", "stages": []any{
		map[string]any{"stage": "evals", "status": "warning"}}})
	svc = newService(t, w, b, nil)
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	svc.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) }
	rec := &slackRec{}
	h := slacksurface.NewAskHandler(bridge{svc}, rec, "UCLIFF", "BCLIFF", nil)
	mention := func(text, ts, thread string) slackevents.EventsAPIEvent {
		return slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Type: "app_mention",
			Data: &slackevents.AppMentionEvent{User: "U1", Text: "<@UCLIFF> " + text, TimeStamp: ts, ThreadTimeStamp: thread, Channel: "C1"}}}
	}

	h.HandleEvent(mention("Play the next event and tell me which evals warned", "50.1", ""))(context.Background())
	if len(b.posts) != 0 || len(rec.buttons) != 2 || !strings.Contains(rec.ephemerals[0], "*Play the next event*\nOn: MedTech Advances (event 13 of 13)") {
		t.Fatalf("step one must stop at the confirmation: posts %v ephemerals %v", b.posts, rec.ephemerals)
	}
	val := strings.TrimPrefix(rec.buttons[0], slacksurface.ActionAskRun+"=")
	cb := slack.InteractionCallback{Type: slack.InteractionTypeBlockActions, User: slack.User{ID: "U1"}, Container: slack.Container{ChannelID: "C1"},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: slacksurface.ActionAskRun, Value: val}}}}
	_, work := slacksurface.NewHandler(nil, nil, "C", "", nil, slacksurface.WithAsk(h)).HandleInteraction(context.Background(), cb)
	work(context.Background())

	if len(b.posts) != 1 || b.posts[0] != "/replay/manifests/"+manifestID+"/episodes/next" {
		t.Fatalf("the confirmed step must post the web Play route once: %v", b.posts)
	}
	final := rec.updates[len(rec.updates)-1]
	for _, want := range []string{"Released event 13 of 13", "D4 warned", "Gate D4 (warn) in the trace", "Trace: <http://web.test/ask/traces/"} {
		if !strings.Contains(final, want) {
			t.Errorf("the final message lacks %q:\n%s", want, final)
		}
	}
	for i, th := range rec.threads {
		if th != "50.1" {
			t.Errorf("message %d (%q) was not posted in the thread: %q", i, rec.posts[i], th)
		}
	}
	if len(rec.ephemerals) != 1 {
		t.Errorf("no second confirmation was due: %v", rec.ephemerals)
	}

	h.HandleEvent(mention("why?", "50.2", "50.1"))(context.Background())
	hist := w.got[2].History
	if len(hist) < 3 || hist[0].Role != "user" || !strings.Contains(hist[0].Text, "Play the next event and tell me which evals warned") ||
		!strings.Contains(hist[len(hist)-1].Text, "D4 warned") {
		t.Errorf("the follow-up must see the whole task: %+v", hist)
	}
}
