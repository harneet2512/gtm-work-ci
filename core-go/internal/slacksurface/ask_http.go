package slacksurface

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// askTimeout covers core's worker loop (60 s) and a Play of the held-out event (55 s).
const askTimeout = 90 * time.Second

// AskHTTP is the real AskCore: a bearer-authenticated client of POST /ask and POST /ask/actions with a timeout that
// covers a whole agent loop (the shared CoreHTTP client gives up after 30 s).
type AskHTTP struct{ c *CoreHTTP }

// NewAskHTTP builds the client; token is the core API bearer token.
func NewAskHTTP(baseURL string, token Secret, hc *http.Client) *AskHTTP {
	if hc == nil {
		hc = &http.Client{Timeout: askTimeout}
	}
	return &AskHTTP{c: NewCoreHTTP(baseURL, token, hc)}
}

// Ask posts the question. Statuses map to the Err* values; anything else is a plain error.
func (a *AskHTTP) Ask(ctx context.Context, req AskRequest) (v AskAnswer, err error) {
	err = a.c.do(ctx, http.MethodPost, "/ask", req, &v)
	return v, askError(err)
}

// RunAction posts a confirmed action.
func (a *AskHTTP) RunAction(ctx context.Context, req AskActionRequest) (v AskActionResult, err error) {
	err = a.c.do(ctx, http.MethodPost, "/ask/actions", req, &v)
	return v, askError(err)
}

// askError maps what statusError could not tell apart: 403, 424 and 503 arrive as plain errors with the status in them.
func askError(err error) error {
	var se *statusErr
	if errors.As(err, &se) {
		switch se.Status {
		case http.StatusFailedDependency:
			return ErrAskProvider
		case http.StatusServiceUnavailable:
			return ErrAskUnavailable
		}
	}
	return err
}

var _ AskCore = (*AskHTTP)(nil)
