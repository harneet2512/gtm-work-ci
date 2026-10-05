package slackfake

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func postForm(t *testing.T, f *Server, method, auth string, form url.Values) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", f.APIURL()+method, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOnlyTokensShapedLikeSlacksAreAccepted(t *testing.T) {
	f := New()
	defer f.Close()

	if got := postForm(t, f, "chat.postMessage", "hunter2", url.Values{"channel": {"C1"}}); got["ok"] != false {
		t.Fatalf("an unauthenticated call succeeded: %v", got)
	}
	ok := postForm(t, f, "chat.postMessage", "xoxb-fake-token", url.Values{"channel": {"C1"}, "text": {"hi"}})
	if ok["ok"] != true || ok["ts"] == "" {
		t.Fatalf("authenticated call: %v", ok)
	}
	if calls := f.CallsOf("chat.postMessage"); len(calls) != 1 || calls[0].Channel != "C1" || calls[0].TS != ok["ts"] {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestEachPostGetsItsOwnTs(t *testing.T) {
	f := New()
	defer f.Close()
	a := postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}})
	b := postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}})
	if a["ts"] == b["ts"] {
		t.Fatalf("two messages share ts %v", a["ts"])
	}
}

func TestMessagesAreKeptWithTheirMetadataUpdatedAndDeleted(t *testing.T) {
	f := New()
	defer f.Close()
	meta := `{"event_type":"ghost_message","event_payload":{"subject_id":"s1","kind":"chooser"}}`
	a := postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}, "text": {"one"}, "metadata": {meta}})
	b := postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}, "text": {"two"}})
	postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C2"}, "text": {"elsewhere"}})
	if got := f.Visible("C1"); len(got) != 2 || got[0].Text != "one" || string(got[0].Metadata) != meta || got[1].Text != "two" {
		t.Fatalf("visible = %+v", got)
	}
	postForm(t, f, "chat.update", "xoxb-x", url.Values{"channel": {"C1"}, "ts": {a["ts"].(string)}, "text": {"one, edited"}})
	if got := f.Visible("C1"); got[0].Text != "one, edited" || string(got[0].Metadata) != meta {
		t.Fatalf("an update must change the text and keep the metadata: %+v", got[0])
	}
	postForm(t, f, "chat.delete", "xoxb-x", url.Values{"channel": {"C1"}, "ts": {b["ts"].(string)}})
	if got := f.Visible("C1"); len(got) != 1 || got[0].TS != a["ts"] {
		t.Fatalf("after a delete: %+v", got)
	}
	if got := f.Visible(""); len(got) != 2 {
		t.Fatalf("every channel: %+v", got)
	}
}

func TestHistoryListsVisibleMessagesNewestFirstWithMetadataOnlyOnRequestAndHonoursOldest(t *testing.T) {
	f := New()
	defer f.Close()
	meta := `{"event_type":"ghost_message","event_payload":{"subject_id":"s1","kind":"bi"}}`
	postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}, "text": {"old"}})
	cut := time.Now().Add(10 * time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	postForm(t, f, "chat.postMessage", "xoxb-x", url.Values{"channel": {"C1"}, "text": {"new"}, "metadata": {meta}})

	type hist struct {
		OK       bool `json:"ok"`
		Messages []struct {
			Text     string          `json:"text"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"messages"`
	}
	ask := func(form url.Values) hist {
		raw, _ := json.Marshal(postForm(t, f, "conversations.history", "xoxb-x", form))
		var h hist
		_ = json.Unmarshal(raw, &h)
		return h
	}
	all := ask(url.Values{"channel": {"C1"}})
	if !all.OK || len(all.Messages) != 2 || all.Messages[0].Text != "new" || len(all.Messages[0].Metadata) != 0 {
		t.Fatalf("without include_all_metadata: %+v", all)
	}
	withMeta := ask(url.Values{"channel": {"C1"}, "include_all_metadata": {"1"}})
	var got, want map[string]any
	if json.Unmarshal(withMeta.Messages[0].Metadata, &got) != nil || json.Unmarshal([]byte(meta), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("with include_all_metadata: %s", withMeta.Messages[0].Metadata)
	}
	since := ask(url.Values{"channel": {"C1"}, "oldest": {strconv.FormatFloat(float64(cut.UnixNano())/1e9, 'f', 6, 64)}, "include_all_metadata": {"1"}})
	if len(since.Messages) != 1 || since.Messages[0].Text != "new" {
		t.Fatalf("oldest must drop the earlier message: %+v", since)
	}
	if other := ask(url.Values{"channel": {"C9"}}); len(other.Messages) != 0 {
		t.Fatalf("another channel: %+v", other)
	}
	if unauth := postForm(t, f, "conversations.history", "", url.Values{"channel": {"C1"}}); unauth["ok"] != false {
		t.Fatalf("an unauthenticated history call succeeded: %v", unauth)
	}
}
