package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

const testRun = "11111111-1111-4111-8111-111111111111"

// stubPulls records its calls and answers with a fixed result.
type stubPulls struct {
	calls  []stubCall
	packet corectx.Packet
	err    error
}

type stubCall struct {
	run    string
	tool   corectx.Tool
	params corectx.Params
}

func (s *stubPulls) Pull(_ context.Context, run string, tool corectx.Tool, p corectx.Params) (corectx.Packet, error) {
	s.calls = append(s.calls, stubCall{run, tool, p})
	return s.packet, s.err
}

// stubTokens accepts exactly one token, for exactly one run.
type stubTokens struct{ token, run string }

func (s stubTokens) Verify(token string, _ time.Time) (string, error) {
	if token != s.token {
		return "", errors.New("bad")
	}
	return s.run, nil
}

func ctxHandler(t *testing.T, pulls ContextService) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewHandler(&fakeService{}, testToken, slog.New(slog.NewTextHandler(logs, nil)),
		WithContext(pulls, stubTokens{token: "run-token", run: testRun}))
	if err != nil {
		t.Fatal(err)
	}
	return h, logs
}

func runAuth() map[string]string { return map[string]string{"Authorization": "Bearer run-token"} }

func TestContextEndpointPassesOnlyTheTokensRunAndTheTwoParameters(t *testing.T) {
	pulls := &stubPulls{packet: corectx.Packet{AccessID: 7, Tool: corectx.ToolState, Items: []json.RawMessage{}}}
	h, _ := ctxHandler(t, pulls)
	rec := do(h, "GET", "/internal/ctx/state?field_path=stage&limit=3", "", runAuth())
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, headers %v", rec.Code, rec.Header())
	}
	want := stubCall{testRun, corectx.ToolState, corectx.Params{FieldPath: "stage", Limit: 3}}
	if len(pulls.calls) != 1 || pulls.calls[0] != want {
		t.Fatalf("calls = %+v, want %+v", pulls.calls, want)
	}
}

func TestContextEndpointRefusesBeforeTouchingTheService(t *testing.T) {
	pulls := &stubPulls{}
	h, _ := ctxHandler(t, pulls)
	cases := []struct {
		name    string
		path    string
		headers map[string]string
		want    int
	}{
		{"no token", "/internal/ctx/state", nil, 401},
		{"operator token", "/internal/ctx/state", authed(), 401},
		{"basic scheme", "/internal/ctx/state", map[string]string{"Authorization": "Basic run-token"}, 401},
		{"empty bearer", "/internal/ctx/state", map[string]string{"Authorization": "Bearer "}, 401},
		{"account param", "/internal/ctx/state?account_id=" + testRun, runAuth(), 400},
		{"unknown param", "/internal/ctx/state?x=1", runAuth(), 400},
		{"repeated limit", "/internal/ctx/state?limit=1&limit=2", runAuth(), 400},
		{"limit zero", "/internal/ctx/state?limit=0", runAuth(), 400},
		{"limit too big", "/internal/ctx/state?limit=21", runAuth(), 400},
		{"limit text", "/internal/ctx/state?limit=ten", runAuth(), 400},
	}
	for _, c := range cases {
		rec := do(h, "GET", c.path, "", c.headers)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
		decodeEnvelope(t, rec)
	}
	if len(pulls.calls) != 0 {
		t.Fatalf("the service was called for refused requests: %+v", pulls.calls)
	}
	if rec := do(h, "HEAD", "/internal/ctx/state", "", runAuth()); rec.Code != 405 || len(pulls.calls) != 0 {
		t.Fatalf("HEAD spent a pull: %d, calls %d", rec.Code, len(pulls.calls))
	}
	if rec := do(h, "POST", "/internal/ctx/state", "", runAuth()); rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
		t.Fatalf("POST: %d %v", rec.Code, rec.Header())
	}
}

