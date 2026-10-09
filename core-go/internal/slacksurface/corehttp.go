package slacksurface

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxCoreResponseBytes = 4 << 20

// Core API paths. Those marked "contract" are in contracts/openapi/core.yaml (HAR-129 section 14);
// "existing" ones are served by core already or are pending WP9 (GET /accounts/{id}).
const (
	pathBI         = "/accounts/%s/business-intelligence/latest" // contract
	pathAccount    = "/accounts/%s"                              // existing
	pathGraph      = "/accounts/%s/graph"                        // existing
	pathState      = "/accounts/%s/state"                        // existing
	pathStrategies = "/runs/%s/strategies"                       // contract
	pathRecord     = "/runs/%s/strategy-decision"                // contract (POST)
	pathDecision   = "/runs/%s/strategy-decision"                // contract (GET)
	pathSend       = "/runs/%s/send"                             // contract
	pathVerdict    = "/episodes/%s/judgment-verdict"             // contract
	pathInference  = "/episodes/%s/judgment-inference"           // contract (GET)
)

// CoreHTTP is the real Core: a bearer-authenticated JSON client for the core service.
type CoreHTTP struct {
	base  string
	token Secret
	hc    *http.Client
	// long carries calls that wait on model work in core (the send re-evaluates the final artifact); it is hc itself when a caller supplied one.
	long *http.Client
}

// SendRunTimeout bounds a send: core's own deadline for it is 10 minutes.
const SendRunTimeout = 11 * time.Minute

// NewCoreHTTP builds a client for baseURL; token is the core API bearer token.
func NewCoreHTTP(baseURL string, token Secret, hc *http.Client) *CoreHTTP {
	long := hc
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
		long = &http.Client{Timeout: SendRunTimeout}
	}
	return &CoreHTTP{base: strings.TrimRight(baseURL, "/"), token: token, hc: hc, long: long}
}

func (c *CoreHTTP) do(ctx context.Context, method, path string, in, out any) error {
	return c.doWith(c.hc, ctx, method, path, in, out)
}

func (c *CoreHTTP) doWith(hc *http.Client, ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("slacksurface: encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("slacksurface: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(c.token))
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &AmbiguousError{fmt.Errorf("core %s %s: %w", method, path, err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCoreResponseBytes))
	if err != nil {
		return &AmbiguousError{fmt.Errorf("read core response: %w", err)}
	}
	if err := statusError(resp.StatusCode, raw, method, path); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &AmbiguousError{fmt.Errorf("decode core %s %s: %w", method, path, err)}
	}
	return nil
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// statusError maps a non-2xx answer. A 409 may carry the existing HumanStrategyDecision (the
// contract returns the record with already_decided) or the error envelope.
func statusError(code int, raw []byte, method, path string) error {
	if code >= 200 && code < 300 {
		return nil
	}
	var env errorEnvelope
	_ = json.Unmarshal(raw, &env)
	switch code {
	case http.StatusNotFound:
		if env.Error.Code == "strategies_not_ready" || env.Error.Code == "inference_not_ready" {
			return ErrNotReady
		}
		return ErrNotFound
	case http.StatusConflict:
		ce := &ConflictError{Code: env.Error.Code}
		var dec HumanStrategyDecision
		if json.Unmarshal(raw, &dec) == nil && dec.SelectedCandidateID != "" {
			ce.Decision = &dec
			if ce.Code == "" {
				ce.Code = CodeAlreadyDecided
			}
		}
		return ce
	case http.StatusUnprocessableEntity:
		return &RefusedError{Message: env.Error.Message}
	}
	err := &statusErr{Method: method, Path: path, Status: code, Code: env.Error.Code, Message: env.Error.Message}
	if code >= 500 {
		return &AmbiguousError{err}
	}
	return err
}

// statusErr is a non-2xx answer with no sentinel of its own; callers that care (Ask Cliff) read Status.
type statusErr struct {
	Method, Path, Code, Message string
	Status                      int
}

func (e *statusErr) Error() string {
	return fmt.Sprintf("slacksurface: core %s %s: status %d %s %s", e.Method, e.Path, e.Status, e.Code, e.Message)
}

func esc(s string) string { return url.PathEscape(s) }

