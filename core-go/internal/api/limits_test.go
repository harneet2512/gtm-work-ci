package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

type funcService func(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error)

func (f funcService) Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error) {
	return f(ctx, ev)
}

func TestIngestRunsUnderADefaultDeadline(t *testing.T) {
	var remaining time.Duration
	var hasDeadline bool
	h, err := NewHandler(funcService(func(ctx context.Context, _ normalize.SourceEvent) (ingest.Result, error) {
		var dl time.Time
		dl, hasDeadline = ctx.Deadline()
		remaining = time.Until(dl)
		return ingest.Result{ActivityID: "a", SourceEventID: "s"}, nil
	}), testToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(h, http.MethodPost, "/ingest", validEvent, authed()); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if !hasDeadline || remaining <= 10*time.Second || remaining > IngestTimeout {
		t.Fatalf("deadline = %v (has=%v), want about %v", remaining, hasDeadline, IngestTimeout)
	}
	if IngestTimeout != 15*time.Second {
		t.Fatalf("IngestTimeout = %v", IngestTimeout)
	}
}

func TestIngestDeadlineAbortsASlowServiceWith500(t *testing.T) {
	h, err := NewHandler(funcService(func(ctx context.Context, _ normalize.SourceEvent) (ingest.Result, error) {
		<-ctx.Done()
		return ingest.Result{}, ctx.Err()
	}), testToken, nil, WithIngestTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rec := do(h, http.MethodPost, "/ingest", validEvent, authed())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("slow service held the request for %v", d)
	}
	if env := decodeEnvelope(t, rec); env.Error.Code != "internal" || strings.Contains(rec.Body.String(), "deadline") {
		t.Errorf("envelope = %+v body %s", env, rec.Body.String())
	}
}

func TestWithIngestTimeoutRejectsNonPositiveValues(t *testing.T) {
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithIngestTimeout(0)); err == nil {
		t.Fatal("zero timeout accepted")
	}
}

func TestBadRequestMessagesAreFixedAndNameOnlyTheField(t *testing.T) {
	cases := []struct {
		name, body, wantMsg string
	}{
		{"unknown field", `{"source_system":"email","source_object_id":"m","source_event_key":"k","payload":{},"extra":1}`, `request body has an unknown field "extra"`},
		{"wrong type", `{"source_system":5,"source_object_id":"m","source_event_key":"k","payload":{}}`, `field "source_system" has the wrong type`},
		{"syntax error", `{"source_system":`, `request body is not valid JSON`},
		{"garbage", `hello`, `request body is not valid JSON`},
		{"empty", ``, `request body is empty`},
		{"array", `[]`, `request body must be a JSON object`},
		{"trailing data", validEvent + `{}`, `request body must contain exactly one JSON object`},
		{"bad timestamp", `{"source_system":"email","source_object_id":"m","source_event_key":"k","occurred_at":"soon","payload":{}}`, `request body is not a valid SourceEvent`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHandler(t, &fakeService{})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/ingest", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+testToken)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", rec.Code)
			}
			env := decodeEnvelope(t, rec)
			if env.Error.Message != tc.wantMsg {
				t.Errorf("message = %q, want %q", env.Error.Message, tc.wantMsg)
			}
			for _, leak := range []string{"json:", "Go struct", "Go value", "parsing time", "offset"} {
				if strings.Contains(env.Error.Message, leak) {
					t.Errorf("message leaks decoder text %q: %s", leak, env.Error.Message)
				}
			}
		})
	}
}

func TestUnknownFieldNameIsBoundedAndQuoted(t *testing.T) {
	long := strings.Repeat("x", 500)
	body := `{"source_system":"email","source_object_id":"m","source_event_key":"k","payload":{},"` + long + `":1}`
	h, _ := newTestHandler(t, &fakeService{})
	rec := do(h, http.MethodPost, "/ingest", body, authed())
	env := decodeEnvelope(t, rec)
	if len(env.Error.Message) > 200 {
		t.Fatalf("message echoes an unbounded field name: %d bytes", len(env.Error.Message))
	}
}
