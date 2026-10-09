// Package fakecore is a deterministic, in-memory stand-in for the core API endpoints the Slack surface
// calls (HAR-137 section 12: the Slack smoke test runs against a fixture before the real backend).
//
// It serves the contract fixture episode (slacksurface.NewFixture) over the same paths and with the same
// idempotency rules as core: choosing is locked, send is record-only and 409 already_decided the second
// time, the judgment inference exists only after the send. Every request body is validated against
// contracts/openapi/core.yaml, every request and its outcome is written to a JSON log (never the
// Authorization header), and the executor only records what would have been sent. It listens on loopback
// only and keeps all state in memory.
package fakecore

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/api/openapitest"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

const maxBody = 1 << 20

// Entry is one request the server handled, as written to the log.
type Entry struct {
	Seq            int             `json:"seq"`
	At             time.Time       `json:"at"`
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Status         int             `json:"status"`
	Code           string          `json:"code,omitempty"` // error code of a non-2xx answer
	Request        json.RawMessage `json:"request,omitempty"`
	ResponseSHA256 string          `json:"response_sha256,omitempty"`
}

// Effect is one send the record-only executor recorded: what would have been delivered.
type Effect struct {
	Kind           string   `json:"kind"`
	IdempotencyKey string   `json:"idempotency_key"`
	Subject        string   `json:"subject"`
	To             []string `json:"to"`
}

// Log is the document written to the log file after every request.
type Log struct {
	Version          int      `json:"version"`
	Entries          []Entry  `json:"entries"`
	Effects          []Effect `json:"effects"`
	StrategiesSHA256 []string `json:"strategies_sha256"` // distinct bodies served for GET /runs/{id}/strategies
}

// Options configure New.
type Options struct {
	Token   string           // required bearer token
	LogPath string           // when set, the log is rewritten here after every request
	Now     func() time.Time // defaults to time.Now
}

// Server is the fake core. Use Handler to serve it.
type Server struct {
	mu        sync.Mutex
	token     string
	logPath   string
	now       func() time.Time
	fx        slacksurface.Fixture
	docs      map[string][]byte
	spec      *openapitest.Spec
	refs      *Refs
	log       Log
	decision  map[string]any
	inference map[string]any
}

// New builds the server. It loads the contract fixture and core.yaml (found by walking up from the
// working directory) and refuses an empty token.
func New(opts Options) (*Server, error) {
	if opts.Token == "" {
		return nil, errors.New("fakecore: a bearer token is required")
	}
	spec, err := openapitest.Load()
	if err != nil {
		return nil, fmt.Errorf("fakecore: load the contract: %w", err)
	}
	docs, err := slacksurface.FixtureDocs()
	if err != nil {
		return nil, fmt.Errorf("fakecore: load the fixture: %w", err)
	}
	s := &Server{token: opts.Token, logPath: opts.LogPath, now: opts.Now, fx: slacksurface.NewFixture(), docs: docs, spec: spec,
		log: Log{Version: 1, Entries: []Entry{}, Effects: []Effect{}, StrategiesSHA256: []string{}}}
	if s.now == nil {
		s.now = time.Now
	}
	s.refs = NewRefs(s.now)
	return s, nil
}

// Log returns a copy of the log so far.
func (s *Server) Log() Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

func (s *Server) snapshot() Log {
	l := s.log
	l.Entries = append([]Entry(nil), l.Entries...)
	l.Effects = append([]Effect(nil), l.Effects...)
	l.StrategiesSHA256 = append([]string(nil), l.StrategiesSHA256...)
	return l
}

// answer is what a handler returns: a status and a JSON body.
type answer struct {
	status int
	body   any
	code   string // error code, for the log
	effect *Effect
}

func fail(status int, code, message string) answer {
	return answer{status: status, code: code, body: map[string]any{"error": map[string]string{"code": code, "message": message}}}
}

func ok(body any) answer { return answer{status: http.StatusOK, body: body} }

type handler func(r *http.Request, body []byte) answer

