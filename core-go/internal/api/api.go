// Package api is the core service's HTTP surface (contracts/openapi/core.yaml): GET /healthz,
// POST /ingest, and, when wired with WithReads / WithContext, the account read endpoints, the run
// trace and the run-token-scoped GET /internal/ctx/{tool}. Errors always use the
// {"error": {code, message}} envelope and never carry internal detail.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

// MaxBodyBytes is the largest accepted request body (1 MiB).
const MaxBodyBytes = 1 << 20

// IngestTimeout is the default deadline of one POST /ingest call, database work included.
const IngestTimeout = 15 * time.Second

// maxEchoedFieldLen bounds a client-supplied field name echoed in a 400 message.
const maxEchoedFieldLen = 64

// Error codes in the envelope. Validation codes come from normalize.
const (
	codeUnauthorized = "unauthorized"
	codeBadRequest   = "bad_request"
	codeTooLarge     = "payload_too_large"
	codeNotFound     = "not_found"
	codeMethod       = "method_not_allowed"
	codeInternal     = "internal"
	codeForbidden    = "forbidden"
	codeTooMany      = "too_many_requests"
)

// IngestService is what POST /ingest needs from the core; *ingest.Service implements it.
type IngestService interface {
	Ingest(ctx context.Context, ev normalize.SourceEvent) (ingest.Result, error)
}

type server struct {
	svc           IngestService
	token         string
	log           *slog.Logger
	ingestTimeout time.Duration
	reads         ReadService
	pulls         ContextService
	breaker       BreakerService
	graph         GraphService
	strategy      StrategyService
	supervision   SupervisionService
	knowledge     KnowledgeService
	outbox        OutboxService
	surfaceMsgs   SurfaceMessageService
	replay        ReplayService
	disputes      EvalDisputeService
	control       ControlPlaneService
	gateResults   GateResultReader
	progress      ProgressService
	recomputation RecomputationService
	runTokens     RunTokenVerifier
	ask           AskService
	now           func() time.Time
}

// Option customizes NewHandler.
type Option func(*server) error

// WithIngestTimeout overrides IngestTimeout; d must be positive.
func WithIngestTimeout(d time.Duration) Option {
	return func(s *server) error {
		if d <= 0 {
			return errors.New("api: ingest timeout must be positive")
		}
		s.ingestTimeout = d
		return nil
	}
}

// NewHandler builds the router. token is the required bearer token for POST /ingest; an empty
// token is refused so the API can never start open. A nil logger discards logs.
func NewHandler(svc IngestService, token string, logger *slog.Logger, opts ...Option) (http.Handler, error) {
	if svc == nil {
		return nil, errors.New("api: ingest service is required")
	}
	if token == "" {
		return nil, errors.New("api: API token is required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	s := &server{svc: svc, token: token, log: logger, ingestTimeout: IngestTimeout, now: time.Now}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("/healthz", methodNotAllowed(http.MethodGet))
	mux.HandleFunc("POST /ingest", s.ingest)
	mux.HandleFunc("/ingest", methodNotAllowed(http.MethodPost))
	s.routeReads(mux)
	s.routeContext(mux)
	s.routeBreaker(mux)
	s.routeGraph(mux)
	s.routeStrategy(mux)
	s.routeSupervision(mux)
	s.routeKnowledge(mux)
	s.routeOutbox(mux)
	s.routeSurfaceMessages(mux)
	s.routeReplay(mux)
	s.routeEvalDisputes(mux)
	s.routeControlPlane(mux)
	s.routeGateResults(mux)
	s.routeProgress(mux)
	s.routeRecomputation(mux)
	s.routeAsk(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, codeNotFound, "no such endpoint", nil)
	})
	return mux, nil
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) ingest(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-core"`)
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid bearer token is required", nil)
		return
	}
	ev, ok := decodeEvent(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.ingestTimeout)
	defer cancel()
	res, err := s.svc.Ingest(ctx, ev)
	if err != nil {
		s.ingestFailed(w, r, ev, err)
		return
	}
	status := http.StatusCreated
	if res.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
}

func (s *server) ingestFailed(w http.ResponseWriter, r *http.Request, ev normalize.SourceEvent, err error) {
	var verr *normalize.ValidationError
	if errors.As(err, &verr) {
		writeError(w, http.StatusUnprocessableEntity, verr.Code, verr.Message, nil)
		return
	}
	// Internal detail (SQL errors, hosts) stays in the server log, never in the response.
	s.log.ErrorContext(r.Context(), "ingest failed", "error", err,
		"source_system", ev.SourceSystem, "source_object_id", ev.SourceObjectID, "source_event_key", ev.SourceEventKey)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
}

// errTrailingData marks a body with content after the first JSON value.
var errTrailingData = errors.New("trailing data")

// decodeEvent reads the size-limited body strictly; it writes the error response itself.
func decodeEvent(w http.ResponseWriter, r *http.Request) (normalize.SourceEvent, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var ev normalize.SourceEvent
	if err := dec.Decode(&ev); err != nil {
		writeBodyError(w, err)
		return ev, false
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			err = errTrailingData
		}
		writeBodyError(w, err)
		return ev, false
	}
	return ev, true
}

// writeBodyError answers with a fixed message per failure class; at most the offending field
// name (bounded) is echoed, never raw decoder text.
func writeBodyError(w http.ResponseWriter, err error) { writeBodyErrorFor(w, err, "SourceEvent") }

// writeBodyErrorFor is writeBodyError for a body that should be the named type.
func writeBodyErrorFor(w http.ResponseWriter, err error, what string) {
	var tooBig *http.MaxBytesError
	var syntax *json.SyntaxError
	var wrongType *json.UnmarshalTypeError
	msg := "request body is not a valid " + what
	code := codeBadRequest
	switch {
	case errors.As(err, &tooBig):
		code, msg = codeTooLarge, "request body exceeds the 1 MiB limit"
	case errors.Is(err, io.EOF):
		msg = "request body is empty"
	case errors.Is(err, errTrailingData):
		msg = "request body must contain exactly one JSON object"
	case errors.As(err, &syntax), errors.Is(err, io.ErrUnexpectedEOF):
		msg = "request body is not valid JSON"
	case errors.As(err, &wrongType):
		if wrongType.Field == "" {
			msg = "request body must be a JSON object"
		} else {
			msg = fmt.Sprintf("field %q has the wrong type", clip(wrongType.Field))
		}
	default:
		if name, ok := unknownFieldName(err); ok {
			msg = fmt.Sprintf("request body has an unknown field %q", clip(name))
		}
	}
	writeError(w, http.StatusBadRequest, code, msg, nil)
}

// unknownFieldName extracts the name from encoding/json's DisallowUnknownFields error, which
// has no exported type.
func unknownFieldName(err error) (string, bool) {
	quoted, ok := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !ok {
		return "", false
	}
	name, uerr := strconv.Unquote(quoted)
	return name, uerr == nil
}

func clip(s string) string {
	if len(s) > maxEchoedFieldLen {
		return s[:maxEchoedFieldLen] + "..."
	}
	return s
}

// authorized checks "Authorization: Bearer <token>" in constant time.
func (s *server) authorized(r *http.Request) bool {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return tokenMatches(token, s.token)
}

// tokenMatches compares two tokens without leaking their length or common prefix.
func tokenMatches(got, want string) bool {
	if want == "" {
		return false
	}
	g, w := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

func methodNotAllowed(allowed string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", allowed)
		writeError(w, http.StatusMethodNotAllowed, codeMethod, "method not allowed; use "+allowed, nil)
	}
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]errorBody{"error": {Code: code, Message: message, Details: details}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"internal error"}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
