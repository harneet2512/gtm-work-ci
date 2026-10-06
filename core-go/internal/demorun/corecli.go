package demorun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// CoreClient is the demo runner's view of core's HTTP API (contracts/openapi/core.yaml). The token is the bearer
// credential and never appears in an error or a log line.
type CoreClient struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// APIError is core's error envelope.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("core answered %d %s: %s", e.Status, e.Code, e.Message)
}

// Leak is one thing the event-N-invisible check found.
type Leak struct {
	Store string `json:"store"`
	Kind  string `json:"kind"`
	ID    string `json:"id"`
}

// Invisibility is GET /replay/manifests/{id}/invisibility.
type Invisibility struct {
	ManifestID     string   `json:"manifest_id"`
	HeldOutEventID string   `json:"held_out_event_id"`
	Status         string   `json:"status"` // withheld | leaked | released
	Checked        []string `json:"checked"`
	Leaks          []Leak   `json:"leaks"`
}

// PlayOutcome is what POST /replay/play produced.
type PlayOutcome struct {
	AlreadyReleased bool
	AccountChangeID string
	AccountID       string
	BIUpdateID      string // empty when the change was not material
	BISummary       string
}

// RunInfo is the part of an AgentRun the demo follows.
type RunInfo struct {
	ID            string
	Status        string
	Phase         string // generation.phase: queued, generating, evaluating, paused, published, failed
	Reason        string
	StrategySetID string
	EpisodeID     string
}

func (c CoreClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Minute} // Play waits for the recompute and the graph projection
}

func (c CoreClient) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("core is not reachable at %s (is `demo up` running?): %v", c.Base, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		return resp.StatusCode, &APIError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// Invisibility reads the event-N-invisible assertion for a manifest.
func (c CoreClient) Invisibility(ctx context.Context, manifestID string) (Invisibility, error) {
	var out Invisibility
	_, err := c.do(ctx, http.MethodGet, "/replay/manifests/"+url.PathEscape(manifestID)+"/invisibility", nil, &out)
	return out, err
}

// Play releases Event N. A second call answers 409 already_released, which is reported as AlreadyReleased, not
// as a failure: the demo can be re-run to read the world back.
func (c CoreClient) Play(ctx context.Context, manifestID string) (PlayOutcome, error) {
	var res struct {
		Change struct {
			ID        string `json:"id"`
			AccountID string `json:"account_id"`
		} `json:"account_change"`
		BI *struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"business_intelligence_update"`
	}
	_, err := c.do(ctx, http.MethodPost, "/replay/play", map[string]string{"manifest_id": manifestID}, &res)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusConflict && ae.Code == "already_released" {
		return PlayOutcome{AlreadyReleased: true}, nil
	}
	if err != nil {
		return PlayOutcome{}, err
	}
	out := PlayOutcome{AccountChangeID: res.Change.ID, AccountID: res.Change.AccountID}
	if res.BI != nil {
		out.BIUpdateID, out.BISummary = res.BI.ID, res.BI.Summary
	}
	return out, nil
}

// Run reads one run with its generation status (GET /runs/{id}). Core does not serve the run list yet (GET /runs
// answers 404 until WP9), so DemoCore takes the run ids from Postgres and reads each run here.
func (c CoreClient) Run(ctx context.Context, runID string) (RunInfo, error) {
	var res struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		Generation *struct {
			Phase         string  `json:"phase"`
			Reason        *string `json:"reason"`
			StrategySetID *string `json:"strategy_set_id"`
			EpisodeID     *string `json:"decision_episode_id"`
		} `json:"generation"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID), nil, &res); err != nil {
		return RunInfo{}, err
	}
	r := RunInfo{ID: res.ID, Status: res.Status}
	if g := res.Generation; g != nil {
		r.Phase = g.Phase
		r.Reason, r.StrategySetID, r.EpisodeID = deref(g.Reason), deref(g.StrategySetID), deref(g.EpisodeID)
	}
	return r, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// SurfaceMessageTS returns the Slack ts of a posted message, "" when it is not posted (404) or only reserved.
func (c CoreClient) SurfaceMessageTS(ctx context.Context, subjectID, surface, kind string) (string, error) {
	var res struct {
		TS *string `json:"ts"`
	}
	_, err := c.do(ctx, http.MethodGet, "/surface-messages/"+url.PathEscape(subjectID)+"/"+url.PathEscape(surface)+"/"+url.PathEscape(kind), nil, &res)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return deref(res.TS), nil
}

// PageExists reports whether a web page answers 2xx (used to print only URLs that exist in this build).
func PageExists(ctx context.Context, pageURL string) bool {
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, pageURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode >= 200 && resp.StatusCode <= 299
}
