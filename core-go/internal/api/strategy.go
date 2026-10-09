package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
)

// StrategyService is what the HAR-129 decision endpoints need; *strategystore.Service implements it.
// Reads return the stored object as the JSON its contract schema describes.
type StrategyService interface {
	Strategies(ctx context.Context, runID string) ([]byte, error)
	LatestBusinessIntelligence(ctx context.Context, accountID string) ([]byte, error)
	Decision(ctx context.Context, runID string) ([]byte, error)
	RecordDecision(ctx context.Context, runID string, req strategystore.DecisionRequest) (doc []byte, created bool, err error)
	Send(ctx context.Context, runID string, req strategystore.SendRequest) ([]byte, error)
	Inference(ctx context.Context, episodeID string) ([]byte, error)
	SubmitVerdict(ctx context.Context, episodeID string, req strategystore.VerdictRequest) ([]byte, error)
}

// WithStrategy serves the strategy set, the human strategy decision (choose, edit, send), the judgment
// inference and verdict, and the latest business-intelligence update.
func WithStrategy(svc StrategyService) Option {
	return func(s *server) error {
		if svc == nil {
			return errors.New("api: strategy service is required")
		}
		s.strategy = svc
		return nil
	}
}

// SendTimeout is the deadline of one POST /runs/{id}/send. The send re-evaluates the final artifact (the pre-send D8 judgment and the
// send-time evals), which on a replayed run is instant; recording it makes real thinking-model calls of a minute each. The
// server's WriteTimeout is lifted for this route only (postSend).
const SendTimeout = 10 * time.Minute

func (s *server) routeStrategy(mux *http.ServeMux) {
	if s.strategy == nil {
		return
	}
	for _, r := range []struct {
		path    string
		allowed string
		methods map[string]http.HandlerFunc
	}{
		{"/runs/{run_id}/strategies", http.MethodGet, map[string]http.HandlerFunc{"GET": s.getStrategies}},
		{"/runs/{run_id}/strategy-decision", "GET, POST", map[string]http.HandlerFunc{"GET": s.getDecision, "POST": s.postDecision}},
		{"/runs/{run_id}/send", http.MethodPost, map[string]http.HandlerFunc{"POST": s.postSend}},
		{"/episodes/{episode_id}/judgment-inference", http.MethodGet, map[string]http.HandlerFunc{"GET": s.getInference}},
		{"/episodes/{episode_id}/judgment-verdict", http.MethodPost, map[string]http.HandlerFunc{"POST": s.postVerdict}},
		{"/accounts/{account_id}/business-intelligence/latest", http.MethodGet, map[string]http.HandlerFunc{"GET": s.getLatestBI}},
	} {
		for method, h := range r.methods {
			if r.path == "/runs/{run_id}/send" {
				mux.HandleFunc(method+" "+r.path, s.operatorWithin(SendTimeout, h))
				continue
			}
			mux.HandleFunc(method+" "+r.path, s.operator(h))
		}
		mux.HandleFunc(r.path, methodNotAllowed(r.allowed))
	}
}

// extendWriteDeadline lifts the server's WriteTimeout for this response only. A writer that cannot move its deadline is not
// fatal (the route still answers), but a long response may then be cut off, so the failure is logged at WARN with the route.
func (s *server) extendWriteDeadline(w http.ResponseWriter, r *http.Request, d time.Duration) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d)); err != nil {
		route := r.Pattern
		if route == "" {
			route = r.Method + " " + r.URL.Path
		}
		s.log.WarnContext(r.Context(), "could not extend the write deadline: a long response may be cut off by the server's write timeout",
			"route", route, "extension", d.String(), "error", err)
	}
}

func (s *server) getStrategies(w http.ResponseWriter, r *http.Request) {
	doc, err := s.strategy.Strategies(r.Context(), r.PathValue("run_id"))
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

func (s *server) getLatestBI(w http.ResponseWriter, r *http.Request) {
	doc, err := s.strategy.LatestBusinessIntelligence(r.Context(), r.PathValue("account_id"))
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

func (s *server) getDecision(w http.ResponseWriter, r *http.Request) {
	doc, err := s.strategy.Decision(r.Context(), r.PathValue("run_id"))
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

func (s *server) getInference(w http.ResponseWriter, r *http.Request) {
	doc, err := s.strategy.Inference(r.Context(), r.PathValue("episode_id"))
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

func (s *server) postDecision(w http.ResponseWriter, r *http.Request) {
	var req strategystore.DecisionRequest
	if !decodeBody(w, r, &req, "strategy decision request") {
		return
	}
	doc, created, err := s.strategy.RecordDecision(r.Context(), r.PathValue("run_id"), req)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	s.strategyReply(w, r, status, doc, err)
}

func (s *server) postSend(w http.ResponseWriter, r *http.Request) {
	var req strategystore.SendRequest
	if !decodeBody(w, r, &req, "send request") {
		return
	}
	// The server's 60 s WriteTimeout would cut a send that is recording its model calls: extend it for this response only.
	s.extendWriteDeadline(w, r, SendTimeout+10*time.Second)
	doc, err := s.strategy.Send(r.Context(), r.PathValue("run_id"), req)
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

func (s *server) postVerdict(w http.ResponseWriter, r *http.Request) {
	var req strategystore.VerdictRequest
	if !decodeBody(w, r, &req, "judgment verdict request") {
		return
	}
	doc, err := s.strategy.SubmitVerdict(r.Context(), r.PathValue("episode_id"), req)
	s.strategyReply(w, r, http.StatusOK, doc, err)
}

// decodeBody reads a size-limited body strictly into dst; it writes the error response itself.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any, what string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeBodyErrorFor(w, err, what)
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			err = errTrailingData
		}
		writeBodyErrorFor(w, err, what)
		return false
	}
	return true
}

// strategyReply writes doc on success and maps the service's errors to the contract's statuses. Internal
// detail stays in the server log.
func (s *server) strategyReply(w http.ResponseWriter, r *http.Request, status int, doc []byte, err error) {
	var nf *strategystore.NotFoundError
	var nr *strategystore.NotReadyError
	var conflict *strategystore.ConflictError
	var refused *strategystore.RefusedError
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write(append(bytes.TrimSpace(doc), '\n'))
	case errors.As(err, &nf):
		writeError(w, http.StatusNotFound, nf.Code, "not found", nil)
	case errors.As(err, &nr):
		writeError(w, http.StatusNotFound, nr.Code, "not ready yet", nil)
	case errors.As(err, &conflict):
		if conflict.Decision != nil && conflict.Code == strategystore.CodeAlreadyDecided {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write(append(bytes.TrimSpace(conflict.Decision), '\n'))
			return
		}
		writeError(w, http.StatusConflict, conflict.Code, conflictMessage(conflict.Code), nil)
	case errors.As(err, &refused):
		writeError(w, http.StatusUnprocessableEntity, refused.Code, refused.Message, nil)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		s.log.WarnContext(r.Context(), "strategy request ended early", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusGatewayTimeout, "timeout", "the request did not finish in time", nil)
	default:
		s.log.ErrorContext(r.Context(), "strategy request failed", "error", err, "path", r.URL.Path, "method", r.Method)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}

func conflictMessage(code string) string {
	switch code {
	case strategystore.CodeSelectionLocked:
		return "a candidate was already chosen; the selection cannot change"
	case strategystore.CodeNoChoice:
		return "no strategy has been chosen yet"
	default:
		return "the decision was already made"
	}
}
