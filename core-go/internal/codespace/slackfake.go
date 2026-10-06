package codespace

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/slack-go/slack"

	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// FakeSlack is the Slack transport of the record run: it implements slacksurface.Poster in memory, so the real Cliff
// handlers (Publisher, Handler) run exactly as they do against Slack, validate every message and modal against the same
// limits, and nothing is ever posted to a channel. It is also what the record run reads the buttons and modal forms from.
type FakeSlack struct {
	mu         sync.Mutex
	seq        int
	messages   map[string]*PostedMessage
	order      []string
	views      map[string]slack.ModalViewRequest
	ephemerals []string
}

// PostedMessage is one message the fake holds, as the last post or update left it.
type PostedMessage struct {
	Channel, TS string
	Message     slacksurface.Message
}

// NewFakeSlack returns an empty transport.
func NewFakeSlack() *FakeSlack {
	return &FakeSlack{messages: map[string]*PostedMessage{}, views: map[string]slack.ModalViewRequest{}}
}

func messageKey(channel, ts string) string { return channel + "/" + ts }

// PostMessage implements slacksurface.Poster.
func (f *FakeSlack) PostMessage(_ context.Context, channel string, m slacksurface.Message) (string, error) {
	if err := slacksurface.ValidateMessage(m); err != nil {
		return "", fmt.Errorf("the message would not be accepted by Slack: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	ts := fmt.Sprintf("1700000000.%06d", f.seq)
	key := messageKey(channel, ts)
	f.messages[key] = &PostedMessage{Channel: channel, TS: ts, Message: m}
	f.order = append(f.order, key)
	return ts, nil
}

// UpdateMessage implements slacksurface.Poster.
func (f *FakeSlack) UpdateMessage(_ context.Context, channel, ts string, m slacksurface.Message) error {
	if err := slacksurface.ValidateMessage(m); err != nil {
		return fmt.Errorf("the update would not be accepted by Slack: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.messages[messageKey(channel, ts)]
	if !ok {
		return fmt.Errorf("message_not_found: %s %s", channel, ts)
	}
	cur.Message = m
	return nil
}

// OpenView implements slacksurface.Poster.
func (f *FakeSlack) OpenView(_ context.Context, _ string, v slack.ModalViewRequest) (string, error) {
	if err := slacksurface.ValidateModal(v); err != nil {
		return "", fmt.Errorf("the modal would not be accepted by Slack: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := fmt.Sprintf("V%06d", f.seq)
	f.views[id] = v
	return id, nil
}

// UpdateView implements slacksurface.Poster.
func (f *FakeSlack) UpdateView(_ context.Context, viewID string, v slack.ModalViewRequest) error {
	if err := slacksurface.ValidateModal(v); err != nil {
		return fmt.Errorf("the modal update would not be accepted by Slack: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.views[viewID]; !ok {
		return fmt.Errorf("view_not_found: %s", viewID)
	}
	f.views[viewID] = v
	return nil
}

// PostEphemeral implements slacksurface.Poster. Cliff only whispers to say an action did not go through, so the record
// run treats every one as a failure (Problems).
func (f *FakeSlack) PostEphemeral(_ context.Context, _, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ephemerals = append(f.ephemerals, text)
	return nil
}

// FindMessage implements slacksurface.Poster: the earliest message carrying the metadata.
func (f *FakeSlack) FindMessage(_ context.Context, channel string, _ time.Time, meta slacksurface.MessageMeta) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, key := range f.order {
		m := f.messages[key]
		if m != nil && m.Channel == channel && m.Message.Meta != nil && *m.Message.Meta == meta {
			return m.TS, true, nil
		}
	}
	return "", false, nil
}

// DeleteMessage implements slacksurface.Poster.
func (f *FakeSlack) DeleteMessage(_ context.Context, channel, ts string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.messages, messageKey(channel, ts))
	return nil
}

// Problems are the whispers Cliff sent since the last call: each says an action failed.
func (f *FakeSlack) Problems() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.ephemerals
	f.ephemerals = nil
	return out
}

// Message is the message as it stands now.
func (f *FakeSlack) Message(channel, ts string) (slacksurface.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.messages[messageKey(channel, ts)]
	if !ok {
		return slacksurface.Message{}, false
	}
	return m.Message, true
}

// View is the modal as it stands now (the loading modal is replaced once core answered).
func (f *FakeSlack) View(id string) (slack.ModalViewRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.views[id]
	return v, ok
}

// LastView is the most recently opened modal and its id.
func (f *FakeSlack) LastView() (string, slack.ModalViewRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	best := ""
	for id := range f.views {
		if id > best {
			best = id
		}
	}
	v, ok := f.views[best]
	return best, v, ok
}

// Button is one action button of a message: its action id, label and the target its value carries.
type Button struct {
	ActionID string
	Label    string
	Value    string
	Target   slacksurface.Target
}

// Buttons lists the action buttons of a message in order. It reads the blocks through their JSON form, which is exactly
// what Slack would receive.
func Buttons(m slacksurface.Message) []Button {
	raw, err := json.Marshal(m.Blocks)
	if err != nil {
		return nil
	}
	var blocks []struct {
		Type     string `json:"type"`
		Elements []struct {
			Type     string          `json:"type"`
			ActionID string          `json:"action_id"`
			Value    string          `json:"value"`
			Text     json.RawMessage `json:"text"` // an object on a button, a string on a context element
		} `json:"elements"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []Button
	for _, b := range blocks {
		if b.Type != "actions" {
			continue
		}
		for _, e := range b.Elements {
			if e.Type != "button" || e.Value == "" {
				continue
			}
			t, err := slacksurface.DecodeTarget(e.Value)
			if err != nil {
				continue
			}
			var text struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(e.Text, &text)
			label := text.Text
			out = append(out, Button{ActionID: e.ActionID, Label: label, Value: e.Value, Target: t})
		}
	}
	return out
}

// ModalInputs reads the text inputs of a modal: block id to its initial value.
func ModalInputs(v slack.ModalViewRequest) map[string]string {
	out := map[string]string{}
	for _, b := range v.Blocks.BlockSet {
		in, ok := b.(*slack.InputBlock)
		if !ok {
			continue
		}
		if el, ok := in.Element.(*slack.PlainTextInputBlockElement); ok {
			out[in.BlockID] = el.InitialValue
		}
	}
	return out
}

var _ slacksurface.Poster = (*FakeSlack)(nil)
