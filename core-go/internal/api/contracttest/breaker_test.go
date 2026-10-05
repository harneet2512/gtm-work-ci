package contracttest

import (
	"encoding/json"
	"testing"
)

func decodeBreaker(t *testing.T, r reply) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("%s: %v", clip(r.body), err)
	}
	return m
}

// TestProviderBreakerEndpointsConformToTheContract: status, trip, operator reset, and auth (HAR-135).
func TestProviderBreakerEndpointsConformToTheContract(t *testing.T) {
	s := newStack(t)
	testBreaker.Reset()
	if r := s.get("/provider-breaker", "/provider-breaker"); r.status != 200 || decodeBreaker(t, r)["state"] != "closed" {
		t.Fatalf("status: %d %s", r.status, clip(r.body))
	}
	testBreaker.Failure("worker 424 provider_unavailable_nonretryable")
	testBreaker.Failure("worker 424 provider_unavailable_nonretryable")
	r := s.get("/provider-breaker", "/provider-breaker")
	if m := decodeBreaker(t, r); m["state"] != "open" || m["opened_at"] == nil || m["trips_total"].(float64) < 1 {
		t.Fatalf("open breaker: %s", clip(r.body))
	}
	r = s.do("POST", "/provider-breaker/reset", "/provider-breaker/reset", apiToken, nil)
	if m := decodeBreaker(t, r); r.status != 200 || m["state"] != "closed" || m["consecutive_failures"].(float64) != 0 {
		t.Fatalf("reset: %d %s", r.status, clip(r.body))
	}
	if r := s.do("GET", "/provider-breaker", "/provider-breaker", "", nil); r.status != 401 {
		t.Fatalf("unauthenticated status read: %d", r.status)
	}
	if r := s.do("POST", "/provider-breaker/reset", "/provider-breaker/reset", "wrong-token", nil); r.status != 401 {
		t.Fatalf("a reset needs the operator token: %d", r.status)
	}
	if r := s.do("DELETE", "/provider-breaker", "", apiToken, nil); r.status != 405 {
		t.Fatalf("method: %d", r.status)
	}
}
