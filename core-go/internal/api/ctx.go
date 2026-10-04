package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/corectx"
)

// PullTimeout is the default deadline of one context pull, database work included.
const PullTimeout = 10 * time.Second

// ContextService serves a bounded context pull; *corectx.Service implements it.
type ContextService interface {
	Pull(ctx context.Context, runID string, tool corectx.Tool, p corectx.Params) (corectx.Packet, error)
}

// RunTokenVerifier turns a run-scoped bearer token into the run id it names; *runtoken.Signer
// implements it. It must return an error for every token it does not accept.
type RunTokenVerifier interface {
	Verify(token string, now time.Time) (runID string, err error)
}

// WithContext serves GET /internal/ctx/{tool}, authenticated by run-scoped tokens.
func WithContext(svc ContextService, tokens RunTokenVerifier) Option {
	return func(s *server) error {
		if svc == nil || tokens == nil {
			return errors.New("api: context service and run token verifier are required")
		}
		s.pulls, s.runTokens = svc, tokens
		return nil
	}
}

func (s *server) routeContext(mux *http.ServeMux) {
	if s.pulls == nil {
		return
	}
	mux.HandleFunc("GET /internal/ctx/{tool}", s.pullContext)
	mux.HandleFunc("/internal/ctx/{tool}", methodNotAllowed(http.MethodGet))
}

// bearer returns the token of "Authorization: Bearer <token>".
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

func (s *server) pullContext(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet { // the GET pattern also matches HEAD, which must not spend a pull
		methodNotAllowed(http.MethodGet)(w, r)
		return
	}
	runID, ok := s.runFromToken(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-run"`)
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid run token is required", nil)
		return
	}
	params, ok := pullParams(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), PullTimeout)
	defer cancel()
	packet, err := s.pulls.Pull(ctx, runID, corectx.Tool(r.PathValue("tool")), params)
	if err != nil {
		s.pullFailed(w, r, runID, err)
		return
	}
	writeJSON(w, http.StatusOK, packet)
}

func (s *server) runFromToken(r *http.Request) (string, bool) {
	token, ok := bearer(r)
	if !ok {
		return "", false
	}
	runID, err := s.runTokens.Verify(token, s.now())
	return runID, err == nil
}

// pullParams accepts only field_path and limit; any other parameter (account_id above all) is a 400,
// so the endpoint can never be made to name an account.
func pullParams(w http.ResponseWriter, r *http.Request) (corectx.Params, bool) {
	var p corectx.Params
	q := r.URL.Query()
	for name, values := range q {
		if (name != "field_path" && name != "limit") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "only one field_path and one limit parameter are accepted", nil)
			return p, false
		}
	}
	p.FieldPath = q.Get("field_path")
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > corectx.MaxLimit {
			writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(corectx.MaxLimit), nil)
			return p, false
		}
		p.Limit = n
	}
	return p, true
}

// pullFailed maps a pull error to the envelope; internal detail stays in the server log (by run id,
// never the token).
func (s *server) pullFailed(w http.ResponseWriter, r *http.Request, runID string, err error) {
	switch {
	case errors.Is(err, corectx.ErrUnknownTool):
		writeError(w, http.StatusNotFound, codeNotFound, "no such tool", nil)
	case errors.Is(err, corectx.ErrBadParams):
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid tool parameters", nil)
	case errors.Is(err, corectx.ErrRunInactive):
		writeError(w, http.StatusForbidden, codeForbidden, "the run no longer accepts context pulls", nil)
	case errors.Is(err, corectx.ErrAmbiguousWorldTime):
		writeError(w, http.StatusConflict, "world_time_ambiguous", "an activity of the account ties with the run's trigger in world time", nil)
	case errors.Is(err, corectx.ErrStateAfterCutoff):
		writeError(w, http.StatusConflict, "run_state_after_cutoff", "the state version the run was built on folds activities after its trigger", nil)
	case errors.Is(err, corectx.ErrTooManyPulls):
		writeError(w, http.StatusTooManyRequests, codeTooMany, "the run's context pull budget is used up", nil)
	default:
		s.log.ErrorContext(r.Context(), "context pull failed", "error", err, "run_id", runID, "tool", r.PathValue("tool"))
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}
