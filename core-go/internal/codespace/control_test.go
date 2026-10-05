package codespace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "control-token-0123456789"

func newControl(t *testing.T, r *rig) (*Control, *httptest.Server) {
	t.Helper()
	r.seeded()
	_ = WriteMarker(r.ops.Paths.ActiveFile(), SlotCase1)
	src := newSource(r)
	c := &Control{Ops: r.ops, Status: src, Token: testToken, Now: func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }}
	src.Job = c.Job
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	return c, srv
}

func call(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHealthzIsOpenAndEverythingElseNeedsTheToken(t *testing.T) {
	_, srv := newControl(t, newRig(t))
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz = %v %v", resp, err)
	}
	_ = resp.Body.Close()
	for _, tc := range []struct{ method, path string }{{"GET", "/status"}, {"POST", "/handoff"}} {
		for name, token := range map[string]string{"none": "", "wrong": "nope", "prefix": testToken[:5]} {
			if code, _ := call(t, srv, tc.method, tc.path, token, `{}`); code != http.StatusUnauthorized {
				t.Errorf("%s %s with %s token = %d, want 401", tc.method, tc.path, name, code)
			}
		}
	}
	// A control service created without a token refuses everything (never open by default).
	open := httptest.NewServer((&Control{}).Handler())
	defer open.Close()
	if code, _ := call(t, open, "GET", "/status", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("empty token = %d", code)
	}
}

func TestStatusEndpointReturnsTheReadinessDocument(t *testing.T) {
	_, srv := newControl(t, newRig(t))
	code, body := call(t, srv, "GET", "/status", testToken, "")
	if code != 200 || body["ready"] != true || body["message"] != "All systems ready" || body["active_case"] != "case1" {
		t.Fatalf("status = %d %v", code, body)
	}
}

func TestHandoffAnswersWithTheNextCaseAndIsTheOnlyOperation(t *testing.T) {
	r := newRig(t)
	_, srv := newControl(t, r)
	r.plat.invisible["man-1"] = "released"
	code, body := call(t, srv, "POST", "/handoff", testToken, `{"manifest_id":"man-1"}`)
	if code != http.StatusOK || body["manifest_id"] != "man-2" || body["account_id"] != "acct-2" || body["slot"] != "case2" {
		t.Fatalf("handoff = %d %v", code, body)
	}
	for _, gone := range []string{"/reset", "/case"} {
		if code, _ := call(t, srv, "POST", gone, testToken, `{}`); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s must not exist any more (no operator controls), got %d", gone, code)
		}
	}
}

func TestHandoffErrorsAreTold(t *testing.T) {
	r := newRig(t)
	c, srv := newControl(t, r)
	for _, tc := range []struct {
		name, body string
		want       int
		code       string
	}{
		{"bad json", `not json`, http.StatusBadRequest, "bad_request"},
		{"extra field", `{"manifest_id":"man-1","x":1}`, http.StatusBadRequest, "bad_request"},
		{"missing manifest", `{}`, http.StatusBadRequest, "bad_request"},
		{"unknown manifest", `{"manifest_id":"man-9"}`, http.StatusNotFound, "unknown_manifest"},
		{"not played yet", `{"manifest_id":"man-1"}`, http.StatusConflict, "not_played"},
		{"last case", `{"manifest_id":"man-2"}`, http.StatusConflict, "no_next_case"},
	} {
		code, body := call(t, srv, "POST", "/handoff", testToken, tc.body)
		if code != tc.want {
			t.Errorf("%s: status %d, want %d (%v)", tc.name, code, tc.want, body)
			continue
		}
		if e, _ := body["error"].(map[string]any); e["code"] != tc.code {
			t.Errorf("%s: error = %v, want code %s", tc.name, e, tc.code)
		}
	}
	r.plat.failStart = errors.New("core will not start")
	r.plat.invisible["man-1"] = "released"
	code, body := call(t, srv, "POST", "/handoff", testToken, `{"manifest_id":"man-1"}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("a failed activation = %d %v", code, body)
	}
	if msg, _ := body["error"].(map[string]any)["message"].(string); strings.Contains(msg, "core will not start") {
		t.Fatalf("the response carries no internal detail: %q", msg)
	}
	if job := c.Job(); job == nil || job.Running || !strings.Contains(job.Error, "core will not start") || job.Step != "Failed" {
		t.Fatalf("the failure is kept in the job for the logs: %+v", job)
	}
}

func TestASecondHandoffWhileOneRunsIsRefused(t *testing.T) {
	r := newRig(t)
	c, srv := newControl(t, r)
	r.plat.invisible["man-1"] = "released"
	gate := make(chan struct{})
	var once sync.Once
	c.Ops.P = blockingPlatform{Platform: r.plat, gate: gate, once: &once}
	first := make(chan int, 1)
	go func() {
		code, _ := call(t, srv, "POST", "/handoff", testToken, `{"manifest_id":"man-1"}`)
		first <- code
	}()
	deadline := time.Now().Add(5 * time.Second)
	for c.Job() == nil || !c.Job().Running {
		if time.Now().After(deadline) {
			t.Fatal("the handoff never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	code, body := call(t, srv, "POST", "/handoff", testToken, `{"manifest_id":"man-1"}`)
	if code != http.StatusConflict || body["error"].(map[string]any)["code"] != "busy" {
		t.Fatalf("second = %d %v, want 409 busy", code, body)
	}
	if _, st := call(t, srv, "GET", "/status", testToken, ""); st["phase"] != PhaseBusy || st["ready"] != false {
		t.Fatalf("status during a handoff = %v", st)
	}
	close(gate)
	if got := <-first; got != http.StatusOK {
		t.Fatalf("first = %d", got)
	}
}

// blockingPlatform holds the first StopCore until gate is closed, so a test can observe a running job.
type blockingPlatform struct {
	Platform
	gate chan struct{}
	once *sync.Once
}

func (b blockingPlatform) StopCore(ctx context.Context) error {
	b.once.Do(func() { <-b.gate })
	return b.Platform.StopCore(ctx)
}

func TestJobIsNilBeforeAnyOperation(t *testing.T) {
	if (&Control{}).Job() != nil {
		t.Fatal("no operation has run")
	}
}
