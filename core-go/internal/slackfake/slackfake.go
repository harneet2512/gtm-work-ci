// Package slackfake is a fake Slack for the no-human smoke run (HAR-137 section 12): the Web API methods
// the Slack surface uses plus a Socket Mode WebSocket through which a driver pushes the same interaction
// envelopes a human's clicks would produce. It accepts only tokens that look like Slack's (xoxb-, xapp-)
// and uses no real credential.
package slackfake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Call is one Web API request the fake received.
type Call struct {
	Method   string          `json:"method"`
	Channel  string          `json:"channel,omitempty"`
	TS       string          `json:"ts,omitempty"` // chat.postMessage: the ts the fake assigned
	Text     string          `json:"text,omitempty"`
	Blocks   json.RawMessage `json:"blocks,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	View     json.RawMessage `json:"view,omitempty"`
	Trigger  string          `json:"trigger_id,omitempty"`
}

// Message is a channel message the fake holds: what chat.postMessage created, chat.update changed and chat.delete
// has not removed. Posted is the wall-clock time of the post: conversations.history filters `oldest` on it (the
// fake's ts values are synthetic and sit in the past, so they cannot be compared with a real clock).
type Message struct {
	Channel  string
	TS       string
	Text     string
	Blocks   json.RawMessage
	Metadata json.RawMessage
	Posted   time.Time
}

// Server is the fake Slack.
type Server struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []Call
	msgs   []Message // visible messages, in post order
	nextTS int
	wmu    sync.Mutex
	conn   *websocket.Conn
	acks   chan map[string]any
	ready  chan struct{}
	envSeq int
}

// New starts the fake on a loopback port.
func New() *Server {
	f := &Server{nextTS: 100, acks: make(chan map[string]any, 64), ready: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/apps.connections.open", f.open)
	mux.HandleFunc("/ws", f.ws)
	for _, m := range []string{"chat.postMessage", "chat.update", "views.open", "views.update", "chat.postEphemeral", "chat.delete", "conversations.history"} {
		method := m
		mux.HandleFunc("/"+method, func(w http.ResponseWriter, r *http.Request) { f.api(method, w, r) })
	}
	f.srv = httptest.NewServer(mux)
	return f
}

// Close stops the server.
func (f *Server) Close() { f.srv.Close() }

// APIURL is the base URL to give slack.OptionAPIURL (it ends in "/").
func (f *Server) APIURL() string { return f.srv.URL + "/" }

func (f *Server) open(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer xapp-") {
		http.Error(w, "bad app token", http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"})
}

func (f *Server) ws(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.wmu.Lock()
	f.conn = c
	_ = c.WriteJSON(map[string]any{"type": "hello", "num_connections": 1, "connection_info": map[string]any{"app_id": "A1"}, "debug_info": map[string]any{}})
	f.wmu.Unlock()
	close(f.ready)
	for {
		var ack map[string]any
		if err := c.ReadJSON(&ack); err != nil {
			return
		}
		f.acks <- ack
	}
}

func (f *Server) api(method string, w http.ResponseWriter, r *http.Request) {
	call := Call{Method: method}
	_ = r.ParseForm() // form-encoded methods carry the token as a field; JSON ones use the header
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer xoxb-") && !strings.HasPrefix(r.Form.Get("token"), "xoxb-") {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "not_authed"})
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.Unmarshal(body["trigger_id"], &call.Trigger)
		call.View = body["view"]
	} else {
		call.Channel, call.TS, call.Text = r.Form.Get("channel"), r.Form.Get("ts"), r.Form.Get("text")
		call.Blocks = json.RawMessage(r.Form.Get("blocks"))
		if md := r.Form.Get("metadata"); md != "" {
			call.Metadata = json.RawMessage(md)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if method == "conversations.history" {
		f.history(w, r.Form.Get("channel"), r.Form.Get("oldest"), r.Form.Get("include_all_metadata") == "1")
		f.calls = append(f.calls, call)
		return
	}
	switch method {
	case "chat.postMessage":
		f.nextTS++
		call.TS = fmt.Sprintf("1700000000.%06d", f.nextTS)
		f.msgs = append(f.msgs, Message{Channel: call.Channel, TS: call.TS, Text: call.Text, Blocks: call.Blocks, Metadata: call.Metadata, Posted: time.Now()})
	case "chat.update":
		for i := range f.msgs {
			if f.msgs[i].Channel == call.Channel && f.msgs[i].TS == call.TS {
				f.msgs[i].Text, f.msgs[i].Blocks = call.Text, call.Blocks
				if call.Metadata != nil {
					f.msgs[i].Metadata = call.Metadata
				}
			}
		}
	case "chat.delete":
		kept := f.msgs[:0]
		for _, m := range f.msgs {
			if m.Channel != call.Channel || m.TS != call.TS {
				kept = append(kept, m)
			}
		}
		f.msgs = kept
	}
	f.calls = append(f.calls, call)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": call.Channel, "ts": call.TS, "message_ts": call.TS, "view": map[string]any{"id": "V1"}})
}

// history answers conversations.history like Slack: the channel's visible messages posted after `oldest` (epoch
// seconds), newest first, with their metadata only when include_all_metadata is set. The caller holds f.mu.
func (f *Server) history(w http.ResponseWriter, channel, oldest string, withMetadata bool) {
	var floor time.Time
	if oldest != "" {
		if sec, err := strconv.ParseFloat(oldest, 64); err == nil {
			floor = time.Unix(0, int64(sec*1e9))
		}
	}
	out := []map[string]any{}
	for i := len(f.msgs) - 1; i >= 0; i-- {
		m := f.msgs[i]
		if m.Channel != channel || !m.Posted.After(floor) {
			continue
		}
		entry := map[string]any{"type": "message", "user": "UBOT", "ts": m.TS, "text": m.Text}
		if len(m.Blocks) > 0 {
			entry["blocks"] = m.Blocks
		}
		if withMetadata && len(m.Metadata) > 0 {
			entry["metadata"] = m.Metadata
		}
		out = append(out, entry)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": out, "has_more": false})
}

// Visible returns the messages of the channel that were posted and not deleted, in post order.
func (f *Server) Visible(channel string) []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Message
	for _, m := range f.msgs {
		if channel == "" || m.Channel == channel {
			out = append(out, m)
		}
	}
	return out
}

// Calls returns every call so far, in order.
func (f *Server) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsOf returns the calls to one Web API method.
func (f *Server) CallsOf(method string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// WaitConnected blocks until the Socket Mode client has connected.
func (f *Server) WaitConnected(ctx context.Context) error {
	select {
	case <-f.ready:
		return nil
	case <-ctx.Done():
		return errors.New("slackfake: the socket mode client never connected")
	}
}

// Push sends an interactive envelope and waits for the client's acknowledgement of it.
func (f *Server) Push(ctx context.Context, payload any) (map[string]any, error) {
	f.mu.Lock()
	f.envSeq++
	id := fmt.Sprintf("env-%d", f.envSeq)
	f.mu.Unlock()
	env := map[string]any{"type": "interactive", "envelope_id": id, "accepts_response_payload": false, "payload": payload}
	f.wmu.Lock()
	err := f.conn.WriteJSON(env)
	f.wmu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("slackfake: push: %w", err)
	}
	select {
	case ack := <-f.acks:
		if ack["envelope_id"] != id {
			return nil, fmt.Errorf("slackfake: ack for %v, want %s", ack["envelope_id"], id)
		}
		return ack, nil
	case <-ctx.Done():
		return nil, errors.New("slackfake: no ack for envelope " + id)
	}
}

// WaitCalls waits until n calls of method have been recorded and returns them.
func (f *Server) WaitCalls(ctx context.Context, method string, n int) ([]Call, error) {
	for {
		if c := f.CallsOf(method); len(c) >= n {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("slackfake: timed out waiting for %d %s calls (have %d)", n, method, len(f.CallsOf(method)))
		case <-time.After(5 * time.Millisecond):
		}
	}
}
