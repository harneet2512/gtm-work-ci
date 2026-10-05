package codespace

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	opHandoff = "Continue the chronology"
	// jobTimeout bounds one handoff: the knowledge carry, a graph rebuild and a core restart.
	jobTimeout  = 15 * time.Minute
	maxBodySize = 4 << 10
)

// Control is the loopback control service the web server calls (server side, with the shared API token): the readiness
// status, and the hidden handoff behind Play. It is not forwarded and not public, and it has no operator controls: Reset
// is a hidden desktop shortcut, not an endpoint.
type Control struct {
	Ops    Ops
	Status *StatusSource
	Token  string
	// Now is for tests; nil is the wall clock.
	Now func() time.Time

	mu  sync.Mutex
	job *JobStatus
}

func (c *Control) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Job is the current or last operation (nil before the first), a copy safe to read.
func (c *Control) Job() *JobStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job == nil {
		return nil
	}
	j := *c.job
	return &j
}

// Handler serves GET /healthz (open), GET /status and POST /handoff (bearer token).
func (c *Control) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /status", c.authed(http.HandlerFunc(c.getStatus)))
	mux.Handle("POST /handoff", c.authed(http.HandlerFunc(c.postHandoff)))
	return mux
}

func (c *Control) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if c.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(c.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized", "a valid bearer token is required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Control) getStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, c.Status.Compute(r.Context()))
}

// postHandoff answers when the next case is active and core serves it: the web's Play then releases that case's Event N.
// It runs to the end even when the caller gives up, because half a switch would leave core on no case.
func (c *Control) postHandoff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ManifestID string `json:"manifest_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.ManifestID == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "the request body must name the manifest"))
		return
	}
	if busy := c.begin(opHandoff); busy != nil {
		writeJSON(w, http.StatusConflict, errorBody("busy", busy.Op+" is already running"))
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), jobTimeout)
	defer cancel()
	h, err := c.Ops.Handoff(ctx, req.ManifestID)
	c.finish(err)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, h)
	case errors.Is(err, ErrUnknownManifest):
		writeJSON(w, http.StatusNotFound, errorBody("unknown_manifest", "no demo case was frozen under that manifest"))
	case errors.Is(err, ErrNotPlayed):
		writeJSON(w, http.StatusConflict, errorBody("not_played", "the case has not been played yet"))
	case errors.Is(err, ErrNoNextCase):
		writeJSON(w, http.StatusConflict, errorBody("no_next_case", "there is no later episode"))
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody("handoff_failed", "the next episode could not be prepared"))
	}
}

// begin records a running operation, or returns the one that is already running.
func (c *Control) begin(op string) *JobStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.job != nil && c.job.Running {
		busy := *c.job
		return &busy
	}
	c.job = &JobStatus{Op: op, Step: "Starting", Running: true, StartedAt: c.now().UTC()}
	return nil
}

func (c *Control) finish(err error) {
	if c.Status != nil {
		c.Status.Forget()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.job.Running = false
	c.job.EndedAt = c.now().UTC()
	if err != nil {
		c.job.Error = err.Error()
		c.job.Step = "Failed"
		return
	}
	c.job.Step = "Done"
}

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("bad_request", "the request body must be one JSON object with the documented fields"))
		return false
	}
	return true
}

func errorBody(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
