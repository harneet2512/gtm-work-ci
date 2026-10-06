package fakecore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func refsReq(t *testing.T, refs *Refs, method, path, body string) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	w := httptest.NewRecorder()
	refs.ServeHTTP(w, r)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s %s: %d %q is not JSON: %v", method, path, w.Code, w.Body.String(), err)
	}
	return w.Code, out
}

func TestRefsReserveIsAtomicAndIdempotentAndTSIsWriteOnce(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 10, 4, 7, 0, 0, 0, time.UTC) }
	refs := NewRefs(now)
	base := "/surface-messages/sub-1/slack/bi"

	code, _ := refsReq(t, refs, http.MethodGet, base, "")
	if code != http.StatusNotFound {
		t.Fatalf("unreserved slot must be 404, got %d", code)
	}
	code, out := refsReq(t, refs, http.MethodPost, base+"/reservation", `{"channel":"C1"}`)
	if code != http.StatusOK || out["created"] != true {
		t.Fatalf("first reservation must create: %d %v", code, out)
	}
	msg := out["message"].(map[string]any)
	if msg["ts"] != nil || msg["reserved_at"] != "2026-10-04T07:00:00Z" {
		t.Fatalf("a fresh reservation has ts null and the store clock: %v", msg)
	}
	code, out = refsReq(t, refs, http.MethodPost, base+"/reservation", `{"channel":"C1"}`)
	if code != http.StatusOK || out["created"] != false {
		t.Fatalf("a second reservation returns the slot unchanged: %d %v", code, out)
	}
	code, out = refsReq(t, refs, http.MethodPut, base+"/ts", `{"ts":"1.1"}`)
	if code != http.StatusOK || out["ts"] != "1.1" {
		t.Fatalf("first ts records: %d %v", code, out)
	}
	code, out = refsReq(t, refs, http.MethodPut, base+"/ts", `{"ts":"1.1"}`)
	if code != http.StatusOK || out["ts"] != "1.1" {
		t.Fatalf("the same ts is a no-op: %d %v", code, out)
	}
	code, out = refsReq(t, refs, http.MethodPut, base+"/ts", `{"ts":"2.2"}`)
	if code != http.StatusConflict || out["error"].(map[string]any)["code"] != "ts_conflict" {
		t.Fatalf("a different ts conflicts: %d %v", code, out)
	}
	code, _ = refsReq(t, refs, http.MethodPut, "/surface-messages/sub-2/slack/chooser/ts", `{"ts":"1.1"}`)
	if code != http.StatusNotFound {
		t.Fatalf("a ts for an unreserved slot is 404, got %d", code)
	}
	code, _ = refsReq(t, refs, http.MethodPut, base+"/ts", `{"ts":"not-slack"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("a malformed ts is 400, got %d", code)
	}
	code, _ = refsReq(t, refs, http.MethodPost, base+"/reservation", `{"channel":""}`)
	if code != http.StatusBadRequest {
		t.Fatalf("an empty channel is 400, got %d", code)
	}
	code, _ = refsReq(t, refs, http.MethodGet, "/other", "")
	if code != http.StatusNotFound {
		t.Fatalf("a non-refs path is 404, got %d", code)
	}
}
