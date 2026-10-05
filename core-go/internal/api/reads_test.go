package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/readmodel"
)

// okReads answers Account with a fixed summary; everything else stays unimplemented for the test.
type okReads struct {
	stubReads
	account readmodel.Account
	gotID   string
}

func (s *okReads) Account(_ context.Context, id string) (readmodel.Account, error) {
	s.gotID = id
	return s.account, nil
}

func TestGetAccountServesTheAccountSummary(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stage, domain, run := "negotiation", "acme.example.test", "0f0a0000-0000-4000-8000-000000000601"
	reads := &okReads{account: readmodel.Account{ID: testRun, Name: "Acme", Domain: &domain,
		Stage: &stage, LastActivityAt: &at, OpenRunID: &run}}
	h, err := NewHandler(&fakeService{}, testToken, nil, WithReads(reads))
	if err != nil {
		t.Fatal(err)
	}

	rec := do(h, http.MethodGet, "/accounts/"+testRun, "", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if reads.gotID != testRun {
		t.Fatalf("the handler passed %q as the account id", reads.gotID)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != testRun || got["name"] != "Acme" || got["stage"] != "negotiation" ||
		got["domain"] != domain || got["open_run_id"] != run {
		t.Fatalf("summary = %v", got)
	}
	if got["last_activity_at"] != "2026-01-02T03:04:05Z" {
		t.Fatalf("last_activity_at = %v", got["last_activity_at"])
	}
	// The contract-required nulls stay present for unset fields.
	for _, k := range []string{"health", "motion", "last_meaningful_change"} {
		v, has := got[k]
		if !has || v != nil {
			t.Fatalf("%s = %v, want an explicit null", k, v)
		}
	}
	if rec := do(h, http.MethodGet, "/accounts/"+testRun, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed: %d", rec.Code)
	}
}
