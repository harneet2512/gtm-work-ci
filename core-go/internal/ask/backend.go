package ask

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// Backend is core's own HTTP API, called in process with the operator token. The ask tools read through it, so they
// reuse exactly the read endpoints the web and Slack use and add no data of their own; the demo actions post the
// very routes the web posts.
type Backend interface {
	Get(ctx context.Context, path string) (status int, body []byte, err error)
	Post(ctx context.Context, path string) (status int, body []byte, err error)
}

// Loopback serves Backend by calling the handler directly. Bind it to the finished router after the router is built.
type Loopback struct {
	token   string
	handler http.Handler
}

// NewLoopback returns a Loopback that authenticates as the operator with token.
func NewLoopback(token string) *Loopback { return &Loopback{token: token} }

// Bind sets the router the Loopback calls.
func (l *Loopback) Bind(h http.Handler) { l.handler = h }

func (l *Loopback) Get(ctx context.Context, path string) (int, []byte, error) {
	return l.do(ctx, http.MethodGet, path)
}

func (l *Loopback) Post(ctx context.Context, path string) (int, []byte, error) {
	return l.do(ctx, http.MethodPost, path)
}

func (l *Loopback) do(ctx context.Context, method, path string) (int, []byte, error) {
	if l.handler == nil {
		return 0, nil, ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, method, path, http.NoBody)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set("Accept", "application/json")
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	l.handler.ServeHTTP(rec, req)
	return rec.status, rec.body.Bytes(), nil
}

// recorder is the smallest http.ResponseWriter that keeps the answer.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(s int)   { r.status = s }
func (r *recorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

var _ io.Writer = (*recorder)(nil)
