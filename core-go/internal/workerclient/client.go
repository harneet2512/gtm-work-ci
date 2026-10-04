// Package workerclient is the HTTP client for the Python model worker's POST /v1/extract
// (contracts/openapi/worker.yaml). It implements claims.Extractor; the worker URL comes from
// configuration (WORKER_URL), never from a request. Core does not call a model directly.
package workerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// DefaultTimeout bounds one extraction when the caller sets none. It must exceed the worker's own
// extract deadline (worker-py extract_deadline_s, 180 s by default), so the worker answers or fails with
// a 502 first; core configures it (WORKER_TIMEOUT_MS) and refuses to start with a timeout at or below
// that deadline.
const DefaultTimeout = 210 * time.Second

// MaxResponseBytes caps the worker response we are willing to read.
const MaxResponseBytes = 4 << 20

// Error is a failed worker call. Retryable errors (network, timeout, 429, 5xx) may succeed on a
// later attempt; a 4xx other than 429 will not.
type Error struct {
	Status    int // HTTP status, 0 for transport failures
	Code      string
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return "workerclient: " + e.Message
	}
	return fmt.Sprintf("workerclient: worker returned %d %s: %s", e.Status, e.Code, e.Message)
}

// CodeProviderUnavailable is the worker's non-retryable provider error code (contracts/openapi/worker.yaml).
const CodeProviderUnavailable = "provider_unavailable_nonretryable"

// Permanent reports that retrying the same request cannot succeed because the request itself is bad
// (claims.IsPermanent). A provider outage is not the request's fault: see ProviderUnavailable.
func (e *Error) Permanent() bool { return !e.Retryable && !e.ProviderUnavailable() }

// ProviderUnavailable reports a non-retryable provider refusal (credits, quota, auth, worker breaker open).
func (e *Error) ProviderUnavailable() bool { return e.Code == CodeProviderUnavailable }

// ProviderFault reports a failure that counts toward the circuit breaker: a provider refusal, an
// unreachable worker or a 5xx.
func (e *Error) ProviderFault() bool {
	return e.ProviderUnavailable() || e.Status == 0 || e.Status >= 500
}

// Client calls the worker. It is safe for concurrent use.
type Client struct {
	baseURL string
	http    *http.Client
	long    *http.Client // no overall timeout of its own: strategies, judge and revise calls carry per-call budgets
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.http.Timeout = d } }

// New returns a client for the worker at baseURL (http or https).
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("workerclient: worker URL %q must be an http(s) URL", baseURL)
	}
	c := &Client{
		baseURL: strings.TrimRight(u.String(), "/"),
		http: &http.Client{
			Timeout: DefaultTimeout,
			// The request body holds customer text; never replay it to a redirect target.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	c.long = &http.Client{CheckRedirect: c.http.CheckRedirect}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

// Extract implements claims.Extractor.
func (c *Client) Extract(ctx context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	if strings.TrimSpace(req.Text) == "" {
		return claims.ExtractResponse{}, errors.New("workerclient: extraction text is empty")
	}
	body, err := json.Marshal(newWireRequest(req))
	if err != nil {
		return claims.ExtractResponse{}, fmt.Errorf("workerclient: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/extract", bytes.NewReader(body))
	if err != nil {
		return claims.ExtractResponse{}, fmt.Errorf("workerclient: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return claims.ExtractResponse{}, ctxErr
		}
		return claims.ExtractResponse{}, &Error{Message: "worker unreachable or timed out: " + redactURLError(err), Retryable: true}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return claims.ExtractResponse{}, &Error{Message: "read worker response: " + err.Error(), Retryable: true}
	}
	if len(raw) > MaxResponseBytes {
		return claims.ExtractResponse{}, &Error{Status: resp.StatusCode, Message: "worker response exceeds the size limit"}
	}
	if resp.StatusCode != http.StatusOK {
		return claims.ExtractResponse{}, statusError(resp.StatusCode, raw)
	}
	var out claims.ExtractResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return claims.ExtractResponse{}, &Error{Status: resp.StatusCode, Code: "bad_response", Message: "worker response is not valid JSON"}
	}
	if out.Model == "" {
		return claims.ExtractResponse{}, &Error{Status: resp.StatusCode, Code: "bad_response", Message: "worker response names no model"}
	}
	return out, nil
}

func statusError(status int, raw []byte) *Error {
	e := &Error{Status: status, Retryable: status == http.StatusTooManyRequests || status >= 500}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil {
		e.Code, e.Message = env.Error.Code, env.Error.Message
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	if e.ProviderUnavailable() {
		e.Retryable = false // whatever the status, retrying a refused key or an empty account cannot succeed
	}
	return e
}

// redactURLError drops the URL from transport errors: it is configuration, not something to log.
func redactURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}