func (c *CoreHTTP) GetBIUpdate(ctx context.Context, accountID string) (v BusinessIntelligenceUpdate, err error) {
	err = c.do(ctx, http.MethodGet, fmt.Sprintf(pathBI, esc(accountID)), nil, &v)
	return v, err
}

func (c *CoreHTTP) GetAccountName(ctx context.Context, accountID string) (string, error) {
	var v struct {
		Name string `json:"name"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathAccount, esc(accountID)), nil, &v)
	return v.Name, err
}

// People is the directory of everyone a candidate can address: the account graph's Person nodes (label = name, data.email when present)
// and the buying group of the account state (person_id, display_name), which is the list the planner's `people` tool serves and so the
// only place a recipient's id can come from. The graph is bounded (it reports truncated) and its nodes are typed "Person", so neither
// alone is the directory; a person in the state but not in the graph is addressed by name.
func (c *CoreHTTP) People(ctx context.Context, accountID string) (Directory, error) {
	dir, err := c.graphPeople(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var st struct {
		BuyingGroup []struct {
			PersonID    string `json:"person_id"`
			DisplayName string `json:"display_name"`
		} `json:"buying_group"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathState, esc(accountID)), nil, &st); err != nil {
		return nil, err
	}
	for _, m := range st.BuyingGroup {
		if m.PersonID == "" || m.DisplayName == "" {
			continue
		}
		if known, ok := dir[m.PersonID]; ok { // the graph's record wins (it can carry the email); fill a missing name
			if known.Name == "" {
				known.Name = m.DisplayName
				dir[m.PersonID] = known
			}
			continue
		}
		dir[m.PersonID] = Person{ID: m.PersonID, Name: m.DisplayName}
	}
	return dir, nil
}

func (c *CoreHTTP) graphPeople(ctx context.Context, accountID string) (Directory, error) {
	var g struct {
		Nodes []struct {
			ID    string         `json:"id"`
			Type  string         `json:"type"`
			Label string         `json:"label"`
			Data  map[string]any `json:"data"`
		} `json:"nodes"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf(pathGraph, esc(accountID)), nil, &g); err != nil {
		return nil, err
	}
	dir := Directory{}
	for _, n := range g.Nodes {
		if !strings.EqualFold(n.Type, "person") { // core types its nodes "Person"
			continue
		}
		email, _ := n.Data["email"].(string)
		dir[n.ID] = Person{ID: n.ID, Name: n.Label, Email: email}
	}
	return dir, nil
}

func (c *CoreHTTP) GetStrategies(ctx context.Context, runID string) (v RunStrategies, err error) {
	err = c.do(ctx, http.MethodGet, fmt.Sprintf(pathStrategies, esc(runID)), nil, &v)
	return v, err
}

func (c *CoreHTTP) GetStrategyDecision(ctx context.Context, runID string) (v HumanStrategyDecision, err error) {
	err = c.do(ctx, http.MethodGet, fmt.Sprintf(pathDecision, esc(runID)), nil, &v)
	return v, err
}

func (c *CoreHTTP) RecordStrategyDecision(ctx context.Context, runID string, req StrategyDecisionRequest) (v HumanStrategyDecision, err error) {
	err = c.do(ctx, http.MethodPost, fmt.Sprintf(pathRecord, esc(runID)), req, &v)
	return v, err
}

func (c *CoreHTTP) SendRun(ctx context.Context, runID string, req SendRequest) (v HumanStrategyDecision, err error) {
	err = c.doWith(c.long, ctx, http.MethodPost, fmt.Sprintf(pathSend, esc(runID)), req, &v)
	return v, err
}

func (c *CoreHTTP) GetJudgmentInference(ctx context.Context, episodeID string) (v JudgmentInference, err error) {
	err = c.do(ctx, http.MethodGet, fmt.Sprintf(pathInference, esc(episodeID)), nil, &v)
	return v, err
}

func (c *CoreHTTP) SubmitJudgmentVerdict(ctx context.Context, episodeID string, req VerdictRequest) (v JudgmentInference, err error) {
	err = c.do(ctx, http.MethodPost, fmt.Sprintf(pathVerdict, esc(episodeID)), req, &v)
	return v, err
}

var _ Core = (*CoreHTTP)(nil)
