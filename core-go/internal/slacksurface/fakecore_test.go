package slacksurface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "core-test-token"

// memCore is an in-memory stand-in for the core API with the idempotency rules of the contract: a
// repeated choose of the same candidate returns the existing decision, a different one is 409
// selection_locked, and a repeated send is 409 already_decided with the existing record.
type memCore struct {
	mu       sync.Mutex
	fx       Fixture
	decision *HumanStrategyDecision
	inf      JudgmentInference
	requests []string // "<what> <body>" of every write, for assertions
	sends    int
	failNext error

	// stateOnly are people core's graph does not return (it is bounded) but the account state's buying group lists: the real shape.
	stateOnly map[string]bool

	inferenceHeld bool // the inference is not ready yet
	sendRefused   bool // send answers 422 (policy)
}

func newMemCore() *memCore {
	fx := NewFixture()
	return &memCore{fx: fx, inf: fx.Inference}
}

func (c *memCore) GetBIUpdate(context.Context, string) (BusinessIntelligenceUpdate, error) {
	return c.fx.BI, nil
}

func (c *memCore) GetAccountName(context.Context, string) (string, error) {
	return c.fx.AccountName, nil
}

func (c *memCore) People(context.Context, string) (Directory, error) { return c.fx.Directory, nil }

func (c *memCore) GetStrategies(context.Context, string) (RunStrategies, error) {
	return c.fx.Strategies, nil
}

func (c *memCore) GetStrategyDecision(context.Context, string) (HumanStrategyDecision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decision == nil {
		return HumanStrategyDecision{}, ErrNotFound
	}
	return *c.decision, nil
}

func (c *memCore) RecordStrategyDecision(_ context.Context, run string, req StrategyDecisionRequest) (HumanStrategyDecision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(req)
	c.requests = append(c.requests, "strategy-decision "+run+" "+string(b))
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return HumanStrategyDecision{}, err
	}
	cand, ok := c.fx.Strategies.Candidate(req.SelectedCandidateID)
	if !ok {
		return HumanStrategyDecision{}, ErrNotFound
	}
	if d := c.decision; d != nil {
		switch {
		case d.SendDecision != SendPending:
			return HumanStrategyDecision{}, &ConflictError{Code: CodeAlreadyDecided, Decision: d}
		case d.SelectedCandidateID != cand.CandidateID:
			return HumanStrategyDecision{}, &ConflictError{Code: CodeSelectionLocked, Decision: d}
		case req.FinalTo == nil && req.FinalCC == nil && req.FinalArtifact == nil:
			return *d, nil // same candidate again: the existing decision
		}
	}
	d := HumanStrategyDecision{
		ID: "0d5d0000-0000-4000-8000-000000000c01", DecisionEpisodeID: c.fx.Strategies.StrategySet.DecisionEpisodeID,
		AgentRunID: run, StrategySetID: c.fx.Strategies.StrategySet.ID, SelectedCandidateID: cand.CandidateID,
		OriginalAgentPreference: fixtureCandA, Surface: req.Surface, ActorLabel: req.ActorLabel,
		Edits: []json.RawMessage{}, SendDecision: SendPending,
	}
	if c.decision != nil {
		d = *c.decision
	}
	if req.FinalTo != nil {
		d.FinalTo = *req.FinalTo
	}
	if req.FinalCC != nil {
		d.FinalCC = *req.FinalCC
	}
	if req.FinalArtifact != nil {
		d.FinalArtifact = req.FinalArtifact
	}
	if req.FinalTo != nil || req.FinalCC != nil || req.FinalArtifact != nil {
		d.Edits = []json.RawMessage{json.RawMessage(`{"kind":"edited"}`)}
	}
	c.decision = &d
	return d, nil
}

