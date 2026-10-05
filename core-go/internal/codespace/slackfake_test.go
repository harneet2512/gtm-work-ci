package codespace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

func textMessage(text string, meta *slacksurface.MessageMeta, buttons ...*slack.ButtonBlockElement) slacksurface.Message {
	blocks := []slack.Block{slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil)}
	if len(buttons) > 0 {
		els := make([]slack.BlockElement, 0, len(buttons))
		for _, b := range buttons {
			els = append(els, b)
		}
		blocks = append(blocks, slack.NewActionBlock("actions", els...))
	}
	return slacksurface.Message{Text: text, Blocks: blocks, Meta: meta}
}

func TestFakeSlackPostsUpdatesFindsAndDeletesLikeSlack(t *testing.T) {
	ctx := context.Background()
	f := NewFakeSlack()
	meta := &slacksurface.MessageMeta{SubjectID: "ep-1", Kind: "chooser"}
	ts1, err := f.PostMessage(ctx, "C1", textMessage("one", meta))
	if err != nil {
		t.Fatal(err)
	}
	ts2, _ := f.PostMessage(ctx, "C1", textMessage("two", nil))
	if ts1 == ts2 || ts1 > ts2 {
		t.Fatalf("each post gets its own increasing ts: %s %s", ts1, ts2)
	}
	if err := f.UpdateMessage(ctx, "C1", ts1, textMessage("one, edited", meta)); err != nil {
		t.Fatal(err)
	}
	if m, ok := f.Message("C1", ts1); !ok || m.Text != "one, edited" {
		t.Fatalf("message = %+v %v", m, ok)
	}
	if err := f.UpdateMessage(ctx, "C1", "9.9", textMessage("x", nil)); err == nil || !strings.Contains(err.Error(), "message_not_found") {
		t.Fatalf("updating a message that is not there: %v", err)
	}
	if ts, found, _ := f.FindMessage(ctx, "C1", time.Time{}, *meta); !found || ts != ts1 {
		t.Fatalf("FindMessage = %s %v", ts, found)
	}
	if _, found, _ := f.FindMessage(ctx, "C1", time.Time{}, slacksurface.MessageMeta{SubjectID: "other", Kind: "chooser"}); found {
		t.Fatal("another subject is not the same message")
	}
	if _, found, _ := f.FindMessage(ctx, "C2", time.Time{}, *meta); found {
		t.Fatal("another channel is not the same message")
	}
	if err := f.DeleteMessage(ctx, "C1", ts1); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Message("C1", ts1); ok {
		t.Fatal("a deleted message is gone")
	}
}

func TestFakeSlackRefusesWhatSlackWouldRefuse(t *testing.T) {
	ctx := context.Background()
	f := NewFakeSlack()
	if _, err := f.PostMessage(ctx, "C1", slacksurface.Message{Text: " "}); err == nil || !strings.Contains(err.Error(), "would not be accepted by Slack") {
		t.Fatalf("a message without fallback text: %v", err)
	}
	ts, _ := f.PostMessage(ctx, "C1", textMessage("ok", nil))
	if err := f.UpdateMessage(ctx, "C1", ts, slacksurface.Message{}); err == nil {
		t.Fatal("an invalid update must be refused")
	}
	if _, err := f.OpenView(ctx, "T1", slack.ModalViewRequest{Title: slack.NewTextBlockObject("plain_text", "", false, false)}); err == nil {
		t.Fatal("a modal with an empty title must be refused")
	}
	if err := f.UpdateView(ctx, "V404", slack.ModalViewRequest{Title: slack.NewTextBlockObject("plain_text", "t", false, false)}); err == nil || !strings.Contains(err.Error(), "view_not_found") {
		t.Fatalf("updating an unknown view: %v", err)
	}
}

func TestFakeSlackModalsAndWhispers(t *testing.T) {
	ctx := context.Background()
	f := NewFakeSlack()
	t1 := slacksurface.Target{RunID: "r1", EpisodeID: "e1"}
	loading := slacksurface.LoadingModal(t1)
	id, err := f.OpenView(ctx, "T1", loading)
	if err != nil {
		t.Fatal(err)
	}
	edit := slacksurface.RenderEditModal(slacksurface.ArtView{To: []string{"Marco <m@x.test>"}, Subject: "Hello", Body: "Body text"}, t1)
	if err := f.UpdateView(ctx, id, edit); err != nil {
		t.Fatal(err)
	}
	gotID, view, ok := f.LastView()
	if !ok || gotID != id {
		t.Fatalf("LastView = %s %v", gotID, ok)
	}
	if v, ok := f.View(id); !ok || v.CallbackID != slacksurface.CallbackEditModal {
		t.Fatalf("the loading modal is replaced once core answered: %+v", v)
	}
	in := ModalInputs(view)
	if in[slacksurface.InputTo] != "Marco <m@x.test>" || in[slacksurface.InputSubject] != "Hello" || in[slacksurface.InputBody] != "Body text" {
		t.Fatalf("inputs = %v", in)
	}
	if _, _, ok := NewFakeSlack().LastView(); ok {
		t.Fatal("no view yet")
	}
	_ = f.PostEphemeral(ctx, "C1", "U1", "That did not go through.")
	if got := f.Problems(); len(got) != 1 || got[0] != "That did not go through." {
		t.Fatalf("problems = %v", got)
	}
	if got := f.Problems(); len(got) != 0 {
		t.Fatalf("whispers are read once: %v", got)
	}
}

func TestButtonsReadsTheActionButtonsAMessageCarries(t *testing.T) {
	target := slacksurface.Target{RunID: "r1", CandidateID: "c2"}
	btn := slack.NewButtonBlockElement(slacksurface.ActionStrategyChoose, target.Encode(), slack.NewTextBlockObject("plain_text", "Select B", false, false))
	link := slack.NewButtonBlockElement("ghost.link", "", slack.NewTextBlockObject("plain_text", "View evals", false, false)) // a URL button: no value
	broken := slack.NewButtonBlockElement("ghost.bad", "not json", slack.NewTextBlockObject("plain_text", "Bad", false, false))
	got := Buttons(textMessage("choose", nil, btn, link, broken))
	if len(got) != 1 || got[0].ActionID != slacksurface.ActionStrategyChoose || got[0].Label != "Select B" || got[0].Target.CandidateID != "c2" {
		t.Fatalf("buttons = %+v", got)
	}
	if got := Buttons(slacksurface.Message{}); got != nil {
		t.Fatalf("an empty message has no buttons: %v", got)
	}
	if _, err := findButton(textMessage("x", nil), slacksurface.ActionSelectedSend, ""); err == nil {
		t.Fatal("a message without the button must be an error")
	}
	if b, err := findButton(textMessage("choose", nil, btn), slacksurface.ActionStrategyChoose, "c2"); err != nil || b.Label != "Select B" {
		t.Fatalf("findButton = %+v %v", b, err)
	}
	if _, err := findButton(textMessage("choose", nil, btn), slacksurface.ActionStrategyChoose, "c9"); err == nil {
		t.Fatal("the button of another candidate is not the one asked for")
	}
}
