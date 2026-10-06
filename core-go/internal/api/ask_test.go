package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/ask"
)

// Ask Cliff HTTP surface: every response is checked against contracts/openapi/core.yaml with a fake service.

type fakeAsk struct {
	askReq     ask.Request
	actionReq  ask.ActionRequest
	toolTok    string
	toolName   string
	toolArgs   map[string]any
	progressID string
	err        error
}

func (f *fakeAsk) Ask(_ context.Context, r ask.Request) (ask.Answer, error) {
	f.askReq = r
	url := "http://web.test/episodes/e1"
	trace := "http://web.test/ask/traces/tr_1"
	return ask.Answer{AnswerMarkdown: "MedTech wants a security review.", Citations: []ask.Citation{{CallID: "t1", Tool: "episode", Label: "Episode", URL: &url}},
		ProposedAction: &ask.ProposedAction{Kind: "play_next", Summary: "Release the next event", Target: "MedTech Advances (event 2 of 3)", RequiresConfirmation: true},
		TraceID:        "tr_1", TraceURL: &trace, Steps: 1}, f.err
}

func (f *fakeAsk) Progress(turnID string) (ask.Progress, error) {
	f.progressID = turnID
	return ask.Progress{State: ask.ProgressWorking, Lines: []string{"Reading MedTech's state as of Nov 9…"}}, f.err
}

func (f *fakeAsk) Trace(_ context.Context, id string) (ask.Trace, error) {
	cost := 0.01
	return ask.Trace{ID: id, ThreadRef: "D1:", Question: "q", AnswerMarkdown: "a", CreatedAt: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
		Model: "m", Steps: []ask.TraceStep{{N: 1, Tool: "account_state", Args: map[string]any{"account": "MedTech"}, OK: true, Output: map[string]any{"stage": "Negotiation"}}},
		TokensIn: 10, TokensOut: 5, CostUSD: &cost, DurationMS: 1200}, f.err
}

func (f *fakeAsk) RunAction(_ context.Context, r ask.ActionRequest) (ask.ActionResult, error) {
	f.actionReq = r
	cont := ask.Answer{AnswerMarkdown: "The buyer asked for a security review.", Citations: []ask.Citation{}, TraceID: "tr_2", Steps: 2}
	return ask.ActionResult{Status: "done", MessageMarkdown: "Released event 2 of 3.", Continuation: &cont}, f.err
}

func (f *fakeAsk) RunTool(_ context.Context, token, tool string, args map[string]any) (ask.ToolResult, error) {
	f.toolTok, f.toolName, f.toolArgs = token, tool, args
	return ask.ToolResult{Tool: tool, OK: true, Data: map[string]any{"name": "MedTech"}, Links: []ask.Link{{Label: "Account", URL: "http://web.test/accounts/a"}}}, f.err
}

