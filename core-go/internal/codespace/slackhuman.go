package codespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// SlackHuman plays the human's path through Cliff's real Slack handlers: the Publisher posts Message 1, 2 and 3, and the
// Handler receives the clicks and the modal submissions (choose, Edit email, Send, Edit interpretation) exactly as Socket
// Mode would deliver them. Only the transport is fake (FakeSlack), so nothing reaches a channel. Because the recorded
// requests come out of the same code the live demo runs, the presenter's identical typing replays from the cache.
type SlackHuman struct {
	Core    slacksurface.Core
	WebURL  string
	Channel string
	// Wait polls ready until it says yes (the recorder's bounded wait).
	Wait func(ctx context.Context, what string, ready func() (bool, error)) error
	Say  func(format string, args ...any)
	// Log receives the handler's logs; nil discards them.
	Log *slog.Logger
	// Real, when set, is the live Slack transport: messages go to the channel and are mirrored locally (the live drive).
	// Nil keeps the record run fully local.
	Real slacksurface.Poster

	trigger atomic.Int64
}

func (h *SlackHuman) say(format string, args ...any) {
	if h.Say != nil {
		h.Say(format, args...)
	}
}

func (h *SlackHuman) logger() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// session is one case's conversation with the handler.
type session struct {
	h       *SlackHuman
	handler *slacksurface.Handler
	fake    *FakeSlack
	user    slack.User
}

// Act implements Human.
func (h *SlackHuman) Act(ctx context.Context, c Case, st demorun.DemoState, s Script) (HumanPath, error) {
	fake := NewFakeSlack()
	channel := h.Channel
	if channel == "" {
		channel = "C0GHOSTDEMO"
	}
	var poster slacksurface.Poster = fake
	if h.Real != nil {
		poster = NewMirrorPoster(h.Real, fake)
	}
	handler := slacksurface.NewHandler(h.Core, poster, channel, h.WebURL, h.logger(), slacksurface.WithManifest(st.ManifestID))
	defer handler.Wait()
	sess := &session{h: h, handler: handler, fake: fake, user: slack.User{ID: s.UserID, Name: s.UserName}}
	path := HumanPath{Case: c.Label, ChoiceRank: s.ChoiceRank}

	pub := handler.Publisher()
	if _, err := pub.PostBIForEpisode(ctx, st.AccountID, st.EpisodeID); err != nil {
		return path, fmt.Errorf("Message 1: %w", err)
	}
	chooser, err := pub.PostChooser(ctx, st.RunID)
	if err != nil {
		return path, fmt.Errorf("Message 2: %w", err)
	}
	if err := sess.chooseAndEdit(ctx, chooser, st, s, &path); err != nil {
		return path, err
	}
	if err := sess.sendAndCorrect(ctx, chooser, st, s, &path); err != nil {
		return path, err
	}
	return path, nil
}

// chooseAndEdit presses Select on the candidate of the scripted rank, then edits the email in the modal.
func (x *session) chooseAndEdit(ctx context.Context, chooser slacksurface.MessageRef, st demorun.DemoState, s Script, path *HumanPath) error {
	rs, err := x.h.Core.GetStrategies(ctx, st.RunID)
	if err != nil {
		return fmt.Errorf("read the strategies: %w", err)
	}
	pick, _, skipped, err := pickSendable(rs, s.ChoiceRank)
	for _, b := range skipped {
		x.h.say("%s: skipping %q: Send would refuse it, blocked by %s", path.Case, b.Title, b.Why)
	}
	if err != nil {
		return err
	}
	msg, _ := x.fake.Message(chooser.Channel, chooser.TS)
	button, err := findButton(msg, slacksurface.ActionStrategyChoose, pick.CandidateID)
	if err != nil {
		return fmt.Errorf("Message 2 has no Select button for %q: %w", pick.Title, err)
	}
	path.ChoiceButton, path.ChoiceTitle, path.ChoiceRank = button.Label, pick.Title, pick.Ranking
	x.h.say("%s: human presses %q (Ghost's rank %d; Ghost's best is rank 1)", path.Case, button.Label, pick.Ranking)
	if err := x.click(ctx, chooser, button); err != nil {
		return fmt.Errorf("press %s: %w", button.Label, err)
	}

	msg, _ = x.fake.Message(chooser.Channel, chooser.TS)
	edit, err := findButton(msg, slacksurface.ActionSelectedEdit, "")
	if err != nil {
		return fmt.Errorf("the selected action has no Edit button: %w", err)
	}
	if err := x.click(ctx, chooser, edit); err != nil {
		return fmt.Errorf("open the Edit email modal: %w", err)
	}
	viewID, view, _ := x.fake.LastView()
	in := ModalInputs(view)
	if _, ok := in[slacksurface.InputSubject]; !ok {
		return errors.New("the Edit email modal has no inputs (the email is longer than Slack's input limits)")
	}
	path.To, path.CC = in[slacksurface.InputTo], in[slacksurface.InputCC]
	path.Subject = strings.TrimSpace(in[slacksurface.InputSubject] + s.EditSubjectSuffix)
	path.Body = strings.TrimSpace(in[slacksurface.InputBody] + s.EditBodyAppend)
	x.h.say("%s: human edits subject and body, then saves", path.Case)
	return x.submit(ctx, viewID, view, slacksurface.CallbackEditModal, map[string]string{
		slacksurface.InputTo: path.To, slacksurface.InputCC: path.CC, slacksurface.InputSubject: path.Subject, slacksurface.InputBody: path.Body})
}

