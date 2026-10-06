package worldtest

import (
	"net/http"
	"testing"
)

// End to end through the handlers: GET /accounts/{id}/graph?world_as_of=T, served without Neo4j.
func TestGraphEndpointWorldAsOfLeaksNothingAtOrAfterT(t *testing.T) {
	s := newStack(t)
	w := s.world
	const tmpl = "/accounts/{account_id}/graph"
	base := "/accounts/" + w.Account + "/graph?limit=20&include_closed=true&world_as_of="

	for k := 1; k <= 5; k++ {
		r := s.get(base+stamp(w.At(k)), tmpl)
		if r.status != http.StatusOK {
			t.Fatalf("T=E%d: %d %s", k, r.status, r.body)
		}
		body := string(r.body)
		what := "GET graph?world_as_of=E" + itoa(k)
		assertNoneOf(t, what, body, ids(w, k, 5)...)
		assertNoneOf(t, what, body, sourceEvents(w, k, 5)...)
		assertNoneOf(t, what, body, claimIDs(t, k, 5, w)...)
		if k <= 4 {
			assertNoneOf(t, what, body, w.SignalID)
		}
	}
	// The same shape as a current read: the contract check above is the one a Neo4j read passes.
	r := s.get(base+stamp(w.At(4)), tmpl)
	assertNoneOf(t, "world graph at E4", string(r.body), "state_not_computed")
}

func TestGraphEndpointWorldAsOfErrorsAndTheCurrentReadNeedingNeo4j(t *testing.T) {
	s := newStack(t)
	w := s.world
	const tmpl = "/accounts/{account_id}/graph"
	if r := s.get("/accounts/"+w.Account+"/graph?world_as_of=yesterday", tmpl); r.status != http.StatusBadRequest {
		t.Errorf("malformed world_as_of: %d %s", r.status, r.body)
	}
	if r := s.get("/accounts/"+w.Account+"/graph?limit=99&world_as_of="+stamp(w.At(3)), tmpl); r.status != http.StatusBadRequest {
		t.Errorf("limit 99: %d %s", r.status, r.body)
	}
	if r := s.get("/accounts/99999999-9999-4999-8999-999999999999/graph?world_as_of="+stamp(w.At(3)), tmpl); r.status != http.StatusNotFound {
		t.Errorf("unknown account: %d %s", r.status, r.body)
	}
	if r := s.do("/accounts/"+w.Account+"/graph?world_as_of="+stamp(w.At(3)), "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.status)
	}
	// Without world_as_of this stack has no graph database, and says so instead of crashing.
	r := s.get("/accounts/"+w.Account+"/graph", tmpl)
	if r.status != http.StatusServiceUnavailable || errorCode(t, r) != "graph_unavailable" {
		t.Errorf("current read without Neo4j: %d %s", r.status, r.body)
	}
}
