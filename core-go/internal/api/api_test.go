package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const testToken = "test-token-0123456789-0123456789"

type fakeService struct {
	calls  int
	gotCtx context.Context
	gotEv  normalize.SourceEvent
	result ingest.Result
	err    error
}

func (f *fakeService) Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	f.calls++
	f.gotCtx, f.gotEv = ctx, ev
	return f.result, f.err
}

type ctxKey struct{}

func newTestHandler(t *testing.T, svc IngestService) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewHandler(svc, testToken, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return h, logs
}

func do(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func authed() map[string]string { return map[string]string{"Authorization": "Bearer " + testToken} }

const validEvent = `{"source_system":"email","source_object_id":"m1","source_event_key":"received","occurred_at":"2026-09-29T15:42:00Z","connector":"gmail","payload":{"kind":"email"}}`

type envelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code == "" || env.Error.Message == "" {
		t.Fatalf("body is not an error envelope: %q (%v)", rec.Body.String(), err)
	}
	return env
}

func TestNewHandlerValidatesArguments(t *testing.T) {
	if _, err := NewHandler(nil, testToken, nil); err == nil {
		t.Error("nil service accepted")
	}
	if _, err := NewHandler(&fakeService{}, "", nil); err == nil {
		t.Error("empty token accepted: the API would be open")
	}
	if _, err := NewHandler(&fakeService{}, testToken, nil); err != nil {
		t.Errorf("nil logger must default: %v", err)
	}
}

func TestHealthzNeedsNoAuth(t *testing.T) {
	h, _ := newTestHandler(t, &fakeService{})
	rec := do(h, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestWrongMethodsAndUnknownPathsUseTheErrorEnvelope(t *testing.T) {
	h, _ := newTestHandler(t, &fakeService{})
	cases := []struct {
		method, path string
		wantCode     int
		wantAllow    string
	}{
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET"},
		{http.MethodGet, "/ingest", http.StatusMethodNotAllowed, "POST"},
		{http.MethodPut, "/ingest", http.StatusMethodNotAllowed, "POST"},
		{http.MethodGet, "/nope", http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := do(h, tc.method, tc.path, "", authed())
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if got := rec.Header().Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
			decodeEnvelope(t, rec)
		})
	}
}

func TestIngestRequiresBearerToken(t *testing.T) {
	cases := map[string]map[string]string{
		"no header":          nil,
		"empty header":       {"Authorization": ""},
		"basic scheme":       {"Authorization": "Basic " + testToken},
		"bare token":         {"Authorization": testToken},
		"wrong token":        {"Authorization": "Bearer nope"},
		"token prefix":       {"Authorization": "Bearer " + testToken[:5]},
		"token plus suffix":  {"Authorization": "Bearer " + testToken + "x"},
		"empty bearer":       {"Authorization": "Bearer "},
		"bearer only":        {"Authorization": "Bearer"},
		"token in query too": {"X-Api-Key": testToken},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{}
			h, _ := newTestHandler(t, svc)
			rec := do(h, http.MethodPost, "/ingest?token="+testToken, validEvent, headers)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("missing WWW-Authenticate")
			}
			if env := decodeEnvelope(t, rec); env.Error.Code != "unauthorized" {
				t.Errorf("code = %s", env.Error.Code)
			}
			if svc.calls != 0 {
				t.Fatal("service called without authentication")
			}
		})
	}
}

