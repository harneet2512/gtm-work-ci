package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2"
)

// GateResultReader serves the stored gate results of an episode (*bucket2.Reader implements it).
type GateResultReader interface {
	GateResults(ctx context.Context, episodeID, gate string) ([]byte, error)
}

// WithGateResults serves GET /episodes/{episode_id}/gate-results: the persisted results of every gate, Bucket 1 (B1-B9),
// Bucket 2 (D1-D10) and Bucket 3 (S1-S5), keyed by episode and gate, optionally narrowed with ?gate=.
func WithGateResults(r GateResultReader) Option {
	return func(s *server) error {
		if r == nil {
			return errors.New("api: a gate result reader is required")
		}
		s.gateResults = r
		return nil
	}
}

func (s *server) routeGateResults(mux *http.ServeMux) {
	if s.gateResults == nil {
		return
	}
	const path = "/episodes/{episode_id}/gate-results"
	mux.HandleFunc("GET "+path, s.operator(s.getGateResults))
	mux.HandleFunc(path, methodNotAllowed(http.MethodGet))
}

func (s *server) getGateResults(w http.ResponseWriter, r *http.Request) {
	doc, err := s.gateResults.GateResults(r.Context(), r.PathValue("episode_id"), r.URL.Query().Get("gate"))
	switch {
	case errors.Is(err, bucket2.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.Is(err, bucket2.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid request parameter", nil)
	case err != nil:
		s.log.ErrorContext(r.Context(), "gate results read failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	default:
		writeRaw(w, http.StatusOK, doc)
	}
}
