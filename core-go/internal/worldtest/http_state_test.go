package worldtest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func stamp(t time.Time) string { return url.QueryEscape(t.UTC().Format(time.RFC3339Nano)) }

// End to end through the handlers: GET /accounts/{id}/state?world_as_of=T for every event.
func TestStateEndpointWorldAsOfLeaksNothingAtOrAfterT(t *testing.T) {
	s := newStack(t)
	w := s.world
	base := "/accounts/" + w.Account + "/state?world_as_of="
	const tmpl = "/accounts/{account_id}/state"

	r := s.get(base+stamp(w.At(1)), tmpl)
	if r.status != http.StatusNotFound || errorCode(t, r) != "state_not_computed_before" {
		t.Fatalf("T=E1: %d %s", r.status, r.body)
	}
	for k := 2; k <= 5; k++ {
		r := s.get(base+stamp(w.At(k)), tmpl)
		if r.status != http.StatusOK {
			t.Fatalf("T=E%d: %d %s", k, r.status, r.body)
		}
		body := string(r.body)
		assertNoneOf(t, "GET state?world_as_of=E"+itoa(k), body, markersFrom(k)...)
		assertNoneOf(t, "GET state?world_as_of=E"+itoa(k), body, ids(w, k, 5)...)
		if got := versionOf(t, r.body); got != k-1 {
			t.Errorf("T=E%d: version %d, want %d", k, got, k-1)
		}
	}
}

func TestStateEndpointKeepsComputedAtSemanticsAndRejectsMixingThem(t *testing.T) {
	s := newStack(t)
	w := s.world
	const tmpl = "/accounts/{account_id}/state"
	prefix := "/accounts/" + w.Account + "/state?"

	// as_of is computed-at: the replay computed everything in October, so E3's September time precedes every computation.
	r := s.get(prefix+"as_of="+stamp(w.At(3)), tmpl)
	if r.status != http.StatusNotFound || errorCode(t, r) != "state_not_computed" {
		t.Fatalf("as_of=E3: %d %s", r.status, r.body)
	}
	if r := s.get(prefix+"as_of="+stamp(time.Now().Add(time.Hour)), tmpl); r.status != http.StatusOK {
		t.Fatalf("as_of=future: %d %s", r.status, r.body)
	}
	if r := s.get(prefix+"as_of="+stamp(w.At(3))+"&world_as_of="+stamp(w.At(3)), tmpl); r.status != http.StatusBadRequest {
		t.Errorf("both parameters: %d %s", r.status, r.body)
	}
	if r := s.get(prefix+"world_as_of=yesterday", tmpl); r.status != http.StatusBadRequest {
		t.Errorf("malformed world_as_of: %d %s", r.status, r.body)
	}
	if r := s.get("/accounts/99999999-9999-4999-8999-999999999999/state?world_as_of="+stamp(w.At(3)), tmpl); r.status != http.StatusNotFound || errorCode(t, r) != "not_found" {
		t.Errorf("unknown account: %d %s", r.status, r.body)
	}
	if r := s.do(prefix+"world_as_of="+stamp(w.At(3)), "", ""); r.status != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.status)
	}
	if body := string(s.get(prefix+"world_as_of="+stamp(w.At(5)), tmpl).body); strings.Contains(body, "Closed Won") {
		t.Errorf("T=E5 must not show E5's stage: %s", body)
	}
}
