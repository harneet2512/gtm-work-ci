package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/harneet2512/gtm-work/core-go/internal/outbox"
)

// OutboxService is the event feed surfaces react to; *outbox.Store implements it (HAR-117).
type OutboxService interface {
	Pending(ctx context.Context, consumer, topic string, limit int) ([]outbox.Event, error)
	Ack(ctx context.Context, consumer string, eventID int64) error
}

// WithOutbox serves GET /outbox/events and POST /outbox/events/{event_id}/ack.
func WithOutbox(o OutboxService) Option {
	return func(s *server) error {
		if o == nil {
			return errors.New("api: outbox service is required")
		}
		s.outbox = o
		return nil
	}
}

func (s *server) routeOutbox(mux *http.ServeMux) {
	if s.outbox == nil {
		return
	}
	mux.HandleFunc("GET /outbox/events", s.operator(s.listOutbox))
	mux.HandleFunc("/outbox/events", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST /outbox/events/{event_id}/ack", s.operator(s.ackOutbox))
	mux.HandleFunc("/outbox/events/{event_id}/ack", methodNotAllowed(http.MethodPost))
}

func (s *server) listOutbox(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > outbox.MaxLimit { // an explicit 0 is a mistake, not "the default"
			writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(outbox.MaxLimit), nil)
			return
		}
		limit = n
	}
	events, err := s.outbox.Pending(r.Context(), q.Get("consumer"), q.Get("topic"), limit)
	if err != nil {
		s.outboxFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

func (s *server) ackOutbox(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("event_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, codeBadRequest, "event_id must be a positive integer", nil)
		return
	}
	if err := s.outbox.Ack(r.Context(), r.URL.Query().Get("consumer"), id); err != nil {
		s.outboxFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// outboxFailed maps an outbox error to the envelope; internal detail stays in the server log.
func (s *server) outboxFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, outbox.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid consumer, topic or limit", nil)
	case errors.Is(err, outbox.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	default:
		s.log.ErrorContext(r.Context(), "outbox failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
