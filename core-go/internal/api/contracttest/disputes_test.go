package contracttest

import (
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evaldispute/disputetest"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore/strategytest"
)

// TestEvalDisputeEndpointConformsToTheContract walks POST /eval-results/{eval_result_id}/disputes over HTTP
// against a seeded EvalResult: created, an identical repeat, a reasoning-only dispute and every refusal, each
// response checked against core.yaml and eval_dispute.v1.json.
func TestEvalDisputeEndpointConformsToTheContract(t *testing.T) {
	s := newStack(t)
	seed := strategytest.Seed(t, env.DB, s.world.AccountA)
	result := disputetest.SeedResult(t, env.DB, disputetest.Result{RunID: seed.RunID, DraftIndex: 1, EvalType: "champion_continuity", Verdict: "fail", Blocking: true})
	path, tmpl := "/eval-results/"+result+"/disputes", "/eval-results/{eval_result_id}/disputes"
	body := map[string]any{"reason": "Marco owns the security review; this should warn, not block.", "expected_verdict": "warn",
		"surface": "web", "actor_label": "Dana Kim"}

	first := s.post(path, tmpl, body)
	if first.status != 201 || jsonField(t, first, "disputed_verdict") != "fail" || jsonField(t, first, "disputed_blocking") != true {
		t.Fatalf("dispute: %d %s", first.status, clip(first.body))
	}
	again := s.post(path, tmpl, body)
	if again.status != 200 || jsonField(t, again, "id") != jsonField(t, first, "id") {
		t.Fatalf("an identical dispute returns the stored one with 200: %d %s", again.status, clip(again.body))
	}
	reasoning := s.post(path, tmpl, map[string]any{"reason": "The quote is from the wrong call.", "surface": "slack", "actor_label": "Dana"})
	if reasoning.status != 201 || jsonField(t, reasoning, "expected_verdict") != nil {
		t.Fatalf("reasoning-only dispute: %d %s", reasoning.status, clip(reasoning.body))
	}

	s.expectError(s.post("/eval-results/"+missingID+"/disputes", tmpl, body), 404, "not_found")
	s.expectError(s.post(path, tmpl, map[string]any{"reason": "Should fail.", "expected_verdict": "fail", "surface": "web", "actor_label": "Dana"}), 422, "expected_equals_verdict")
	s.expectError(s.post(path, tmpl, map[string]any{"reason": "x", "surface": "web", "actor_label": "Dana", "actor_person_id": missingID}), 422, "unknown_person")
	if r := s.do("POST", path, tmpl, apiToken, []byte(`{"reason":"   ","surface":"web","actor_label":"Dana"}`)); r.status != 422 || errorCode(t, r) != "invalid_request" {
		t.Fatalf("a blank reason: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", path, tmpl, apiToken, []byte(`{"reason":"x","surface":"web","actor_label":"Dana","score":1}`)); r.status != 400 {
		t.Fatalf("an unknown field: %d %s", r.status, clip(r.body))
	}
	if r := s.do("POST", path, tmpl, apiToken, []byte(`not json`)); r.status != 400 {
		t.Fatalf("not json: %d", r.status)
	}
	if r := s.do("POST", path, tmpl, "", []byte(`{}`)); r.status != 401 {
		t.Fatalf("no token: %d", r.status)
	}
	if r := s.do("GET", path, "", apiToken, nil); r.status != 405 {
		t.Fatalf("GET is not allowed: %d", r.status)
	}
	if r := s.do("POST", "/eval-results/not-a-uuid/disputes", "", apiToken, []byte(`{"reason":"x","surface":"web","actor_label":"a"}`)); r.status != 404 {
		t.Fatalf("a malformed id: %d", r.status)
	}
}
