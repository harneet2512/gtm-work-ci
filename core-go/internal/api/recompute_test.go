package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
)

type fakeRecomputation struct {
	doc    recompute.Invalidation
	err    error
	gotRun string
}

func (f *fakeRecomputation) Recomputation(_ context.Context, runID string) (recompute.Invalidation, error) {
	f.gotRun = runID
	return f.doc, f.err
}

const recomputePath = "/runs/0f0a0000-0000-4000-8000-000000000601/recomputation"

func recomputeHandler(t *testing.T, svc RecomputationService) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewHandler(&fakeService{}, testToken, slog.New(slog.NewTextHandler(logs, nil)), WithRecomputation(svc))
	if err != nil {
		t.Fatal(err)
	}
	return h, logs
}

func TestWithRecomputationRefusesANilService(t *testing.T) {
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithRecomputation(nil)); err == nil {
		t.Fatal("a nil recomputation service must be refused")
	}
}

func TestRecomputationServesTheDocumentWithEmptyListsAsArrays(t *testing.T) {
	f := &fakeRecomputation{doc: recompute.Invalidation{Status: recompute.Unedited, SemanticLabels: []string{}, Entries: []recompute.Entry{}, PreservedOverall: []recompute.SpanRef{}}}
	h, _ := recomputeHandler(t, f)
	rec := do(h, "GET", recomputePath, "", authed())
	if rec.Code != http.StatusOK || f.gotRun != "0f0a0000-0000-4000-8000-000000000601" {
		t.Fatalf("%d run %q", rec.Code, f.gotRun)
	}
	body := rec.Body.String()
	for _, want := range []string{`"entries":[]`, `"preserved_overall":[]`, `"status":"unedited"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestRecomputationNeedsTheOperatorTokenAndAGet(t *testing.T) {
	h, _ := recomputeHandler(t, &fakeRecomputation{})
	if rec := do(h, "GET", recomputePath, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	rec := do(h, "POST", recomputePath, "{}", authed())
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("POST: %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestRecomputationErrorsMapToTheContractWithoutLeakingDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not found", recompute.ErrNotFound, http.StatusNotFound, codeNotFound},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout"},
		{"internal", errors.New("pq: relation secret_table at 10.0.0.3"), http.StatusInternalServerError, codeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := recomputeHandler(t, &fakeRecomputation{err: tc.err})
			rec := do(h, "GET", recomputePath, "", authed())
			if rec.Code != tc.status || decodeEnvelope(t, rec).Error.Code != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "secret_table") {
				t.Fatalf("the response leaks internal detail: %s", rec.Body.String())
			}
			if tc.status == http.StatusInternalServerError && !strings.Contains(logs.String(), "secret_table") {
				t.Errorf("the detail must reach the server log: %s", logs.String())
			}
		})
	}
}
