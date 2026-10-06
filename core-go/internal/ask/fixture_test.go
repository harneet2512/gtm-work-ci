package ask

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The MedTech fixture: MedTech Advances, whose held-out Event N is 2023-11-09 (bench/reports/demo-cases-2026-10-04.md).
const (
	acctID     = "0a000000-0000-4000-8000-000000000001"
	runID      = "0a000000-0000-4000-8000-000000000002"
	episodeID  = "0a000000-0000-4000-8000-000000000003"
	manifestID = "0a000000-0000-4000-8000-000000000009"
	webBase    = "http://web.test"
	eventN     = "2023-11-09T09:30:00Z"
)

type reply struct {
	status int
	body   any
}

// fakeBackend serves canned JSON by path (with the query first, then without) and records every call.
type fakeBackend struct {
	mu     sync.Mutex
	routes map[string]reply
	gets   []string
	posts  []string
}

func (f *fakeBackend) Get(_ context.Context, path string) (int, []byte, error) {
	f.mu.Lock()
	f.gets = append(f.gets, path)
	f.mu.Unlock()
	return f.answer(path)
}

func (f *fakeBackend) Post(_ context.Context, path string) (int, []byte, error) {
	f.mu.Lock()
	f.posts = append(f.posts, path)
	f.mu.Unlock()
	return f.answer("POST " + path)
}

func (f *fakeBackend) answer(path string) (int, []byte, error) {
	r, ok := f.routes[path]
	if !ok {
		base, _, _ := strings.Cut(path, "?")
		r, ok = f.routes[base]
	}
	if !ok {
		return 404, []byte(`{"error":{"code":"not_found","message":"x"}}`), nil
	}
	b, _ := json.Marshal(r.body)
	return r.status, b, nil
}

// getQuery returns the decoded query of the first recorded GET whose path starts with prefix.
func (f *fakeBackend) getQuery(t *testing.T, prefix string) url.Values {
	t.Helper()
	for _, g := range f.gets {
		if strings.HasPrefix(g, prefix) {
			_, raw, _ := strings.Cut(g, "?")
			v, _ := url.ParseQuery(raw)
			return v
		}
	}
	t.Fatalf("no GET with prefix %s in %v", prefix, f.gets)
	return nil
}

func ok(body any) reply { return reply{200, body} }