func (c *memCore) SendRun(_ context.Context, run string, req SendRequest) (HumanStrategyDecision, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(req)
	c.requests = append(c.requests, "send "+run+" "+string(b))
	if c.failNext != nil {
		err := c.failNext
		c.failNext = nil
		return HumanStrategyDecision{}, err
	}
	switch {
	case c.decision == nil:
		return HumanStrategyDecision{}, &ConflictError{Code: CodeNoChoice}
	case c.decision.SendDecision != SendPending:
		return HumanStrategyDecision{}, &ConflictError{Code: CodeAlreadyDecided, Decision: c.decision}
	case c.sendRefused:
		return HumanStrategyDecision{}, &RefusedError{Message: "a blocking eval is unresolved"}
	}
	now := time.Date(2026, 3, 5, 9, 30, 0, 0, time.UTC)
	c.decision.SendDecision, c.decision.SendDecidedAt = req.Decision, &now
	c.sends++
	return *c.decision, nil
}

func (c *memCore) GetJudgmentInference(context.Context, string) (JudgmentInference, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inferenceHeld || c.decision == nil || c.decision.SendDecision != SendSend {
		return JudgmentInference{}, ErrNotReady
	}
	return c.inf, nil
}

func (c *memCore) SubmitJudgmentVerdict(_ context.Context, _ string, req VerdictRequest) (JudgmentInference, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(req)
	c.requests = append(c.requests, "judgment-verdict "+string(b))
	if req.Verdict != "" {
		// core's locks: learning declined is terminal for confirm/correct, and a correction cannot be withdrawn.
		cur := c.inf.HumanVerdict
		if (cur == VerdictNoLearning && req.Verdict != VerdictNoLearning) || (cur == VerdictCorrected && req.Verdict == VerdictNoLearning) {
			return JudgmentInference{}, &RefusedError{Message: "verdict_locked"}
		}
		if cur != VerdictPending {
			return JudgmentInference{}, &ConflictError{Code: "verdict_recorded"}
		}
		c.inf.HumanVerdict = req.Verdict
		if req.CorrectedStatement != "" {
			s := req.CorrectedStatement
			c.inf.CorrectedStatement = &s
		}
	}
	if req.Note != "" {
		n := req.Note
		c.inf.HumanNote = &n
	}
	return c.inf, nil
}

func (c *memCore) writes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests...)
}

var _ Core = (*memCore)(nil)

// httpCore exposes a Core over the contract's HTTP paths, requiring the bearer token.
func httpCore(t *testing.T, c Core) *httptest.Server {
	t.Helper()
	refs := newMemRefs() // core's surface_messages, so a Publisher over CoreHTTP is exactly-once as in production
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, `{"error":{"code":"unauthorized","message":"no"}}`, http.StatusUnauthorized)
			return
		}
		if routeRefs(w, r, refs) {
			return
		}
		routeCore(w, r, c)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func reply(w http.ResponseWriter, v any, err error) {
	var ce *ConflictError
	var re *RefusedError
	switch {
	case errors.Is(err, ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"missing"}}`))
	case errors.Is(err, ErrNotReady):
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"strategies_not_ready","message":"later"}}`))
	case errors.As(err, &ce):
		w.WriteHeader(http.StatusConflict)
		if ce.Decision != nil && ce.Code == CodeAlreadyDecided { // already_decided carries the existing record
			_ = json.NewEncoder(w).Encode(ce.Decision)
		} else {
			_, _ = w.Write([]byte(`{"error":{"code":"` + ce.Code + `","message":"conflict"}}`))
		}
	case errors.As(err, &re):
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"refused","message":"` + re.Message + `"}}`))
	case err != nil:
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"boom"}}`))
	default:
		_ = json.NewEncoder(w).Encode(v)
	}
}

