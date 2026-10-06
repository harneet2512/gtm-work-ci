package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/controlplane"
)

// ControlPlaneService is what the HAR-145 control-plane reads need; *controlplane.Reader implements it. Every method is
// read-only and derived from rows that already exist (no eval runs here).
type ControlPlaneService interface {
	ListEvalRuns(ctx context.Context, f controlplane.EvalRunFilter) (controlplane.EvalRunPage, error)
	EvalFamilies(ctx context.Context, id string) (controlplane.FamilySummary, error)
	CompareEvalRuns(ctx context.Context, a, b string) (controlplane.Comparison, error)
	Episode(ctx context.Context, id string) (controlplane.EpisodeSummary, error)
	Trace(ctx context.Context, id string) (controlplane.EpisodeTrace, error)
	KnowledgeMutations(ctx context.Context, id string) (controlplane.KnowledgeMutations, error)
	Metrics(ctx context.Context, id string) (controlplane.OperationalMetrics, error)
}

// WithControlPlane serves the Ghost Eval Control Plane reads (core.yaml tag control-plane): GET /eval-runs,
// /eval-runs/{id}, /eval-runs/{id}/families, /eval-runs/compare, /episodes/{id}, /episodes/{id}/trace and
// /episodes/{id}/knowledge-mutations.
func WithControlPlane(svc ControlPlaneService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: control plane service is required")
		}
		s.control = svc
		return nil
	}
}

func (s *server) routeControlPlane(mux *http.ServeMux) {
	if s.control == nil {
		return
	}
	for path, h := range map[string]http.HandlerFunc{
		"/eval-runs":                                 s.listEvalRuns,
		"/eval-runs/compare":                         s.compareEvalRuns,
		"/eval-runs/{eval_run_id}/families":          s.getEvalRunFamilies,
		"/episodes/{episode_id}":                     s.getEpisode,
		"/episodes/{episode_id}/trace":               s.getEpisodeTrace,
		"/episodes/{episode_id}/knowledge-mutations": s.getKnowledgeMutations,
		"/episodes/{episode_id}/metrics":             s.getEpisodeMetrics,
	} {
		mux.HandleFunc("GET "+path, s.operator(h))
		mux.HandleFunc(path, methodNotAllowed(http.MethodGet))
	}
}

func (s *server) listEvalRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := parseLimit(w, q)
	if !ok {
		return
	}
	cursor, ok := parseCursor(w, r)
	if !ok {
		return
	}
	page, err := s.control.ListEvalRuns(r.Context(), controlplane.EvalRunFilter{AccountID: q.Get("account_id"), Limit: limit, Cursor: cursor})
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getEvalRunFamilies(w http.ResponseWriter, r *http.Request) {
	sum, err := s.control.EvalFamilies(r.Context(), r.PathValue("eval_run_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *server) compareEvalRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cmp, err := s.control.CompareEvalRuns(r.Context(), q.Get("a"), q.Get("b"))
	if errors.Is(err, controlplane.ErrNotComparable) {
		writeError(w, http.StatusUnprocessableEntity, "not_comparable",
			"the two runs were not triggered by the same event, so they cannot be compared", nil)
		return
	}
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cmp)
}

func (s *server) getEpisode(w http.ResponseWriter, r *http.Request) {
	ep, err := s.control.Episode(r.Context(), r.PathValue("episode_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ep)
}

func (s *server) getEpisodeTrace(w http.ResponseWriter, r *http.Request) {
	tr, err := s.control.Trace(r.Context(), r.PathValue("episode_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// getEpisodeMetrics serves the OperationalMetrics of the episode's run: a metric, never an eval.
func (s *server) getEpisodeMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := s.control.Metrics(r.Context(), r.PathValue("episode_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) getKnowledgeMutations(w http.ResponseWriter, r *http.Request) {
	ms, err := s.control.KnowledgeMutations(r.Context(), r.PathValue("episode_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}