// Handler serves the fake core API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) })
	routes := []struct {
		method, template string
		h                handler
	}{
		{"GET", "/accounts/{account_id}", s.getAccount},
		{"GET", "/accounts/{account_id}/graph", s.getGraph},
		{"GET", "/accounts/{account_id}/state", s.getState},
		{"GET", "/accounts/{account_id}/business-intelligence/latest", s.getBI},
		{"GET", "/runs/{run_id}/strategies", s.getStrategies},
		{"GET", "/runs/{run_id}/strategy-decision", s.getDecision},
		{"POST", "/runs/{run_id}/strategy-decision", s.postDecision},
		{"POST", "/runs/{run_id}/send", s.postSend},
		{"GET", "/episodes/{episode_id}/judgment-inference", s.getInference},
		{"POST", "/episodes/{episode_id}/judgment-verdict", s.postVerdict},
		{"GET", "/surface-messages/{subject_id}/{surface}/{kind}", s.getSurfaceMessage},
		{"POST", "/surface-messages/{subject_id}/{surface}/{kind}/reservation", s.reserveSurfaceMessage},
		{"PUT", "/surface-messages/{subject_id}/{surface}/{kind}/ts", s.recordSurfaceMessageTS},
	}
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.template, s.serve(rt.method, rt.template, rt.h))
	}
	return mux
}

func (s *Server) authorized(r *http.Request) bool {
	scheme, tok, found := strings.Cut(r.Header.Get("Authorization"), " ")
	return found && strings.EqualFold(scheme, "Bearer") &&
		subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1
}

// serve wraps a handler: bearer check, bounded body, request validation against the contract, the state
// lock, the log entry and the JSON answer.
func (s *Server) serve(method, template string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		a := fail(http.StatusUnauthorized, "unauthorized", "a valid bearer token is required")
		if s.authorized(r) {
			var err error
			if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody)); err != nil {
				a = fail(http.StatusBadRequest, "bad_request", "unreadable request body")
			} else if a = s.checked(method, template, body); a.status == 0 {
				s.mu.Lock()
				a = h(r, body)
				s.mu.Unlock()
			}
		}
		raw, _ := json.Marshal(a.body)
		s.record(r, body, a, raw)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(a.status)
		_, _ = w.Write(append(raw, '\n'))
	}
}

// checked validates a body-carrying request against core.yaml; status 0 means it is acceptable.
func (s *Server) checked(method, template string, body []byte) answer {
	if method != http.MethodPost {
		return answer{}
	}
	if err := s.spec.Request(method, template, body); err != nil {
		return fail(http.StatusBadRequest, "invalid_request", "the request body does not match the contract")
	}
	return answer{}
}

func (s *Server) record(r *http.Request, body []byte, a answer, raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := Entry{Seq: len(s.log.Entries) + 1, At: s.now().UTC(), Method: r.Method, Path: r.URL.Path, Status: a.status, Code: a.code}
	if len(body) > 0 && json.Valid(body) {
		e.Request = json.RawMessage(body)
	}
	if strings.HasSuffix(r.URL.Path, "/strategies") && a.status == http.StatusOK {
		sum := sha256.Sum256(raw)
		e.ResponseSHA256 = hex.EncodeToString(sum[:])
		if !contains(s.log.StrategiesSHA256, e.ResponseSHA256) {
			s.log.StrategiesSHA256 = append(s.log.StrategiesSHA256, e.ResponseSHA256)
		}
	}
	if a.effect != nil {
		s.log.Effects = append(s.log.Effects, *a.effect)
	}
	s.log.Entries = append(s.log.Entries, e)
	s.persist()
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// persist rewrites the log file atomically so a killed process still leaves a complete log.
func (s *Server) persist() {
	if s.logPath == "" {
		return
	}
	if err := writeFileAtomic(s.logPath, s.log); err != nil {
		fmt.Fprintln(os.Stderr, "fakecore: could not write the request log:", err)
	}
}

func writeFileAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