func routeCore(w http.ResponseWriter, r *http.Request, c Core) {
	ctx := r.Context()
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case len(p) == 3 && p[0] == "accounts" && p[2] == "graph":
		dir, err := c.People(ctx, p[1])
		nodes := []map[string]any{{"id": "x", "type": "account", "label": "acct"}}
		hidden := map[string]bool{}
		if mc, ok := c.(*memCore); ok {
			hidden = mc.stateOnly
		}
		for _, pr := range dir {
			if hidden[pr.ID] {
				continue
			}
			// core types its nodes "Person"; its graph carries no email for most people
			data := map[string]any{"kind": "contact"}
			if pr.Email != "" {
				data["email"] = pr.Email
			}
			nodes = append(nodes, map[string]any{"id": pr.ID, "type": "Person", "label": pr.Name, "data": data})
		}
		reply(w, map[string]any{"nodes": nodes, "edges": []any{}}, err)
	case len(p) == 3 && p[0] == "accounts" && p[2] == "state":
		dir, err := c.People(ctx, p[1])
		group := []map[string]any{}
		for _, pr := range dir {
			group = append(group, map[string]any{"person_id": pr.ID, "display_name": pr.Name})
		}
		reply(w, map[string]any{"account_id": p[1], "buying_group": group}, err)
	case len(p) == 2 && p[0] == "accounts":
		name, err := c.GetAccountName(ctx, p[1])
		reply(w, map[string]any{"id": p[1], "name": name}, err)
	case len(p) == 4 && p[0] == "accounts" && p[2] == "business-intelligence":
		v, err := c.GetBIUpdate(ctx, p[1])
		reply(w, v, err)
	case len(p) == 3 && p[0] == "runs" && p[2] == "strategies":
		v, err := c.GetStrategies(ctx, p[1])
		reply(w, v, err)
	case len(p) == 3 && p[0] == "runs" && p[2] == "strategy-decision" && r.Method == http.MethodGet:
		v, err := c.GetStrategyDecision(ctx, p[1])
		reply(w, v, err)
	case len(p) == 3 && p[0] == "runs" && p[2] == "strategy-decision":
		var req StrategyDecisionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		v, err := c.RecordStrategyDecision(ctx, p[1], req)
		reply(w, v, err)
	case len(p) == 3 && p[0] == "runs" && p[2] == "send":
		var req SendRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		v, err := c.SendRun(ctx, p[1], req)
		reply(w, v, err)
	case len(p) == 3 && p[0] == "episodes" && p[2] == "judgment-inference":
		v, err := c.GetJudgmentInference(ctx, p[1])
		reply(w, v, err)
	case len(p) == 3 && p[0] == "episodes" && p[2] == "judgment-verdict":
		var req VerdictRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		v, err := c.SubmitJudgmentVerdict(ctx, p[1], req)
		reply(w, v, err)
	default:
		reply(w, nil, ErrNotFound)
	}
}

// routeRefs serves the message refs of the contract over a memRefs; it reports whether the path was one of them.
func routeRefs(w http.ResponseWriter, r *http.Request, refs *memRefs) bool {
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(p) < 4 || p[0] != "surface-messages" {
		return false
	}
	ctx, subject, kind := r.Context(), p[1], p[3]
	asJSON := func(rec RefRecord) refJSON {
		out := refJSON{SubjectID: rec.SubjectID, Kind: rec.Kind, Channel: rec.Channel, ReservedAt: rec.ReservedAt}
		if rec.TS != "" {
			out.TS = &rec.TS
		}
		return out
	}
	switch {
	case len(p) == 4 && r.Method == http.MethodGet:
		rec, err := refs.GetRef(ctx, subject, kind)
		reply(w, asJSON(rec), err)
	case len(p) == 5 && p[4] == "reservation" && r.Method == http.MethodPost:
		var body struct {
			Channel string `json:"channel"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec, created, err := refs.ReserveRef(ctx, subject, kind, body.Channel)
		reply(w, map[string]any{"created": created, "message": asJSON(rec)}, err)
	case len(p) == 5 && p[4] == "ts" && r.Method == http.MethodPut:
		var body struct {
			TS string `json:"ts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec, err := refs.RecordRefTS(ctx, subject, kind, body.TS)
		var conflict *RefTSConflictError
		if errors.As(err, &conflict) {
			err = &ConflictError{Code: CodeTSConflict}
		}
		reply(w, asJSON(rec), err)
	default:
		reply(w, nil, ErrNotFound)
	}
	return true
}
