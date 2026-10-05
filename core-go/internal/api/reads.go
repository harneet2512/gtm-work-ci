package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// ReadTimeout is the default deadline of one read request, database work included.
const ReadTimeout = 15 * time.Second

// ReadService is what the account read endpoints and the run trace need; *readmodel.Reader
// implements it.
type ReadService interface {
	Account(ctx context.Context, accountID string) (readmodel.Account, error)
	State(ctx context.Context, accountID string, asOf *time.Time) (json.RawMessage, error)
	// StateWorldAsOf is the state as the world stood strictly before t (ADR-0019).
	StateWorldAsOf(ctx context.Context, accountID string, t time.Time) (json.RawMessage, error)
	Diffs(ctx context.Context, accountID string, limit int, materialOnly bool) ([]readmodel.StateDiff, error)
	Signals(ctx context.Context, accountID string, limit int) ([]readmodel.Signal, error)
	// TimelinePage is the activity feed strictly before `before`; beforeID is the tie-break of the cursor.
	TimelinePage(ctx context.Context, accountID string, limit int, before *time.Time, beforeID string) (readmodel.Timeline, error)
	Trace(ctx context.Context, runID string) (readmodel.Trace, error)
	Run(ctx context.Context, runID string) (readmodel.Run, error)
	// ListAccounts and ListRuns are the keyset-paginated lists (GET /accounts, GET /runs; lists.go).
	ListAccounts(ctx context.Context, limit int, cursor string) (readmodel.AccountPage, error)
	ListRuns(ctx context.Context, f readmodel.RunFilter) (readmodel.RunPage, error)
}

// WithReads serves GET /accounts/{id}/state|diffs|signals|timeline and GET /runs/{id} (the run with its generation status) and GET /runs/{id}/trace.
func WithReads(r ReadService) Option {
	return func(s *server) error {
		if r == nil {
			return errors.New("api: read service is required")
		}
		s.reads = r
		return nil
	}
}

func (s *server) routeReads(mux *http.ServeMux) {
	if s.reads == nil {
		return
	}
	for path, h := range map[string]http.HandlerFunc{
		"/accounts":                       s.listAccounts,
		"/runs":                           s.listRuns,
		"/accounts/{account_id}":          s.getAccount,
		"/accounts/{account_id}/state":    s.getState,
		"/accounts/{account_id}/diffs":    s.getDiffs,
		"/accounts/{account_id}/signals":  s.getSignals,
		"/accounts/{account_id}/timeline": s.getTimeline,
		"/runs/{run_id}":                  s.getRun,
		"/runs/{run_id}/trace":            s.getTrace,
	} {
		mux.HandleFunc("GET "+path, s.operator(h))
		mux.HandleFunc(path, methodNotAllowed(http.MethodGet))
	}
}

// operator wraps h with the operator bearer-token check and a request deadline.
func (s *server) operator(h http.HandlerFunc) http.HandlerFunc {
	return s.operatorWithin(ReadTimeout, h)
}

// operatorWithin is operator with a request deadline of d.
func (s *server) operatorWithin(d time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ghost-core"`)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid bearer token is required", nil)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()
		h(w, r.WithContext(ctx))
	}
}

func (s *server) getAccount(w http.ResponseWriter, r *http.Request) {
	account, err := s.reads.Account(r.Context(), r.PathValue("account_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("as_of") != "" && q.Has("world_as_of") {
		writeError(w, http.StatusBadRequest, codeBadRequest, "as_of (computed-at) and world_as_of (world time) are mutually exclusive", nil)
		return
	}
	if q.Has("world_as_of") {
		t, ok := parseTime(w, q, "world_as_of")
		if !ok {
			return
		}
		state, err := s.reads.StateWorldAsOf(r.Context(), r.PathValue("account_id"), t)
		if err != nil {
			s.readFailed(w, r, err)
			return
		}
		writeRaw(w, http.StatusOK, state)
		return
	}
	var asOf *time.Time
	if q.Get("as_of") != "" { // an empty as_of has always meant "current"
		t, ok := parseTime(w, q, "as_of")
		if !ok {
			return
		}
		asOf = &t
	}
	state, err := s.reads.State(r.Context(), r.PathValue("account_id"), asOf)
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeRaw(w, http.StatusOK, state)
}

func (s *server) getDiffs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := parseLimit(w, q)
	if !ok {
		return
	}
	materialOnly := true // contract default
	if raw := q.Get("material_only"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, codeBadRequest, "material_only must be true or false", nil)
			return
		}
		materialOnly = v
	}
	items, err := s.reads.Diffs(r.Context(), r.PathValue("account_id"), limit, materialOnly)
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getSignals(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r.URL.Query())
	if !ok {
		return
	}
	items, err := s.reads.Signals(r.Context(), r.PathValue("account_id"), limit)
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getTimeline(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := parseLimit(w, q)
	if !ok {
		return
	}
	var before *time.Time
	if q.Get("before") != "" {
		t, ok := parseTime(w, q, "before")
		if !ok {
			return
		}
		before = &t
	}
	beforeID := q.Get("before_id")
	if beforeID != "" && (before == nil || !readmodel.ValidUUID(beforeID)) {
		writeError(w, http.StatusBadRequest, codeBadRequest, "before_id must be a uuid and needs before", nil)
		return
	}
	page, err := s.reads.TimelinePage(r.Context(), r.PathValue("account_id"), limit, before, beforeID)
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.reads.Run(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *server) getTrace(w http.ResponseWriter, r *http.Request) {
	trace, err := s.reads.Trace(r.Context(), r.PathValue("run_id"))
	if err != nil {
		s.readFailed(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

// parseTime reads the RFC 3339 query parameter name; it writes the 400 when it is malformed or empty.
func parseTime(w http.ResponseWriter, q url.Values, name string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, q.Get(name))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, name+" must be an RFC 3339 date-time", nil)
		return time.Time{}, false
	}
	return t, true
}

// parseLimit reads ?limit= (default readmodel.DefaultLimit, 1..readmodel.MaxLimit); it writes the 400.
func parseLimit(w http.ResponseWriter, q url.Values) (int, bool) {
	raw := q.Get("limit")
	if raw == "" {
		return readmodel.DefaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > readmodel.MaxLimit {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(readmodel.MaxLimit), nil)
		return 0, false
	}
	return n, true
}

// readFailed maps a read error to the envelope; internal detail stays in the server log.
func (s *server) readFailed(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, readmodel.ErrNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, "not found", nil)
	case errors.Is(err, readmodel.ErrNoState):
		writeError(w, http.StatusNotFound, "state_not_computed", "no account state has been computed for that time", nil)
	case errors.Is(err, readmodel.ErrNoStateBefore):
		writeError(w, http.StatusNotFound, "state_not_computed_before", "no account state existed strictly before that world time", nil)
	case errors.Is(err, readmodel.ErrInvalid):
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid request parameter", nil)
	default:
		s.log.ErrorContext(r.Context(), "read failed", "error", err, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
	}
}

// writeRaw writes pre-encoded JSON (a stored state document) with the standard headers.
func writeRaw(w http.ResponseWriter, status int, raw json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append([]byte(raw), '\n'))
}
