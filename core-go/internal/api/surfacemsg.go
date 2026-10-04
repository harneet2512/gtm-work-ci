package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/surfacemsg"
)

// codeTSConflict is the 409 of a second, different ts for one message ref.
const codeTSConflict = "ts_conflict"

// SurfaceMessageService keeps the create-only message refs (HAR-136); *surfacemsg.Store implements it.
type SurfaceMessageService interface {
	Get(ctx context.Context, subject, surface, kind string) (surfacemsg.Ref, error)
	Reserve(ctx context.Context, subject, surface, kind, channel string) (surfacemsg.Ref, bool, error)
	RecordTS(ctx context.Context, subject, surface, kind, ts string) (surfacemsg.Ref, error)
}

// WithSurfaceMessages serves GET /surface-messages/{subject_id}/{surface}/{kind} and its reservation and ts.
func WithSurfaceMessages(m SurfaceMessageService) Option {
	return func(s *server) error {
		if m == nil {
			return errors.New("api: surface message service is required")
		}
		s.surfaceMsgs = m
		return nil
	}
}

func (s *server) routeSurfaceMessages(mux *http.ServeMux) {
	if s.surfaceMsgs == nil {
		return
	}
	const base = "/surface-messages/{subject_id}/{surface}/{kind}"
	mux.HandleFunc("GET "+base, s.operator(s.getSurfaceMessage))
	mux.HandleFunc(base, methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST "+base+"/reservation", s.operator(s.reserveSurfaceMessage))
	mux.HandleFunc(base+"/reservation", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("PUT "+base+"/ts", s.operator(s.recordSurfaceMessageTS))
	mux.HandleFunc(base+"/ts", methodNotAllowed(http.MethodPut))
}

func (s *server) getSurfaceMessage(w http.ResponseWriter, r *http.Request) {
	ref, err := s.surfaceMsgs.Get(r.Context(), r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"))
	s.surfaceMessageReply(w, r, ref, err)
}

func (s *server) reserveSurfaceMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel string `json:"channel"`
	}
	if !decodeBody(w, r, &body, "reservation request") {
		return
	}
	ref, created, err := s.surfaceMsgs.Reserve(r.Context(), r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"), body.Channel)
	if err != nil {
		s.surfaceMessageReply(w, r, ref, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "message": ref})
}

func (s *server) recordSurfaceMessageTS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TS string `json:"ts"`
	}
	if !decodeBody(w, r, &body, "ts request") {
		return
	}
	ref, err := s.surfaceMsgs.RecordTS(r.Context(), r.PathValue("subject_id"), r.PathValue("surface"), r.PathValue("kind"), body.TS)
	s.surfaceMessageReply(w, r, ref, err)
}

// surfaceMessageReply writes the ref or maps the store's errors to the contract's statuses; internal detail
// stays in the server log.
func (s *server) surfaceMessageReply(w http.ResponseWriter, r *http.Request, ref surfacemsg.Ref, err error) {
	var conflict *surfacemsg.TSConflictError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, ref)
	case errors.Is(err, surfacemsg.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid subject, surface, kind, channel or ts", nil)
	case errors.Is(err, surfacemsg.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.As(err, &conflict):
		var ts any
		if conflict.Existing.TS != nil {
			ts = *conflict.Existing.TS
		}
		writeError(w, http.StatusConflict, codeTSConflict, "a different ts is already recorded for this message", map[string]any{"ts": ts})
	default:
		s.log.ErrorContext(r.Context(), "surface message failed", "error", err, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