func askHandler(t *testing.T, svc AskService) http.Handler {
	t.Helper()
	h, err := NewHandler(&fakeService{}, testToken, nil, WithAsk(svc))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func post(h http.Handler, path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustValid(t *testing.T, spec *openapitest.Spec, method, path string, status int, body []byte) {
	t.Helper()
	if err := spec.Response(method, path, status, body); err != nil {
		t.Fatalf("%s %s %d does not match the contract: %v\n%s", method, path, status, err, body)
	}
}

func TestAskEndpointsConformToTheContract(t *testing.T) {
	spec, err := openapitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAsk{}
	h := askHandler(t, f)
	bearer := "Bearer " + testToken

	body := `{"text":"What changed at MedTech on Nov 9?","channel_kind":"dm","user":"U1","thread_ref":"D1:1.2","turn_id":"turn-12345678"}`
	if err := spec.Request("POST", "/ask", []byte(body)); err != nil {
		t.Fatalf("the request is not in the contract: %v", err)
	}
	rec := post(h, "/ask", bearer, body)
	if rec.Code != 200 {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body)
	}
	mustValid(t, spec, "POST", "/ask", 200, rec.Body.Bytes())
	if f.askReq.User != "U1" || f.askReq.ChannelKind != "dm" || f.askReq.ThreadRef != "D1:1.2" {
		t.Errorf("service got %+v", f.askReq)
	}

	abody := `{"kind":"play_next","user":"U1","thread_ref":"D1:1.2","turn_id":"turn-12345678"}`
	if err := spec.Request("POST", "/ask/actions", []byte(abody)); err != nil {
		t.Fatalf("the request is not in the contract: %v", err)
	}
	rec = post(h, "/ask/actions", bearer, abody)
	if rec.Code != 200 {
		t.Fatalf("action: %d %s", rec.Code, rec.Body)
	}
	mustValid(t, spec, "POST", "/ask/actions", 200, rec.Body.Bytes())
	if f.actionReq.ThreadRef != "D1:1.2" || f.actionReq.TurnID != "turn-12345678" {
		t.Errorf("service got %+v", f.actionReq)
	}
}

func get(h http.Handler, path, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAskProgressAndTraceConformToTheContract(t *testing.T) {
	spec, err := openapitest.Load()
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAsk{}
	h := askHandler(t, f)
	bearer := "Bearer " + testToken

	rec := get(h, "/ask/turns/turn-12345678/progress", bearer)
	if rec.Code != 200 || f.progressID != "turn-12345678" {
		t.Fatalf("progress: %d %s", rec.Code, rec.Body)
	}
	mustValid(t, spec, "GET", "/ask/turns/{turn_id}/progress", 200, rec.Body.Bytes())

	rec = get(h, "/ask/traces/tr_1", bearer)
	if rec.Code != 200 {
		t.Fatalf("trace: %d %s", rec.Code, rec.Body)
	}
	mustValid(t, spec, "GET", "/ask/traces/{trace_id}", 200, rec.Body.Bytes())

	for _, path := range []string{"/ask/turns/turn-12345678/progress", "/ask/traces/tr_1"} {
		if rec := get(h, path, ""); rec.Code != 401 {
			t.Errorf("%s without a token: %d", path, rec.Code)
		}
		if rec := post(h, path, bearer, `{}`); rec.Code != 405 {
			t.Errorf("POST %s: %d", path, rec.Code)
		}
	}
	h = askHandler(t, &fakeAsk{err: ask.ErrNotFound})
	rec = get(h, "/ask/traces/tr_gone", bearer)
	if rec.Code != 404 {
		t.Errorf("unknown trace: %d", rec.Code)
	}
	mustValid(t, spec, "GET", "/ask/traces/{trace_id}", 404, rec.Body.Bytes())
	h = askHandler(t, &fakeAsk{err: fmt.Errorf("%w: turn_id", ask.ErrBadRequest)})
	if rec := get(h, "/ask/turns/x/progress", bearer); rec.Code != 400 {
		t.Errorf("a malformed turn id: %d", rec.Code)
	}
}

func TestAskToolsUseTheAskTokenAndNotTheOperatorToken(t *testing.T) {
	spec, _ := openapitest.Load()
	f := &fakeAsk{}
	h := askHandler(t, f)
	rec := post(h, "/internal/ask/tools/account_state", "Bearer at1.token", `{"args":{"account":"MedTech"}}`)
	if rec.Code != 200 {
		t.Fatalf("tool: %d %s", rec.Code, rec.Body)
	}
	mustValid(t, spec, "POST", "/internal/ask/tools/{tool}", 200, rec.Body.Bytes())
	if f.toolTok != "at1.token" || f.toolName != "account_state" || f.toolArgs["account"] != "MedTech" {
		t.Errorf("service got %q %q %v", f.toolTok, f.toolName, f.toolArgs)
	}
	if rec := post(h, "/internal/ask/tools/account_state", "", `{}`); rec.Code != 401 {
		t.Errorf("no token: %d", rec.Code)
	}
	f.err = ask.ErrBadToken
	if rec := post(h, "/internal/ask/tools/account_state", "Bearer "+testToken, `{}`); rec.Code != 401 {
		t.Errorf("the operator token must not open the tools: %d", rec.Code)
	}
}

func TestAskRoutesRefuseCallersWithoutTheOperatorToken(t *testing.T) {
	h := askHandler(t, &fakeAsk{})
	for _, path := range []string{"/ask", "/ask/actions"} {
		if rec := post(h, path, "", `{}`); rec.Code != 401 {
			t.Errorf("%s without a token: %d", path, rec.Code)
		}
		if rec := post(h, path, "Bearer at1.wrong", `{}`); rec.Code != 401 {
			t.Errorf("%s with a wrong token: %d", path, rec.Code)
		}
	}
}

type providerDown struct{}

func (providerDown) Error() string             { return "workerclient: worker returned 424" }
func (providerDown) ProviderUnavailable() bool { return true }

func TestAskErrorsMapToTheEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		err    error
		status int
		code   string
	}{
		{"bad request", "/ask", fmt.Errorf("%w: text must be 1 to 2000 characters", ask.ErrBadRequest), 400, "bad_request"},
		{"no worker", "/ask", ask.ErrUnavailable, 503, "ask_unavailable"},
		{"provider down", "/ask", providerDown{}, 424, "provider_unavailable_nonretryable"},
		{"worker failed", "/ask", errors.New("workerclient: worker unreachable"), 502, "worker_error"},
		{"unexpected", "/ask", errors.New("boom secret detail"), 500, "internal"},
	}
	for _, c := range cases {
		h := askHandler(t, &fakeAsk{err: c.err})
		body := `{"text":"q","channel_kind":"dm","user":"U1"}`
		if c.path == "/ask/actions" {
			body = `{"kind":"play_next","user":"U9"}`
		}
		rec := post(h, c.path, "Bearer "+testToken, body)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), `"`+c.code+`"`) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "secret detail") {
			t.Errorf("%s leaked detail: %s", c.name, rec.Body)
		}
	}
	h := askHandler(t, &fakeAsk{err: ask.ErrUnknownTool})
	if rec := post(h, "/internal/ask/tools/send_email", "Bearer at1.t", `{}`); rec.Code != 404 {
		t.Errorf("unknown tool: %d", rec.Code)
	}
	h = askHandler(t, &fakeAsk{err: fmt.Errorf("%w: account is required", ask.ErrBadArguments)})
	if rec := post(h, "/internal/ask/tools/account_state", "Bearer at1.t", `{}`); rec.Code != 400 {
		t.Errorf("bad arguments: %d", rec.Code)
	}
}

