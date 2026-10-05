package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute"
)

type fakeDisputes struct {
	doc     []byte
	created bool
	err     error
	gotID   string
	gotReq  evaldispute.Request
}

func (f *fakeDisputes) Dispute(_ context.Context, id string, req evaldispute.Request) ([]byte, bool, error) {
	f.gotID, f.gotReq = id, req
	return f.doc, f.created, f.err
}

const disputePath = "/eval-results/0e1a0000-0000-4000-8000-000000000901/disputes"
const disputeBody = `{"reason":"should warn","expected_verdict":"warn","surface":"web","actor_label":"Dana"}`

func disputeHandler(t *testing.T, svc EvalDisputeService) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewHandler(&fakeService{}, testToken, slog.New(slog.NewTextHandler(logs, nil)), WithEvalDisputes(svc))
	if err != nil {
		t.Fatal(err)
	}
	return h, logs
}

func TestWithEvalDisputesRefusesANilService(t *testing.T) {
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithEvalDisputes(nil)); err == nil {
		t.Fatal("a nil dispute service must be refused")
	}
}

func TestDisputeStatusFollowsCreated(t *testing.T) {
	for _, tc := range []struct {
		created bool
		want    int
	}{{true, http.StatusCreated}, {false, http.StatusOK}} {
		f := &fakeDisputes{doc: []byte(`{"id":"x"}` + "\n\n"), created: tc.created}
		h, _ := disputeHandler(t, f)
		rec := do(h, "POST", disputePath, disputeBody, authed())
		if rec.Code != tc.want || rec.Body.String() != "{\"id\":\"x\"}\n" {
			t.Fatalf("created=%v: %d %q", tc.created, rec.Code, rec.Body.String())
		}
		if f.gotID != "0e1a0000-0000-4000-8000-000000000901" || f.gotReq.Reason != "should warn" || *f.gotReq.ExpectedVerdict != "warn" {
			t.Fatalf("the handler passed %q %+v", f.gotID, f.gotReq)
		}
	}
}

func TestDisputeErrorsMapToTheContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", evaldispute.ErrNotFound, http.StatusNotFound, codeNotFound},
		{"refused", &evaldispute.RefusedError{Code: evaldispute.CodeExpectedEqualsVerdict, Message: "same"}, http.StatusUnprocessableEntity, evaldispute.CodeExpectedEqualsVerdict},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout"},
		{"internal", errors.New("pq: relation secret_table at 10.0.0.3"), http.StatusInternalServerError, codeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := disputeHandler(t, &fakeDisputes{err: tc.err})
			rec := do(h, "POST", disputePath, disputeBody, authed())
			if rec.Code != tc.status || decodeEnvelope(t, rec).Error.Code != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "10.0.0.3") {
				t.Fatal("internal detail leaked into the response")
			}
			if tc.name == "internal" && !strings.Contains(logs.String(), "eval dispute failed") {
				t.Fatal("the internal error was not logged")
			}
		})
	}
}

func TestDisputeRequiresTheTokenAndAStrictBody(t *testing.T) {
	f := &fakeDisputes{}
	h, _ := disputeHandler(t, f)
	if rec := do(h, "POST", disputePath, disputeBody, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do(h, "POST", disputePath, `{"reason":"x","verdict":"pass"}`, authed()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if rec := do(h, "PUT", disputePath, disputeBody, authed()); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Fatalf("PUT: %d allow=%q", rec.Code, rec.Header().Get("Allow"))
	}
	if f.gotID != "" {
		t.Fatal("a refused request reached the service")
	}
}
