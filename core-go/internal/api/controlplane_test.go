package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// stubControl answers every control-plane read with err (or, when err is nil, an empty value), and records the arguments.
type stubControl struct {
	err       error
	gotFilter controlplane.EvalRunFilter
	gotA      string
	gotB      string
	gotID     string
}

func (s *stubControl) ListEvalRuns(_ context.Context, f controlplane.EvalRunFilter) (controlplane.EvalRunPage, error) {
	s.gotFilter = f
	return controlplane.EvalRunPage{Items: []controlplane.EvalRun{}}, s.err
}
func (s *stubControl) EvalFamilies(_ context.Context, id string) (controlplane.FamilySummary, error) {
	s.gotID = id
	return controlplane.FamilySummary{EvalRunID: id}, s.err
}
func (s *stubControl) CompareEvalRuns(_ context.Context, a, b string) (controlplane.Comparison, error) {
	s.gotA, s.gotB = a, b
	return controlplane.Comparison{}, s.err
}

func (s *stubControl) Episode(_ context.Context, id string) (controlplane.EpisodeSummary, error) {
	s.gotID = id
	return controlplane.EpisodeSummary{ID: id}, s.err
}
func (s *stubControl) Trace(_ context.Context, id string) (controlplane.EpisodeTrace, error) {
	s.gotID = id
	return controlplane.EpisodeTrace{EpisodeID: id, Spans: []controlplane.TraceSpan{}}, s.err
}
func (s *stubControl) KnowledgeMutations(_ context.Context, id string) (controlplane.KnowledgeMutations, error) {
	s.gotID = id
	return controlplane.KnowledgeMutations{EpisodeID: id, Items: []controlplane.KnowledgeMutation{}}, s.err
}

func (s *stubControl) Metrics(_ context.Context, id string) (controlplane.OperationalMetrics, error) {
	s.gotID = id
	return controlplane.OperationalMetrics{Classification: "metric", AgentRunID: id, Models: []string{}, Stages: []controlplane.StageMetrics{}}, s.err
}

func controlHandler(t *testing.T, svc ControlPlaneService) http.Handler {
	t.Helper()
	h, err := NewHandler(&fakeService{}, testToken, nil, WithControlPlane(svc))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestControlPlaneHandlersPassArgumentsThrough(t *testing.T) {
	stub := &stubControl{}
	h := controlHandler(t, stub)

	if rec := do(h, http.MethodGet, "/eval-runs?account_id="+testRun+"&limit=4&cursor=abc", "", authed()); rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if want := (controlplane.EvalRunFilter{AccountID: testRun, Limit: 4, Cursor: "abc"}); stub.gotFilter != want {
		t.Fatalf("filter = %+v", stub.gotFilter)
	}
	stub.gotID = ""
	if rec := do(h, http.MethodGet, "/eval-runs/"+testRun+"/families", "", authed()); rec.Code != 200 || stub.gotID != testRun {
		t.Fatalf("families: %d %q", rec.Code, stub.gotID)
	}
	for _, suffix := range []string{"", "/trace", "/knowledge-mutations", "/metrics"} {
		stub.gotID = ""
		if rec := do(h, http.MethodGet, "/episodes/"+testRun+suffix, "", authed()); rec.Code != 200 || stub.gotID != testRun {
			t.Fatalf("episode%s: %d %q", suffix, rec.Code, stub.gotID)
		}
	}
	if rec := do(h, http.MethodGet, "/eval-runs/compare?a=A&b=B", "", authed()); rec.Code != 200 || stub.gotA != "A" || stub.gotB != "B" {
		t.Fatalf("compare: %d %q %q (compare is a literal route, not an eval run id)", rec.Code, stub.gotA, stub.gotB)
	}
}

func TestControlPlaneErrorsMapToStatusesAndNeverLeakInternals(t *testing.T) {
	paths := []string{"/eval-runs", "/eval-runs/" + testRun + "/families", "/eval-runs/compare?a=x&b=y",
		"/episodes/" + testRun, "/episodes/" + testRun + "/trace", "/episodes/" + testRun + "/knowledge-mutations", "/episodes/" + testRun + "/metrics"}
	for _, c := range []struct {
		err  error
		want int
		code string
	}{
		{readmodel.ErrNotFound, 404, "not_found"},
		{readmodel.ErrInvalid, 400, "bad_request"},
		{errors.New("pq: relation \"eval_runs\" does not exist host=db.internal"), 500, "internal"},
	} {
		h := controlHandler(t, &stubControl{err: c.err})
		for _, path := range paths {
			rec := do(h, http.MethodGet, path, "", authed())
			if rec.Code != c.want || decodeEnvelope(t, rec).Error.Code != c.code {
				t.Errorf("%v on %s: %d %s", c.err, path, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "db.internal") {
				t.Errorf("body leaks internals: %s", rec.Body.String())
			}
		}
	}
}

func TestComparingRunsOfDifferentTriggersIsA422NotAnInternalError(t *testing.T) {
	h := controlHandler(t, &stubControl{err: fmt.Errorf("wrapped: %w", controlplane.ErrNotComparable)})
	rec := do(h, http.MethodGet, "/eval-runs/compare?a=x&b=y", "", authed())
	if rec.Code != http.StatusUnprocessableEntity || decodeEnvelope(t, rec).Error.Code != "not_comparable" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestControlPlaneRoutesNeedAuthAndGETAndTheService(t *testing.T) {
	h := controlHandler(t, &stubControl{})
	for _, path := range []string{"/eval-runs", "/eval-runs/compare?a=x&b=y", "/episodes/" + testRun,
		"/episodes/" + testRun + "/trace", "/episodes/" + testRun + "/knowledge-mutations", "/episodes/" + testRun + "/metrics"} {
		if rec := do(h, http.MethodGet, path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated: %d", path, rec.Code)
		}
		if rec := do(h, http.MethodPost, path, "", authed()); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, rec.Code)
		}
	}
	if rec := do(h, http.MethodGet, "/eval-runs?limit=0", "", authed()); rec.Code != 400 {
		t.Errorf("a bad limit: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/eval-runs?cursor="+strings.Repeat("a", maxCursorLen+1), "", authed()); rec.Code != 400 {
		t.Errorf("an oversized cursor: %d", rec.Code)
	}
	unwired, err := NewHandler(&fakeService{}, testToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := do(unwired, http.MethodGet, "/eval-runs", "", authed()); rec.Code != http.StatusNotFound {
		t.Errorf("an unwired control plane answers %d", rec.Code)
	}
	if _, err := NewHandler(&fakeService{}, testToken, nil, WithControlPlane(nil)); err == nil {
		t.Error("a nil control plane service was accepted")
	}
}
