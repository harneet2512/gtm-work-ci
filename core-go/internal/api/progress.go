package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// ProgressService reads the live pipeline progress (HAR-145); *stageevents.Reader implements it.
type ProgressService interface {
	Manifest(ctx context.Context, manifestID string) (stageevents.Progress, error)
	Run(ctx context.Context, runID string) (stageevents.Progress, error)
}

// WithProgress serves GET /replay/manifests/{manifest_id}/progress and GET /runs/{run_id}/progress.
//
// Polling, not a stream: every stage change is committed the moment it happens and the document is a few
// hundred bytes, so a poll loses nothing; and the server's write timeout and the operator middleware's
// request deadline are both finite, which a long-lived SSE response would have to work around.
func WithProgress(svc ProgressService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: progress service is required")
		}
		s.progress = svc
		return nil
	}
}

func (s *server) routeProgress(mux *http.ServeMux) {
	if s.progress == nil {
		return
	}
	mux.HandleFunc("GET /replay/manifests/{manifest_id}/progress", s.operator(s.getManifestProgress))
	mux.HandleFunc("/replay/manifests/{manifest_id}/progress", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("GET /runs/{run_id}/progress", s.operator(s.getRunProgress))
	mux.HandleFunc("/runs/{run_id}/progress", methodNotAllowed(http.MethodGet))
}

func (s *server) getManifestProgress(w http.ResponseWriter, r *http.Request) {
	doc, err := s.progress.Manifest(r.Context(), r.PathValue("manifest_id"))
	s.progressReply(w, r, doc, err, "manifest_not_found", "no such demo manifest")
}

func (s *server) getRunProgress(w http.ResponseWriter, r *http.Request) {
	doc, err := s.progress.Run(r.Context(), r.PathValue("run_id"))
	s.progressReply(w, r, doc, err, codeNotFound, "not found")
}

func (s *server) progressReply(w http.ResponseWriter, r *http.Request, doc stageevents.Progress, err error, notFoundCode, notFoundMsg string) {
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, doc)
	case errors.Is(err, stageevents.ErrNotFound):
		writeError(w, http.StatusNotFound, notFoundCode, notFoundMsg, nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "progress request ended early", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request did not finish in time", nil)
	default:
		s.log.ErrorContext(r.Context(), "progress request failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
