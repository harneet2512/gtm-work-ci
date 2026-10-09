package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ctxgraph"
)

// GraphService is what the graph endpoints need; *ctxgraph.Service implements it.
type GraphService interface {
	Neighborhood(ctx context.Context, accountID string, p ctxgraph.Params) (ctxgraph.View, error)
	// NeighborhoodAsOf is the neighborhood as the world stood strictly before t, rebuilt from Postgres (ADR-0019).
	NeighborhoodAsOf(ctx context.Context, accountID string, p ctxgraph.Params, t time.Time) (ctxgraph.View, error)
	EventDiff(ctx context.Context, eventID string) (ctxgraph.EventDiff, error)
	Lag(ctx context.Context) (ctxgraph.Lag, error)
}

// WithGraph serves GET /accounts/{id}/graph, GET /events/{id}/graph-diff and GET /graph/projection.
func WithGraph(g GraphService) Option {
	return func(s *server) error {
		if g == nil {
			return errors.New("api: graph service is required")
		}
		s.graph = g
		return nil
	}
}

func (s *server) routeGraph(mux *http.ServeMux) {
	if s.graph == nil {
		return
	}
	for path, h := range map[string]http.HandlerFunc{
		"/accounts/{account_id}/graph":  s.getGraph,
		"/events/{event_id}/graph-diff": s.getGraphDiff,
		"/graph/projection":             s.getGraphProjection,
	} {
		mux.HandleFunc("GET "+path, s.operator(h))
		mux.HandleFunc(path, methodNotAllowed(http.MethodGet))
	}
}

func (s *server) getGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var p ctxgraph.Params
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > ctxgraph.MaxSectionLimit {
			writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(ctxgraph.MaxSectionLimit), nil)
			return
		}
		p.SectionLimit = n
	}
	if raw := q.Get("include_closed"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeBadRequest, "include_closed must be true or false", nil)
			return
		}
		p.IncludeClosed = v
	}
	var view ctxgraph.View
	var err error
	if q.Has("world_as_of") {
		t, ok := parseTime(w, q, "world_as_of")
		if !ok {
			return
		}
		view, err = s.graph.NeighborhoodAsOf(r.Context(), r.PathValue("account_id"), p, t)
	} else {
		view, err = s.graph.Neighborhood(r.Context(), r.PathValue("account_id"), p)
	}
	if err != nil {
		s.graphFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *server) getGraphDiff(w http.ResponseWriter, r *http.Request) {
	d, err := s.graph.EventDiff(r.Context(), r.PathValue("event_id"))
	if err != nil {
		s.graphFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) getGraphProjection(w http.ResponseWriter, r *http.Request) {
	lag, err := s.graph.Lag(r.Context())
	if err != nil {
		s.graphFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, lag)
}

// graphFailed maps a graph error to the envelope; internal detail stays in the server log.
func (s *server) graphFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ctxgraph.ErrAccountUnknown), errors.Is(err, ctxgraph.ErrEventUnknown):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.Is(err, ctxgraph.ErrGraphUnavailable):
		s.log.ErrorContext(r.Context(), "graph read failed", "error", err, "path", r.URL.Path)
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "graph_unavailable", "the graph database is not reachable", nil)
	default:
		s.log.ErrorContext(r.Context(), "graph read failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
