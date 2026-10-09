package ask

import (
	"context"
	"errors"
	"testing"
)

func TestTheConfirmationDescribesTheActionFromItsKindNotFromModelText(t *testing.T) {
	// The model words a play proposal as something harmless; the kind decides what runs and what is shown.
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "ok", ProposedAction: &WorkerAction{Kind: "play_next", Summary: "Just show the status, nothing changes"}}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if got := ans.ProposedAction.Summary; got != "Play the next event for MedTech Advances (event 13 of 13)" {
		t.Errorf("summary = %q", got)
	}
	w.answer.ProposedAction = &WorkerAction{Kind: "demo_status", Summary: "Play the next event"}
	ans, _ = newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if got := ans.ProposedAction.Summary; got != "Show the replay status of the demo case (read only)" {
		t.Errorf("summary = %q", got)
	}
	// With no replay to read, the plain fixed sentence is used, still never the model's.
	bare := &fakeBackend{routes: map[string]reply{}}
	w.answer.ProposedAction = &WorkerAction{Kind: "play_next", Summary: "anything"}
	ans, _ = newService(t, w, bare, nil).Ask(context.Background(), Request{Text: "q", ChannelKind: ChannelDM, User: "U1"})
	if got := ans.ProposedAction.Summary; got != "Play the next event of the demo case" {
		t.Errorf("summary = %q", got)
	}
}

func TestResetDemoIsNotAnAction(t *testing.T) {
	w := &fakeWorker{answer: WorkerAnswer{AnswerMarkdown: "ok", ProposedAction: &WorkerAction{Kind: "reset_demo", Summary: "Reset"}}}
	ans, _ := newService(t, w, medtech(), nil).Ask(context.Background(), Request{Text: "reset", ChannelKind: ChannelDM, User: "U1"})
	if ans.ProposedAction != nil {
		t.Errorf("a reset was offered: %+v", ans.ProposedAction)
	}
	if _, err := newService(t, w, medtech(), nil).RunAction(context.Background(), ActionRequest{Kind: "reset_demo", User: "U1"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("err = %v", err)
	}
}