func TestContextErrorsMapToStatusesAndNeverLeakInternals(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{corectx.ErrUnknownTool, 404},
		{corectx.ErrBadParams, 400},
		{corectx.ErrRunInactive, 403},
		{corectx.ErrTooManyPulls, 429},
		{corectx.ErrAmbiguousWorldTime, 409},
		{corectx.ErrStateAfterCutoff, 409},
		{errors.New(`pq: password authentication failed for user "ghost" host=db.internal`), 500},
	}
	for _, c := range cases {
		h, logs := ctxHandler(t, &stubPulls{err: c.err})
		rec := do(h, "GET", "/internal/ctx/state", "", runAuth())
		if rec.Code != c.want {
			t.Errorf("%v: status %d, want %d", c.err, rec.Code, c.want)
		}
		decodeEnvelope(t, rec)
		for _, secret := range []string{"password", "db.internal", "run-token"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%v: body leaks %q: %s", c.err, secret, rec.Body.String())
			}
		}
		if strings.Contains(logs.String(), "run-token") {
			t.Errorf("%v: the log holds the token: %s", c.err, logs.String())
		}
		if c.want == 500 && (!strings.Contains(logs.String(), testRun) || !strings.Contains(logs.String(), "password")) {
			t.Errorf("the 500 must be logged server-side with the run id: %s", logs.String())
		}
	}
}

func TestOptionsValidateTheirArguments(t *testing.T) {
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithContext(nil, stubTokens{})); err == nil {
		t.Error("WithContext accepted a nil service")
	}
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithContext(&stubPulls{}, nil)); err == nil {
		t.Error("WithContext accepted a nil verifier")
	}
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithReads(nil)); err == nil {
		t.Error("WithReads accepted a nil service")
	}
}

func TestRoutesAreAbsentUntilWired(t *testing.T) {
	h, _ := newTestHandler(t, &fakeService{})
	for _, path := range []string{"/internal/ctx/state", "/accounts/" + testRun + "/state", "/runs/" + testRun + "/trace"} {
		if rec := do(h, "GET", path, "", authed()); rec.Code != 404 {
			t.Errorf("%s answered %d before being wired", path, rec.Code)
		}
	}
}

// stubReads fails every call with err.
type stubReads struct{ err error }

func (s stubReads) Account(context.Context, string) (readmodel.Account, error) {
	return readmodel.Account{}, s.err
}
func (s stubReads) State(context.Context, string, *time.Time) (json.RawMessage, error) {
	return nil, s.err
}
func (s stubReads) StateWorldAsOf(context.Context, string, time.Time) (json.RawMessage, error) {
	return nil, s.err
}
func (s stubReads) Diffs(context.Context, string, int, bool) ([]readmodel.StateDiff, error) {
	return nil, s.err
}
func (s stubReads) Signals(context.Context, string, int) ([]readmodel.Signal, error) {
	return nil, s.err
}
func (s stubReads) TimelinePage(context.Context, string, int, *time.Time, string) (readmodel.Timeline, error) {
	return readmodel.Timeline{}, s.err
}
func (s stubReads) Trace(context.Context, string) (readmodel.Trace, error) {
	return readmodel.Trace{}, s.err
}
func (s stubReads) Run(context.Context, string) (readmodel.Run, error) {
	return readmodel.Run{}, s.err
}

func TestReadErrorsMapToStatusesAndNeverLeakInternals(t *testing.T) {
	paths := []string{
		"/accounts/" + testRun, "/accounts/" + testRun + "/state", "/accounts/" + testRun + "/diffs",
		"/accounts/" + testRun + "/signals", "/accounts/" + testRun + "/timeline", "/runs/" + testRun + "/trace",
		"/runs/" + testRun,
	}
	cases := []struct {
		err  error
		want int
		code string
	}{
		{readmodel.ErrNotFound, 404, "not_found"},
		{readmodel.ErrNoState, 404, "state_not_computed"},
		{readmodel.ErrNoStateBefore, 404, "state_not_computed_before"},
		{readmodel.ErrInvalid, 400, "bad_request"},
		{errors.New("pq: relation \"account_state\" does not exist host=db.internal"), 500, "internal"},
	}
	for _, c := range cases {
		logs := &bytes.Buffer{}
		h, err := NewHandler(&fakeService{}, testToken, slog.New(slog.NewTextHandler(logs, nil)), WithReads(stubReads{err: c.err}))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			rec := do(h, "GET", path, "", authed())
			if rec.Code != c.want || decodeEnvelope(t, rec).Error.Code != c.code {
				t.Errorf("%v on %s: %d %s", c.err, path, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "db.internal") || strings.Contains(rec.Body.String(), "account_state") {
				t.Errorf("body leaks internals: %s", rec.Body.String())
			}
			if rec := do(h, "POST", path, "", authed()); rec.Code != 405 {
				t.Errorf("POST %s: %d", path, rec.Code)
			}
		}
	}
}
