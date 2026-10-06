package demosmoke

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// reqLog records every request in the fakecore.Log shape, writes the judgment inference once a send
// succeeds (the worker's /v1/judgment-inference is contract-only yet) and mirrors the recorded effects.
type reqLog struct {
	mu     sync.Mutex
	db     *sql.DB
	seed   *strategytest.Seeded
	path   string
	logger *slog.Logger
	log    fakecore.Log
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (c *captureWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

// reqLogMaxBody bounds a captured request or response body, as fakecore's does.
const reqLogMaxBody = 1 << 20

func (c *captureWriter) Write(b []byte) (int, error) {
	if len(c.body) < reqLogMaxBody {
		keep := reqLogMaxBody - len(c.body)
		if keep > len(b) {
			keep = len(b)
		}
		c.body = append(c.body, b[:keep]...)
	}
	return c.ResponseWriter.Write(b)
}

func (l *reqLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, reqLogMaxBody))
			r.Body = io.NopCloser(strings.NewReader(string(body)))
		}
		rec := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		l.record(r, body, rec)
	})
}

func (l *reqLog) record(r *http.Request, body []byte, rec *captureWriter) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := fakecore.Entry{Seq: len(l.log.Entries) + 1, At: time.Now().UTC(), Method: r.Method,
		Path: r.URL.Path, Status: rec.status}
	if len(body) > 0 && json.Valid(body) {
		e.Request = json.RawMessage(body)
	}
	if rec.status >= 400 {
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(rec.body, &env) == nil {
			e.Code = env.Error.Code
		}
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/strategies") && rec.status == http.StatusOK {
		sum := sha256.Sum256(rec.body)
		e.ResponseSHA256 = hex.EncodeToString(sum[:])
		if !hasString(l.log.StrategiesSHA256, e.ResponseSHA256) {
			l.log.StrategiesSHA256 = append(l.log.StrategiesSHA256, e.ResponseSHA256)
		}
	}
	l.log.Entries = append(l.log.Entries, e)
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/send") && rec.status == http.StatusOK {
		l.refreshEffects(r.Context())
	}
	if err := writeLogAtomic(l.path, l.log); err != nil && l.logger != nil {
		l.logger.Warn("demosmoke: could not write the request log", "error", err)
	}
}

// hasCode reports whether a logged entry carries the error code (the 409/422 envelopes).
func (l *reqLog) hasCode(code string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.log.Entries {
		if e.Code == code {
			return true
		}
	}
	return false
}

// refreshEffects mirrors the recorded dry-run effects of the episode's run into the log's effect list.
func (l *reqLog) refreshEffects(ctx context.Context) {
	rows, err := l.db.QueryContext(ctx, `SELECT detail->'recorded_effect' FROM agent_run_steps
WHERE agent_run_id = $1::uuid AND step = 'execute' AND status = 'recorded'`, l.seed.RunID)
	if err != nil {
		return
	}
	defer rows.Close()
	var effects []fakecore.Effect
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return
		}
		var eff struct {
			Kind           string `json:"kind"`
			IdempotencyKey string `json:"idempotency_key"`
			Target         string `json:"target"`
		}
		if json.Unmarshal(raw, &eff) == nil && eff.Kind != "" {
			effects = append(effects, fakecore.Effect{Kind: eff.Kind, IdempotencyKey: eff.IdempotencyKey,
				To: []string{eff.Target}})
		}
	}
	if effects != nil {
		l.log.Effects = effects
	}
}

func hasString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// writeLogAtomic rewrites the log file atomically so a killed process still leaves a complete log.
func writeLogAtomic(path string, v any) error {
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