func TestBearerSchemeIsCaseInsensitive(t *testing.T) {
	h, _ := newTestHandler(t, &fakeService{})
	rec := do(h, http.MethodPost, "/ingest", validEvent, map[string]string{"Authorization": "bearer " + testToken})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestTokenMatchesIsExactAndLengthSafe(t *testing.T) {
	if !tokenMatches("abc", "abc") || tokenMatches("abc", "abd") || tokenMatches("abc", "abcd") || tokenMatches("", "abc") || tokenMatches("abc", "") {
		t.Fatal("tokenMatches wrong")
	}
}

func TestIngestNewEventReturns201WithResult(t *testing.T) {
	acct := "11111111-1111-1111-1111-111111111111"
	due := time.Date(2026, 3, 1, 10, 0, 3, 0, time.UTC)
	svc := &fakeService{result: ingest.Result{
		ActivityID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", SourceEventID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		AccountID: &acct, RecomputeDueAt: &due,
	}}
	h, _ := newTestHandler(t, svc)

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(validEvent)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"activity_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "source_event_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		"duplicate": false, "account_id": acct, "recompute_due_at": "2026-03-01T10:00:03Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if svc.gotCtx.Value(ctxKey{}) != "marker" {
		t.Error("request context not propagated to the service")
	}
	ev := svc.gotEv
	if ev.SourceSystem != "email" || ev.SourceObjectID != "m1" || ev.SourceEventKey != "received" || ev.Connector != "gmail" ||
		ev.OccurredAt == nil || !ev.OccurredAt.Equal(time.Date(2026, 9, 29, 15, 42, 0, 0, time.UTC)) || string(ev.Payload) != `{"kind":"email"}` {
		t.Errorf("decoded event = %+v", ev)
	}
}

func TestIngestDuplicateReturns200AndNullsStayNull(t *testing.T) {
	svc := &fakeService{result: ingest.Result{ActivityID: "a", SourceEventID: "s", Duplicate: true}}
	h, _ := newTestHandler(t, svc)
	rec := do(h, http.MethodPost, "/ingest", validEvent, authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["duplicate"] != true {
		t.Errorf("duplicate = %v", got["duplicate"])
	}
	if v, present := got["account_id"]; !present || v != nil {
		t.Errorf("account_id should be an explicit null, got %v (present=%v)", v, present)
	}
	if v, present := got["recompute_due_at"]; !present || v != nil {
		t.Errorf("recompute_due_at should be an explicit null, got %v", v)
	}
}

func TestIngestRejectsMalformedRequestsWith400(t *testing.T) {
	cases := map[string]string{
		"empty body":            ``,
		"not json":              `hello`,
		"truncated json":        `{"source_system":`,
		"json array":            `[]`,
		"unknown top-level key": `{"source_system":"email","source_object_id":"m1","source_event_key":"received","payload":{},"extra":1}`,
		"wrong type":            `{"source_system":5,"source_object_id":"m1","source_event_key":"received","payload":{}}`,
		"bad timestamp":         `{"source_system":"email","source_object_id":"m1","source_event_key":"received","occurred_at":"soon","payload":{}}`,
		"trailing data":         validEvent + `{"again":true}`,
		"trailing garbage":      validEvent + ` x`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeService{}
			h, _ := newTestHandler(t, svc)
			rec := do(h, http.MethodPost, "/ingest", body, authed())
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
			}
			if env := decodeEnvelope(t, rec); env.Error.Code != "bad_request" {
				t.Errorf("code = %s", env.Error.Code)
			}
			if svc.calls != 0 {
				t.Error("service called for a malformed request")
			}
		})
	}
}

// bodyOfSize builds a valid event whose JSON encoding is exactly n bytes.
func bodyOfSize(t *testing.T, n int) string {
	t.Helper()
	const head = `{"source_system":"email","source_object_id":"m1","source_event_key":"received","payload":{"pad":"`
	const tail = `"}}`
	pad := n - len(head) - len(tail)
	if pad < 0 {
		t.Fatalf("n too small: %d", n)
	}
	return head + strings.Repeat("x", pad) + tail
}

