package slacksurface

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// apiCall is one Web API request the fake Slack server received.
type apiCall struct {
	Method  string
	Channel string
	TS      string
	Text    string
	Blocks  json.RawMessage
	View    json.RawMessage
	Trigger string
	User    string
}

// fakeSlack is a fake Slack: the Web API methods the adapter uses plus a Socket Mode WebSocket the
// test can push interaction envelopes through. It uses no real tokens.
type fakeSlack struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []apiCall
	nextTS int
	conn   *websocket.Conn
	wmu    sync.Mutex
	acks   chan map[string]any
	ready  chan struct{}
	envSeq int
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{t: t, nextTS: 100, acks: make(chan map[string]any, 64), ready: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/apps.connections.open", f.open)
	mux.HandleFunc("/ws", f.ws)
	for _, m := range []string{"chat.postMessage", "chat.update", "views.open", "views.update", "chat.postEphemeral", "chat.delete"} {
		method := m
		mux.HandleFunc("/"+method, func(w http.ResponseWriter, r *http.Request) { f.api(method, w, r) })
	}
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) apiURL() string { return f.srv.URL + "/" }

func (f *fakeSlack) open(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer xapp-") {
		http.Error(w, "bad app token", http.StatusUnauthorized)
		return
	}
	url := "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": url})
}

func (f *fakeSlack) ws(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	c, err := up.Upgrade(w, r, nil)
	if err != nil {
		f.t.Logf("upgrade: %v (path %s)", err, r.URL.String())
		return
	}
	f.wmu.Lock()
	f.conn = c
	f.wmu.Unlock()
	hello := map[string]any{"type": "hello", "num_connections": 1, "connection_info": map[string]any{"app_id": "A1"}, "debug_info": map[string]any{}}
	f.wmu.Lock()
	_ = c.WriteJSON(hello)
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

func (f *fakeSlack) api(method string, w http.ResponseWriter, r *http.Request) {
	call := apiCall{Method: method}
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
		call.Channel, call.TS, call.Text, call.User = r.Form.Get("channel"), r.Form.Get("ts"), r.Form.Get("text"), r.Form.Get("user")
		call.Blocks = json.RawMessage(r.Form.Get("blocks"))
	}
	f.mu.Lock()
	ts := call.TS
	if method == "chat.postMessage" {
		f.nextTS++
		ts = fmt.Sprintf("1700000000.%06d", f.nextTS)
	}
	call.TS = ts // for chat.postMessage: the ts the fake assigned
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": call.Channel, "ts": ts, "message_ts": ts, "view": map[string]any{"id": "V1"}})
}

// callsOf returns the recorded calls to one Web API method.
func (f *fakeSlack) callsOf(method string) []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []apiCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeSlack) waitConnected() {
	f.t.Helper()
	select {
	case <-f.ready:
	case <-time.After(5 * time.Second):
		f.t.Fatal("socket mode client never connected to the fake server")
	}
}

// push sends an interactive envelope and waits for the client's ack of it.
func (f *fakeSlack) push(payload any) map[string]any {
	f.t.Helper()
	f.envSeq++
	id := fmt.Sprintf("env-%d", f.envSeq)
	env := map[string]any{"type": "interactive", "envelope_id": id, "accepts_response_payload": false, "payload": payload}
	f.wmu.Lock()
	err := f.conn.WriteJSON(env)
	f.wmu.Unlock()
	if err != nil {
		f.t.Fatalf("push: %v", err)
	}
	select {
	case ack := <-f.acks:
		if ack["envelope_id"] != id {
			f.t.Fatalf("ack for %v, want %s", ack["envelope_id"], id)
		}
		return ack
	case <-time.After(5 * time.Second):
		f.t.Fatal("no ack for envelope " + id)
	}
	return nil
}

// waitCalls waits until n calls of method have been recorded.
func (f *fakeSlack) waitCalls(method string, n int) []apiCall {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := f.callsOf(method); len(c) >= n {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("timed out waiting for %d %s calls (have %d)", n, method, len(f.callsOf(method)))
	return nil
}
