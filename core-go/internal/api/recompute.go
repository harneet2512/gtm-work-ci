package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
)

// RecomputationService derives what a human edit invalidated, recomputed and preserved (HAR-97 E11);
// *recompute.Service implements it.
type RecomputationService interface {
	Recomputation(ctx context.Context, runID string) (recompute.Invalidation, error)
}

// WithRecomputation serves GET /runs/{run_id}/recomputation.
func WithRecomputation(svc RecomputationService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: recomputation service is required")
		}
		s.recomputation = svc
		return nil
	}
}

func (s *server) routeRecomputation(mux *http.ServeMux) {
	if s.recomputation == nil {
		return
	}
	mux.HandleFunc("GET /runs/{run_id}/recomputation", s.operator(s.getRecomputation))
	mux.HandleFunc("/runs/{run_id}/recomputation", methodNotAllowed(http.MethodGet))
}

func (s *server) getRecomputation(w http.ResponseWriter, r *http.Request) {
	doc, err := s.recomputation.Recomputation(r.Context(), r.PathValue("run_id"))
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store") // the answer moves with the send
		writeJSON(w, http.StatusOK, doc)
	case errors.Is(err, recompute.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "recomputation request ended early", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request did not finish in time", nil)
	default:
		s.log.ErrorContext(r.Context(), "recomputation request failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
