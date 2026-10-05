package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute"
)

// EvalDisputeService is what POST /eval-results/{eval_result_id}/disputes needs; *evaldispute.Service
// implements it. It returns the EvalDispute JSON and whether the dispute is new.
type EvalDisputeService interface {
	Dispute(ctx context.Context, resultID string, req evaldispute.Request) (doc []byte, created bool, err error)
}

// WithEvalDisputes serves POST /eval-results/{eval_result_id}/disputes ("this eval is wrong", HAR-97 E19).
func WithEvalDisputes(svc EvalDisputeService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: eval dispute service is required")
		}
		s.disputes = svc
		return nil
	}
}

func (s *server) routeEvalDisputes(mux *http.ServeMux) {
	if s.disputes == nil {
		return
	}
	const path = "/eval-results/{eval_result_id}/disputes"
	mux.HandleFunc("POST "+path, s.operator(s.postDispute))
	mux.HandleFunc(path, methodNotAllowed(http.MethodPost))
}

func (s *server) postDispute(w http.ResponseWriter, r *http.Request) {
	var req evaldispute.Request
	if !decodeBody(w, r, &req, "eval dispute request") {
		return
	}
	doc, created, err := s.disputes.Dispute(r.Context(), r.PathValue("eval_result_id"), req)
	var refused *evaldispute.RefusedError
	switch {
	case err == nil:
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(append(bytes.TrimSpace(doc), '\n'))
	case errors.Is(err, evaldispute.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.As(err, &refused):
		writeError(w, http.StatusUnprocessableEntity, refused.Code, refused.Message, nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "eval dispute ended early", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request did not finish in time", nil)
	default:
		s.log.ErrorContext(r.Context(), "eval dispute failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