func medtech() *fakeBackend {
	acc := "/accounts/" + acctID
	trace := map[string]any{"episode_id": episodeID, "spans": []any{
		map[string]any{"id": "knowledge_retrieved:k1", "kind": "knowledge_retrieved", "title": "Retrieved: security review first", "status": "ok", "summary": "2 retrieved", "attributes": map[string]any{"knowledge_ids": []any{"K1", "K2"}}},
		map[string]any{"id": "knowledge_applicable:k1", "kind": "knowledge_applicable", "title": "Applicable: security review first", "status": "ok", "summary": "1 applicable", "attributes": map[string]any{"knowledge_ids": []any{"K1"}}},
		map[string]any{"id": "knowledge_used:k1", "kind": "knowledge_used", "title": "Used: security review first", "status": "ok", "summary": "cited in candidate 1", "attributes": map[string]any{"influence_measured": false}},
		map[string]any{"id": "state:v7", "kind": "state", "title": "State", "status": "ok", "summary": "x"},
	}}
	return &fakeBackend{routes: map[string]reply{
		"/accounts": ok(map[string]any{"items": []any{
			map[string]any{"id": acctID, "name": "MedTech Advances", "stage": "Negotiation", "health": "at_risk", "motion": "expansion", "last_meaningful_change": "Security review requested", "last_activity_at": eventN},
			map[string]any{"id": "0a000000-0000-4000-8000-0000000000aa", "name": "EcoLite Innovations", "stage": "Proposal"}}, "next_cursor": nil}),
		acc:                                   ok(map[string]any{"id": acctID, "name": "MedTech Advances"}),
		acc + "/state":                        ok(map[string]any{"stage": "Negotiation", "buyer_intent": "evaluating", "next_step": "send security package", "as_of": eventN}),
		acc + "/diffs":                        ok(map[string]any{"items": []any{map[string]any{"id": "d1", "changes": []any{map[string]any{"field": "blockers_risk", "after": "security review pending"}}}}}),
		acc + "/business-intelligence/latest": ok(map[string]any{"account_id": acctID, "headline": "MedTech asked for a security review before signing", "created_at": eventN}),
		acc + "/timeline": ok(map[string]any{"items": []any{
			map[string]any{"id": "a2", "type": "EmailReceived", "occurred_at": eventN, "summary": "Buyer: we need the security review before we sign"},
			map[string]any{"id": "a1", "type": "EmailSent", "occurred_at": "2023-11-01T09:30:00Z", "summary": "Seller sent the quote"}}}),
		acc + "/graph": ok(map[string]any{"nodes": []any{
			map[string]any{"id": "p1", "type": "person", "label": "Dana Reyes"}, map[string]any{"id": "p2", "type": "person", "label": "Sam Ortiz"}}}),
		"/runs":          ok(map[string]any{"items": []any{map[string]any{"id": runID, "account_id": acctID, "generation": map[string]any{"decision_episode_id": episodeID}}}, "next_cursor": nil}),
		"/runs/" + runID: ok(map[string]any{"id": runID, "generation": map[string]any{"decision_episode_id": episodeID}}),
		"/episodes/" + episodeID: ok(map[string]any{"id": episodeID, "agent_run_id": runID, "account_id": acctID, "account_name": "MedTech Advances",
			"recommended_action": "A: send the security package", "selected_action": "A"}),
		"/episodes/" + episodeID + "/gate-results": ok(map[string]any{"episode_id": episodeID, "items": []any{
			map[string]any{"gate": "D4", "sub_gate": "", "verdict": "warn", "question": "Is the ask specific?", "observed": "no date", "why": "The email names no date", "evidence_refs": []any{"activity:a2"}, "span_id": "candidates:c1"},
			map[string]any{"gate": "B2", "verdict": "pass", "question": "Is the state supported?", "observed": "yes", "why": "cited", "evidence_refs": []any{"activity:a1"}, "span_id": "state:v7"}}}),
		"/episodes/" + episodeID + "/trace": ok(trace),
		"/runs/" + runID + "/strategies": ok(map[string]any{"strategy_set": map[string]any{"candidates": []any{
			map[string]any{"strategy_type": "send_package_and_wait", "preferred_by_agent": true, "ranking": 1, "to": []any{map[string]any{"person_id": "p1"}}, "cc": []any{map[string]any{"person_id": "p2"}},
				"subject": "Security package", "full_action_artifact": map[string]any{"subject": "Security package", "body": "Hi Dana, attached is the security package."}, "rationale": "The buyer asked for it."},
			map[string]any{"strategy_type": "wait", "preferred_by_agent": false, "ranking": 2}}}, "eval_bundles": []any{}}),
		"/knowledge": ok(map[string]any{"items": []any{map[string]any{"id": "K1", "statement": "Lead with the security review for MedTech Advances style deals", "status": "supported"}}}),
		"/replay/manifests/" + manifestID + "/progress": ok(map[string]any{"overall": "running", "stages": []any{
			map[string]any{"stage": "ingest", "status": "completed"}, map[string]any{"stage": "evals", "status": "waiting"}}}),
		"/replay/manifests/" + manifestID + "/episodes":           ok(map[string]any{"account_id": acctID, "episode": 12, "total": 13}),
		"POST /replay/manifests/" + manifestID + "/episodes/next": ok(map[string]any{"episode": 13, "total": 13}),
	}}
}

func newTools(b Backend) *Tools {
	fixed := time.Date(2023, 11, 20, 12, 0, 0, 0, time.UTC)
	return &Tools{B: b, L: NewLinks(webBase), Clock: func() time.Time { return fixed }}
}

func run(t *testing.T, tl *Tools, tool string, args map[string]any) ToolResult {
	t.Helper()
	res, err := tl.Run(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s(%v): %v", tool, args, err)
	}
	return res
}

func dataText(r ToolResult) string { b, _ := json.Marshal(r.Data); return string(b) }