func TestAskRejectsMalformedBodies(t *testing.T) {
	h := askHandler(t, &fakeAsk{})
	bearer := "Bearer " + testToken
	for name, body := range map[string]string{"empty": "", "unknown field": `{"text":"q","channel_kind":"dm","user":"U","x":1}`,
		"trailing": `{"text":"q","channel_kind":"dm","user":"U"} {}`, "not json": "nope"} {
		if rec := post(h, "/ask", bearer, body); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	big := `{"text":"` + strings.Repeat("x", MaxBodyBytes) + `","channel_kind":"dm","user":"U"}`
	if rec := post(h, "/ask", bearer, big); rec.Code != 400 || !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Errorf("oversized: %d %s", rec.Code, rec.Body)
	}
	rec := post(h, "/internal/ask/tools/x", "Bearer at1.t", `{"args":`+strings.Repeat("1", maxToolBody)+`}`)
	if rec.Code != 413 && rec.Code != 400 {
		t.Errorf("oversized tool body: %d", rec.Code)
	}
}

func TestAskIsNotMountedWithoutAService(t *testing.T) {
	h, _ := NewHandler(&fakeService{}, testToken, nil)
	if rec := post(h, "/ask", "Bearer "+testToken, `{}`); rec.Code != 404 {
		t.Errorf("without a service: %d", rec.Code)
	}
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithAsk(nil)); err == nil {
		t.Error("a nil ask service must be refused")
	}
	rec := httptest.NewRecorder()
	askHandler(t, &fakeAsk{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ask", http.NoBody))
	if rec.Code != 405 {
		t.Errorf("GET /ask: %d", rec.Code)
	}
}