func TestIngestBodyLimitIsOneMiB(t *testing.T) {
	if MaxBodyBytes != 1<<20 {
		t.Fatalf("MaxBodyBytes = %d, want 1 MiB", MaxBodyBytes)
	}
	svc := &fakeService{result: ingest.Result{ActivityID: "a", SourceEventID: "s"}}
	h, _ := newTestHandler(t, svc)

	if rec := do(h, http.MethodPost, "/ingest", bodyOfSize(t, MaxBodyBytes), authed()); rec.Code != http.StatusCreated {
		t.Fatalf("a body of exactly 1 MiB must be accepted, got %d: %.200s", rec.Code, rec.Body.String())
	}
	svc.calls = 0
	rec := do(h, http.MethodPost, "/ingest", bodyOfSize(t, MaxBodyBytes+1), authed())
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("1 MiB + 1 byte: status = %d, want 400", rec.Code)
	}
	if env := decodeEnvelope(t, rec); env.Error.Code != "payload_too_large" {
		t.Errorf("code = %s", env.Error.Code)
	}
	if svc.calls != 0 {
		t.Error("service called for an oversized body")
	}
}

func TestIngestMapsValidationErrorsTo422(t *testing.T) {
	cases := map[string]error{
		"invalid":     &normalize.ValidationError{Code: normalize.CodeInvalidEvent, Message: "payload.kind is wrong"},
		"unsupported": &normalize.ValidationError{Code: normalize.CodeUnsupportedEvent, Message: "no mapping"},
		"wrapped":     fmt.Errorf("ingest: %w", &normalize.ValidationError{Code: normalize.CodeInvalidEvent, Message: "wrapped detail"}),
	}
	wantCode := map[string]string{"invalid": "invalid_event", "unsupported": "unsupported_event", "wrapped": "invalid_event"}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := newTestHandler(t, &fakeService{err: err})
			rec := do(h, http.MethodPost, "/ingest", validEvent, authed())
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", rec.Code)
			}
			env := decodeEnvelope(t, rec)
			if env.Error.Code != wantCode[name] {
				t.Errorf("code = %s, want %s", env.Error.Code, wantCode[name])
			}
			var verr *normalize.ValidationError
			_ = errors.As(err, &verr)
			if env.Error.Message != verr.Message {
				t.Errorf("message = %q, want the validation message %q", env.Error.Message, verr.Message)
			}
		})
	}
}

func TestIngestNeverLeaksInternalErrorsOn500(t *testing.T) {
	secret := `pq: password authentication failed for user "ghost" at host db.internal:5432 (postgres://ghost:hunter2@db.internal/x)`
	h, logs := newTestHandler(t, &fakeService{err: errors.New(secret)})
	rec := do(h, http.MethodPost, "/ingest", validEvent, authed())

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"hunter2", "password", "db.internal", "postgres://", "pq:"} {
		if strings.Contains(body, leak) {
			t.Errorf("500 body leaks %q: %s", leak, body)
		}
	}
	if env := decodeEnvelope(t, rec); env.Error.Code != "internal" {
		t.Errorf("code = %s", env.Error.Code)
	}
	if !strings.Contains(logs.String(), "db.internal") {
		t.Errorf("the detailed error must be logged server-side, logs: %s", logs.String())
	}
	if strings.Contains(logs.String(), testToken) {
		t.Error("logs contain the API token")
	}
}

func TestIngestPassesUnicodePayloadsThrough(t *testing.T) {
	svc := &fakeService{result: ingest.Result{ActivityID: "a", SourceEventID: "s"}}
	h, _ := newTestHandler(t, svc)
	body := `{"source_system":"slack","source_object_id":"#deal-café:1.1","source_event_key":"posted","payload":{"text":"héllo 🚀 O'Brien; DROP TABLE x;--"}}`
	if rec := do(h, http.MethodPost, "/ingest", body, authed()); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if svc.gotEv.SourceObjectID != "#deal-café:1.1" || !strings.Contains(string(svc.gotEv.Payload), `🚀`) {
		t.Errorf("event mangled: %+v", svc.gotEv)
	}
}

func TestIngestAcceptsLargeButLegalPayloadQuickly(t *testing.T) {
	svc := &fakeService{result: ingest.Result{ActivityID: "a", SourceEventID: "s"}}
	h, _ := newTestHandler(t, svc)
	start := time.Now()
	rec := do(h, http.MethodPost, "/ingest", bodyOfSize(t, 900*1024), authed())
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("900 KiB body took %v", d)
	}
}