// sendAndCorrect presses Send, waits for Message 3, and corrects Ghost's interpretation in the modal.
func (x *session) sendAndCorrect(ctx context.Context, chooser slacksurface.MessageRef, st demorun.DemoState, s Script, path *HumanPath) error {
	msg, _ := x.fake.Message(chooser.Channel, chooser.TS)
	send, err := findButton(msg, slacksurface.ActionSelectedSend, "")
	if err != nil {
		return fmt.Errorf("the selected action has no Send button: %w", err)
	}
	x.h.say("%s: human presses Send (dry run: the final artifact is re-evaluated at send time)", path.Case)
	if err := x.click(ctx, chooser, send); err != nil {
		return fmt.Errorf("press Send: %w", err)
	}
	if x.h.Wait != nil {
		if err := x.h.Wait(ctx, "the judgment inference of "+st.EpisodeID+" was not generated", func() (bool, error) {
			_, err := x.h.Core.GetJudgmentInference(ctx, st.EpisodeID)
			if errors.Is(err, slacksurface.ErrNotReady) || errors.Is(err, slacksurface.ErrNotFound) {
				return false, nil
			}
			return err == nil, err
		}); err != nil {
			return err
		}
	}
	judgment, err := x.handler.Publisher().PostJudgment(ctx, st.RunID, st.EpisodeID)
	if err != nil {
		return fmt.Errorf("Message 3: %w", err)
	}
	msg, _ = x.fake.Message(judgment.Channel, judgment.TS)
	correct, err := findButton(msg, slacksurface.ActionJudgmentCorrect, "")
	if err != nil {
		return fmt.Errorf("Message 3 has no Edit interpretation button: %w", err)
	}
	if err := x.click(ctx, judgment, correct); err != nil {
		return fmt.Errorf("open Edit interpretation: %w", err)
	}
	viewID, view, _ := x.fake.LastView()
	path.Interpretation = ModalInputs(view)[slacksurface.InputCorrection]
	path.Correction, path.Note = strings.TrimSpace(s.Correction), strings.TrimSpace(s.Note)
	x.h.say("%s: human corrects Ghost's interpretation (Message 3)", path.Case)
	return x.submit(ctx, viewID, view, slacksurface.CallbackCorrectionModal, map[string]string{
		slacksurface.InputCorrection: path.Correction, slacksurface.InputNote: path.Note})
}

func findButton(m slacksurface.Message, actionID, candidateID string) (Button, error) {
	for _, b := range Buttons(m) {
		if b.ActionID == actionID && (candidateID == "" || b.Target.CandidateID == candidateID) {
			return b, nil
		}
	}
	return Button{}, fmt.Errorf("no %s button on the message", actionID)
}

// click delivers a block action, as Socket Mode would: acknowledge, then the deferred work.
func (x *session) click(ctx context.Context, at slacksurface.MessageRef, b Button) error {
	cb := slack.InteractionCallback{
		Type: slack.InteractionTypeBlockActions, User: x.user,
		TriggerID: fmt.Sprintf("T%d", x.h.trigger.Add(1)),
		Container: slack.Container{ChannelID: at.Channel, MessageTs: at.TS},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{
			{ActionID: b.ActionID, Value: b.Value}}},
	}
	err := x.deliver(ctx, cb)
	if err != nil {
		if m, ok := x.fake.Message(at.Channel, at.TS); ok {
			err = fmt.Errorf("%w; the message now reads: %s", err, MessageTexts(m))
		}
	}
	return err
}

// submit delivers a modal submission with the typed values.
func (x *session) submit(ctx context.Context, viewID string, v slack.ModalViewRequest, callbackID string, values map[string]string) error {
	state := &slack.ViewState{Values: map[string]map[string]slack.BlockAction{}}
	for block, text := range values {
		state.Values[block] = map[string]slack.BlockAction{slacksurface.InputAction: {Value: text}}
	}
	cb := slack.InteractionCallback{
		Type: slack.InteractionTypeViewSubmission, User: x.user,
		View: slack.View{ID: viewID, CallbackID: callbackID, PrivateMetadata: v.PrivateMetadata, State: state},
	}
	return x.deliver(ctx, cb)
}

func (x *session) deliver(ctx context.Context, cb slack.InteractionCallback) error {
	ack, work := x.handler.HandleInteraction(ctx, cb)
	if ack != nil {
		raw, _ := json.Marshal(ack)
		return fmt.Errorf("Cliff refused the form: %s", raw)
	}
	if work != nil {
		work(ctx)
	}
	if problems := x.fake.Problems(); len(problems) > 0 {
		return fmt.Errorf("Cliff said the action did not go through: %s", strings.Join(problems, "; "))
	}
	return nil
}

var _ Human = (*SlackHuman)(nil)
