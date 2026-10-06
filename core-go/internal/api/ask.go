package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ask"
)

// AskTimeout is the deadline of one POST /ask: the worker's 120 s loop plus its answer. The server's default write
// timeout is 60 s, so this route extends its own write deadline.
const AskTimeout = 135 * time.Second

// AskActionTimeout is the deadline of one POST /ask/actions: a played event (55 s), the wait for its pipeline (90 s)
// and the resumed task (120 s).
const AskActionTimeout = 270 * time.Second

// maxToolBody bounds a tool call body.
const maxToolBody = 16 << 10

// AskService is Ask Cliff (*ask.Service implements it).
type AskService interface {
	Ask(ctx context.Context, req ask.Request) (ask.Answer, error)
	RunAction(ctx context.Context, req ask.ActionRequest) (ask.ActionResult, error)
	RunTool(ctx context.Context, token, tool string, args map[string]any) (ask.ToolResult, error)
	Progress(turnID string) (ask.Progress, error)
	Trace(ctx context.Context, id string) (ask.Trace, error)
}

// WithAsk serves POST /ask, POST /ask/actions and POST /internal/ask/tools/{tool} (contracts/openapi/core.yaml tag
// ask). The first two take the operator token; the tools take the ask token the service minted for one question.
func WithAsk(svc AskService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: an ask service is required")
		}
		s.ask = svc
		return nil
	}
}

func (s *server) routeAsk(mux *http.ServeMux) {
	if s.ask == nil {
		return
	}
	mux.HandleFunc("POST /ask", s.operatorWithin(AskTimeout, s.postAsk))
	mux.HandleFunc("/ask", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("POST /ask/actions", s.operatorWithin(AskActionTimeout, s.postAskAction))
	mux.HandleFunc("/ask/actions", methodNotAllowed(http.MethodPost))
	mux.HandleFunc("GET /ask/turns/{turn_id}/progress", s.operatorWithin(ReadTimeout, s.getAskProgress))
	mux.HandleFunc("/ask/turns/{turn_id}/progress", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("GET /ask/traces/{trace_id}", s.operatorWithin(ReadTimeout, s.getAskTrace))
	mux.HandleFunc("/ask/traces/{trace_id}", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST /internal/ask/tools/{tool}", s.postAskTool)
	mux.HandleFunc("/internal/ask/tools/{tool}", methodNotAllowed(http.MethodPost))
}

// decodeStrict reads one JSON object with no unknown fields; it writes the error response itself.
func decodeStrict(w http.ResponseWriter, r *http.Request, into any, what string, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeBodyErrorFor(w, err, what)
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeBodyErrorFor(w, errTrailingData, what)
		return false
	}
	return true
}

func (s *server) postAsk(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(AskTimeout + 5*time.Second))
	var req ask.Request
	if !decodeStrict(w, r, &req, "AskRequest", MaxBodyBytes) {
		return
	}
	ans, err := s.ask.Ask(r.Context(), req)
	if err != nil {
		s.askFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

func (s *server) getAskProgress(w http.ResponseWriter, r *http.Request) {
	p, err := s.ask.Progress(r.PathValue("turn_id"))
	if err != nil {
		s.askFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) getAskTrace(w http.ResponseWriter, r *http.Request) {
	t, err := s.ask.Trace(r.Context(), r.PathValue("trace_id"))
	if err != nil {
		s.askFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *server) postAskAction(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(AskActionTimeout + 5*time.Second))
	var req ask.ActionRequest
	if !decodeStrict(w, r, &req, "AskActionRequest", MaxBodyBytes) {
		return
	}
	res, err := s.ask.RunAction(r.Context(), req)
	if err != nil {
		s.askFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) postAskTool(w http.ResponseWriter, r *http.Request) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid ask token is required", nil)
		return
	}
	var body struct {
		Args map[string]any `json:"args"`
	}
	if !decodeStrict(w, r, &body, "tool call", maxToolBody) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ReadTimeout)
	defer cancel()
	res, err := s.ask.RunTool(ctx, token, r.PathValue("tool"), body.Args)
	if err != nil {
		s.askFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// askFailed maps a service error to the envelope; detail stays in the log.
func (s *server) askFailed(w http.ResponseWriter, r *http.Request, err error) {
	var provider interface{ ProviderUnavailable() bool }
	var coded interface{ Error() string }
	switch {
	case errors.Is(err, ask.ErrBadToken):
		w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-ask"`)
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid ask token is required", nil)
	case errors.Is(err, ask.ErrUnknownTool):
		writeError(w, http.StatusNotFound, codeNotFound, "no such tool", nil)
	case errors.Is(err, ask.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "no such trace", nil)
	case errors.Is(err, ask.ErrBadArguments), errors.Is(err, ask.ErrBadRequest):
		writeError(w, http.StatusBadRequest, codeBadRequest, cleanMessage(err), nil)
	case errors.Is(err, ask.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "ask_unavailable", "Ask Cliff is not available on this deployment", nil)
	case errors.As(err, &provider) && provider.ProviderUnavailable():
		s.log.ErrorContext(r.Context(), "ask: model provider unavailable", "error", err)
		writeError(w, http.StatusFailedDependency, "provider_unavailable_nonretryable", "the model provider is unavailable", nil)
	case errors.As(err, &coded) && strings.HasPrefix(err.Error(), "workerclient:"):
		s.log.ErrorContext(r.Context(), "ask: worker failed", "error", err)
		writeError(w, http.StatusBadGateway, "worker_error", "the model worker could not answer", nil)
	default:
		s.log.ErrorContext(r.Context(), "ask failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}

// cleanMessage drops the sentinel prefix ("ask: bad request: ") from a validation error.
func cleanMessage(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}
