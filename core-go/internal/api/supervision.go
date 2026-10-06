package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
)

// SupervisionService is what GET /episodes/{episode_id}/reactions needs; *reactions.Service
// implements it through SupervisionDoc.
type SupervisionService interface {
	SupervisionDoc(ctx context.Context, episodeID string) ([]byte, error)
}

// WithSupervision serves the episode supervision endpoint: the customer reactions and business
// outcomes detected after the episode's send (HAR-120; core.yaml EpisodeSupervision).
func WithSupervision(svc SupervisionService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: supervision service is required")
		}
		s.supervision = svc
		return nil
	}
}

func (s *server) routeSupervision(mux *http.ServeMux) {
	if s.supervision == nil {
		return
	}
	mux.HandleFunc("GET /episodes/{episode_id}/reactions", s.operator(s.getEpisodeReactions))
	mux.HandleFunc("/episodes/{episode_id}/reactions", methodNotAllowed(http.MethodGet))
}

func (s *server) getEpisodeReactions(w http.ResponseWriter, r *http.Request) {
	doc, err := s.supervision.SupervisionDoc(r.Context(), r.PathValue("episode_id"))
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(append(bytes.TrimSpace(doc), '\n'))
	case errors.Is(err, reactions.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "episode reactions request ended early", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request did not finish in time", nil)
	default:
		s.log.ErrorContext(r.Context(), "episode reactions request failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
