package api

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

type fakeProgress struct {
	doc        stageevents.Progress
	err        error
	gotKind    string
	gotID      string
	manifestID string
}

func (f *fakeProgress) Manifest(_ context.Context, id string) (stageevents.Progress, error) {
	f.gotKind, f.gotID = "manifest", id
	return f.doc, f.err
}

func (f *fakeProgress) Run(_ context.Context, id string) (stageevents.Progress, error) {
	f.gotKind, f.gotID = "run", id
	return f.doc, f.err
}

const (
	progressManifest = "0d3a0000-0000-4000-8000-000000000501"
	progressRun      = "0f0a0000-0000-4000-8000-000000000601"
)

func progressHandler(t *testing.T, svc ProgressService) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	h, err := NewHandler(&fakeService{}, testToken, slog.New(slog.NewTextHandler(logs, nil)), WithProgress(svc))
	if err != nil {
		t.Fatal(err)
	}
	return h, logs
}

func sampleProgress() stageevents.Progress {
	stages := make([]stageevents.StageDoc, 0, len(stageevents.Order))
	for _, s := range stageevents.Order {
		stages = append(stages, stageevents.StageDoc{Stage: s, Status: stageevents.Waiting, EvalResultIDs: []string{}})
	}
	return stageevents.Progress{Scope: "manifest", AccountID: "0a0c0000-0000-4000-8000-000000000001", Overall: stageevents.NotStarted,
		Stages: stages, GeneratedAt: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)}
}

func TestWithProgressRefusesANilService(t *testing.T) {
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithProgress(nil)); err == nil {
		t.Fatal("a nil progress service must be refused")
	}
}

func TestProgressEndpointsServeTheDocumentAndAreNeverCached(t *testing.T) {
	for _, tc := range []struct{ path, kind, id string }{
		{"/replay/manifests/" + progressManifest + "/progress", "manifest", progressManifest},
		{"/runs/" + progressRun + "/progress", "run", progressRun},
	} {
		f := &fakeProgress{doc: sampleProgress()}
		h, _ := progressHandler(t, f)
		rec := do(h, "GET", tc.path, "", authed())
		if rec.Code != http.StatusOK || f.gotKind != tc.kind || f.gotID != tc.id {
			t.Fatalf("%s: %d kind %q id %q", tc.path, rec.Code, f.gotKind, f.gotID)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q: a poll must always see the latest stage", tc.path, got)
		}
		if !strings.Contains(rec.Body.String(), `"overall":"not_started"`) || !strings.Contains(rec.Body.String(), `"stage":"cliff"`) {
			t.Errorf("%s: body = %s", tc.path, rec.Body.String())
		}
	}
}

func TestProgressEndpointsNeedTheOperatorTokenAndAGet(t *testing.T) {
	h, _ := progressHandler(t, &fakeProgress{doc: sampleProgress()})
	for _, path := range []string{"/replay/manifests/" + progressManifest + "/progress", "/runs/" + progressRun + "/progress"} {
		if rec := do(h, "GET", path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", path, rec.Code)
		}
		rec := do(h, "POST", path, "{}", authed())
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
			t.Errorf("%s POST: %d allow %q", path, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestProgressErrorsMapToTheContractWithoutLeakingDetail(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		err        error
		status     int
		code       string
	}{
		{"unknown manifest", "/replay/manifests/" + progressManifest + "/progress", stageevents.ErrNotFound, http.StatusNotFound, "manifest_not_found"},
		{"unknown run", "/runs/" + progressRun + "/progress", stageevents.ErrNotFound, http.StatusNotFound, codeNotFound},
		{"timeout", "/runs/" + progressRun + "/progress", context.DeadlineExceeded, http.StatusGatewayTimeout, "timeout"},
		{"internal", "/runs/" + progressRun + "/progress", errors.New("pq: relation secret_table at 10.0.0.3"), http.StatusInternalServerError, codeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := progressHandler(t, &fakeProgress{err: tc.err})
			rec := do(h, "GET", tc.path, "", authed())
			if rec.Code != tc.status || decodeEnvelope(t, rec).Error.Code != tc.code {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "secret_table") || strings.Contains(rec.Body.String(), "10.0.0.3") {
				t.Fatalf("the response leaks internal detail: %s", rec.Body.String())
			}
			if tc.status == http.StatusInternalServerError && !strings.Contains(logs.String(), "secret_table") {
				t.Errorf("the detail must reach the server log: %s", logs.String())
			}
		})
	}
}
